// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires

/* Messages */
function messagesPage() {
    return {
        messages: [],
        total: 0,
        offset: 0,
        limit: 50,
        loading: false,
        chatFilter: '',   // chat_id as string ('' = all chats)
        userFilter: '',   // username or user id substring ('' = all users)
        get currentPage() { return Math.floor(this.offset / this.limit) + 1; },
        get totalPages() { return Math.ceil(this.total / this.limit) || 1; },
        get pageNumbers() { return paginationWindow(this.currentPage, this.totalPages); },
        get chats() { return Alpine.store('app').chats || []; },

        // syncRoute is the route-driven entry point (called from x-effect with
        // $store.app.routeParams). It pulls offset/filters out of the URL and
        // fetches. The actual work is deferred to a microtask so the fetch()'s
        // reads of local reactive fields are NOT captured as dependencies of
        // the x-effect — otherwise typing in the filter box (which mutates
        // those fields) would retrigger the effect and clobber the input.
        syncRoute(params) {
            queueMicrotask(() => {
                this.offset = Math.max(0, parseInt(params.offset, 10) || 0);
                this.chatFilter = params.chat || '';
                this.userFilter = params.user || '';
                this.fetch();
            });
        },
        // _go pushes a new history entry with the given offset, preserving the
        // active filters. The route change drives the actual fetch via syncRoute.
        _go(offset) {
            const p = {};
            if (offset) p.offset = offset;
            if (this.chatFilter) p.chat = this.chatFilter;
            if (this.userFilter) p.user = this.userFilter.trim();
            Alpine.store('app').setRoute(p);
        },
        async fetch() {
            this.loading = true;
            try {
                let q = '/api/messages?limit=' + this.limit + '&offset=' + this.offset;
                if (this.chatFilter) q += '&chat_id=' + encodeURIComponent(this.chatFilter);
                if (this.userFilter) q += '&user=' + encodeURIComponent(this.userFilter.trim());
                const d = await apiJSON(q);
                this.messages = d.messages || [];
                this.total = d.total || 0;
            } catch (e) { console.error(e); }
            this.loading = false;
        },
        applyFilters() {
            this._go(0);
        },
        clearFilters() {
            if (!this.chatFilter && !this.userFilter) return;
            this.chatFilter = '';
            this.userFilter = '';
            this._go(0);
        },
        changePage(dir) {
            this._go(Math.max(0, this.offset + dir * this.limit));
        },
        goToPage(n) {
            if (typeof n !== 'number') return;
            const off = (n - 1) * this.limit;
            if (off === this.offset || off < 0) return;
            this._go(off);
        },
        async deleteMsg(m) {
            if (!confirm(Alpine.store('i18n').t('msg_delete_confirm'))) return;
            try {
                await api('/api/messages/delete', {
                    method: 'DELETE',
                    body: JSON.stringify({ message_id: m.message_id, chat_id: m.chat_id })
                });
                this.messages = this.messages.filter(x => !(x.message_id === m.message_id && x.chat_id === m.chat_id));
                this.total--;
            } catch (e) { console.error(e); }
        },
        async deleteMsgFromChat(m) {
            const i18n = Alpine.store('i18n');
            if (!confirm(i18n.t('msg_delete_chat_confirm'))) return;
            try {
                const res = await api('/api/moderation/delete-message', {
                    method: 'POST',
                    body: JSON.stringify({
                        user_id: Number(m.user_id),
                        chat_id: Number(m.chat_id),
                        message_id: Number(m.message_id)
                    })
                });
                if (!res.ok) {
                    let detail = '';
                    try { const j = await res.json(); detail = i18n.err(j); } catch {}
                    alert(i18n.t('mod_action_failed') + (detail ? ': ' + detail : ' (' + res.status + ')'));
                    return;
                }
                m.action_type = 'delete';
            } catch (e) {
                console.error('delete-from-chat failed', e);
                alert(i18n.t('mod_action_failed') + ': ' + (e.message || ''));
            }
        },
        async modMsg(m, type) {
            await modAction({ user_id: m.user_id, chat_id: m.chat_id, message_id: m.message_id }, type);
        }
    };
}

/* Reply chain - lazy, recursive "Reply to" viewer for a message card.
 *
 * Rather than inlining parent text into every message, the list only carries
 * `reply_to_message_id`. Expanding the toggle fetches the immediate parent
 * (fully enriched) from /api/messages?message_id=…&chat_id=…; if that parent is
 * itself a reply, a "Reply to #…" button on the frontier loads the next
 * ancestor, walking the chain one hop at a time. The recursion is linearised
 * into the flat `nodes` array (nearest parent first, deepest last) so no
 * recursive markup is needed. */
function replyChain(root) {
    return {
        root,
        open: false,
        loading: false,
        failed: false,    // network/server error on the last fetch
        notFound: false,  // a parent id that isn't in the DB (chain end)
        nodes: [],        // loaded ancestors, nearest-first

        // The deepest known message whose parent we could load next: the last
        // loaded node, or the root when nothing is loaded yet.
        get frontier() { return this.nodes.length ? this.nodes[this.nodes.length - 1] : this.root; },
        get canGoDeeper() { return !this.loading && !this.notFound && !this.failed && this.hasParent(this.frontier); },

        // A real reply is one whose target isn't the forum topic's root message
        // (Telegram points in-topic posts at the root - that's membership, not a
        // reply).
        hasParent(m) {
            return !!m && !!m.reply_to_message_id && m.reply_to_message_id !== m.message_thread_id;
        },

        async toggle() {
            this.open = !this.open;
            if (this.open && this.nodes.length === 0 && !this.failed && !this.notFound) {
                await this.loadNext();
            }
        },

        async loadNext() {
            const src = this.frontier;
            if (!this.hasParent(src)) return;
            this.loading = true;
            this.failed = false;
            this.notFound = false;
            try {
                const data = await apiJSON('/api/messages?message_id=' + src.reply_to_message_id + '&chat_id=' + src.chat_id);
                const parent = (data && data.messages && data.messages[0]) || null;
                if (parent) this.nodes.push(parent);
                else this.notFound = true;
            } catch (e) {
                console.error('reply chain load failed', e);
                this.failed = true;
            }
            this.loading = false;
        }
    };
}

