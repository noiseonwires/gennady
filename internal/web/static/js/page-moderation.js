// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires

/* Moderation */
function moderationPage() {
    return {
        actions: [],
        muted: [],
        showMsgPopup: null,
        loading: false,
        limit: 25,
        // Independent pagination for each list (component state, not URL-synced,
        // so the two pagers never fight over the route).
        actionsOffset: 0,
        actionsTotal: 0,
        mutedOffset: 0,
        mutedTotal: 0,

        get actionsPage() { return Math.floor(this.actionsOffset / this.limit) + 1; },
        get actionsPages() { return Math.ceil(this.actionsTotal / this.limit) || 1; },
        get actionsPageNumbers() { return paginationWindow(this.actionsPage, this.actionsPages); },
        get mutedPage() { return Math.floor(this.mutedOffset / this.limit) + 1; },
        get mutedPages() { return Math.ceil(this.mutedTotal / this.limit) || 1; },
        get mutedPageNumbers() { return paginationWindow(this.mutedPage, this.mutedPages); },

        async load() {
            this.loading = true;
            await Promise.all([this.fetchActions(), this.fetchMuted()]);
            this.loading = false;
        },
        async fetchActions() {
            try {
                const d = await apiJSON('/api/actions?limit=' + this.limit + '&offset=' + this.actionsOffset);
                this.actions = d.actions || [];
                this.actionsTotal = d.total || 0;
            } catch (e) { console.error(e); }
        },
        async fetchMuted() {
            try {
                const d = await apiJSON('/api/muted?limit=' + this.limit + '&offset=' + this.mutedOffset);
                this.muted = d.users || [];
                this.mutedTotal = d.total || 0;
            } catch (e) { console.error(e); }
        },
        changeActionsPage(dir) {
            const off = Math.max(0, this.actionsOffset + dir * this.limit);
            if (off === this.actionsOffset) return;
            this.actionsOffset = off;
            this.fetchActions();
        },
        goActionsPage(n) {
            if (typeof n !== 'number') return;
            const off = (n - 1) * this.limit;
            if (off === this.actionsOffset || off < 0) return;
            this.actionsOffset = off;
            this.fetchActions();
        },
        changeMutedPage(dir) {
            const off = Math.max(0, this.mutedOffset + dir * this.limit);
            if (off === this.mutedOffset) return;
            this.mutedOffset = off;
            this.fetchMuted();
        },
        goMutedPage(n) {
            if (typeof n !== 'number') return;
            const off = (n - 1) * this.limit;
            if (off === this.mutedOffset || off < 0) return;
            this.mutedOffset = off;
            this.fetchMuted();
        },
        async unmuteUser(m) {
            const ok = await modAction({ user_id: m.user_id, chat_id: m.chat_id }, 'unmute');
            if (!ok) return;
            // Removing the last row on a page would leave an empty page; step back.
            if (this.muted.length === 1 && this.mutedOffset >= this.limit) {
                this.mutedOffset = Math.max(0, this.mutedOffset - this.limit);
            }
            await this.fetchMuted();
        },
        async showMuteMsg(m) {
            if (!m.message_id || m.message_id === 0) return;
            try {
                const data = await apiJSON('/api/messages?limit=1&offset=0&message_id=' + m.message_id + '&chat_id=' + m.chat_id);
                const msgs = data.messages || [];
                const msg = msgs.length > 0 ? msgs[0] : null;
                const text = msg ? msg.text : '';
                this.showMsgPopup = { username: m.username, chat_name: m.chat_name, _msgText: text || '-', moderation_reason: (msg && msg.moderation_reason) || '' };
            } catch (e) {
                this.showMsgPopup = { username: m.username, chat_name: m.chat_name, _msgText: '-' };
            }
        }
    };
}

