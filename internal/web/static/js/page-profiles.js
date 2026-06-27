// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires

/* Profiles */
function profilesPage() {
    return {
        profiles: [],
        total: 0,
        offset: 0,
        limit: 25,
        sort: 'discovery',   // 'discovery' (newest first-seen first) | 'name'
        search: '',
        loading: false,
        get currentPage() { return Math.floor(this.offset / this.limit) + 1; },
        get totalPages() { return Math.ceil(this.total / this.limit) || 1; },
        get pageNumbers() { return paginationWindow(this.currentPage, this.totalPages); },

        // Route-driven entry point (see messagesPage.syncRoute for why the work
        // is deferred to a microtask).
        syncRoute(params) {
            queueMicrotask(() => {
                this.offset = Math.max(0, parseInt(params.offset, 10) || 0);
                this.sort = params.sort === 'name' ? 'name' : 'discovery';
                this.search = params.search || '';
                this.fetch();
            });
        },
        // _go pushes a new history entry with the given offset, preserving the
        // active sort/search. The route change drives the actual fetch.
        _go(offset) {
            const p = {};
            if (offset) p.offset = offset;
            if (this.sort !== 'discovery') p.sort = this.sort;
            if (this.search) p.search = this.search;
            Alpine.store('app').setRoute(p);
        },
        async fetch() {
            this.loading = true;
            try {
                const q = '/api/profiles?limit=' + this.limit + '&offset=' + this.offset +
                    '&sort=' + this.sort + '&search=' + encodeURIComponent(this.search);
                const d = await apiJSON(q);
                this.profiles = (d && d.profiles) || [];
                this.total = (d && d.total) || 0;
            } catch (e) { console.error(e); }
            this.loading = false;
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
        setSort(s) {
            if (this.sort === s) return;
            this.sort = s;
            this._go(0);
        },
        doSearch() {
            this._go(0);
        },

        reputationClass(rep) {
            if (rep === 'good') return 'badge-green';
            if (rep === 'bad') return 'badge-red';
            return 'badge-yellow';
        },

        // profileLabel formats a user's identity the same way the bot's
        // moderation keyboard does: "@username (Display Name)" when both are
        // present, otherwise whichever single value is available, falling back
        // to "#user_id".
        profileLabel(p) {
            const uname = (p.username || '').replace(/^@+/, '').trim();
            const display = (p.display_name || '').trim();
            if (uname && display) return '@' + uname + ' (' + display + ')';
            if (uname) return '@' + uname;
            if (display) return display;
            return '#' + p.user_id;
        },

        async deleteProfile(p) {
            if (!confirm(Alpine.store('i18n').t('prof_delete_confirm'))) return;
            try {
                await api('/api/profiles/delete', {
                    method: 'DELETE',
                    body: JSON.stringify({ user_id: p.user_id })
                });
                // Refetch the current page so total/pagination stay correct;
                // step back a page if we just emptied the last one.
                await this.fetch();
                if (this.profiles.length === 0 && this.offset > 0) {
                    this.changePage(-1);
                }
            } catch (e) { console.error(e); }
        }
    };
}

