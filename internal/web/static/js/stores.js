// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires

/* ── Alpine Stores ── */
document.addEventListener('alpine:init', () => {

    /* ── i18n Store ── */
    Alpine.store('i18n', {
        data: { en: {}, ru: {} },
        lang: localStorage.getItem('lang') || 'en',

        t(k, ...args) {
            let s = (this.data[this.lang] && this.data[this.lang][k]) || this.data.en[k] || k;
            args.forEach((v, i) => { s = s.replace('{' + i + '}', v); });
            return s;
        },

        // err renders a backend error response. Prefers the structured
        // error_code (translated via the bundled i18n catalog) as the headline
        // and appends the server-provided `detail` (e.g. which config field
        // failed validation) on its own line so the user sees exactly what
        // went wrong. Falls back to the English `error` field for unknown
        // codes / older payloads.
        err(d) {
            if (!d) return '';
            let head = '';
            const code = d.error_code;
            if (code) {
                const lang = this.data[this.lang];
                if (lang && lang[code]) head = lang[code];
                else if (this.data.en && this.data.en[code]) head = this.data.en[code];
            }
            const detail = (d.detail || '').trim();
            if (head && detail && detail !== head) return head + ':\n' + detail;
            return head || detail || d.error || '';
        },

        setLang(l) {
            this.lang = l;
            localStorage.setItem('lang', l);
        },

        async load() {
            try {
                const r = await fetch(BASE + '/api/i18n');
                if (r.ok) this.data = await r.json();
            } catch (e) { console.error('Failed to load i18n', e); }
        }
    });

    /* ── Auth Store ── */
    Alpine.store('auth', {
        loggedIn: false,
        step: 'loading',  // 'loading', 'password', 'otp', 'request_otp'
        passwordRequired: true,
        otpAvailable: false,
        password: '',
        otpCode: '',
        loginError: '',

        clearSession() {
            this.loggedIn = false;
            this.step = this.passwordRequired ? 'password' : (this.otpAvailable ? 'request_otp' : 'password');
            this.password = '';
            this.otpCode = '';
            localStorage.removeItem('token');
        },

        async fetchMode() {
            try {
                const r = await fetch(BASE + '/api/auth/mode', { credentials: 'same-origin' });
                if (r.ok) {
                    const d = await r.json();
                    this.passwordRequired = d.password_required;
                    this.otpAvailable = d.otp_available;
                }
            } catch {}
            if (!this.loggedIn) {
                if (this.passwordRequired) {
                    this.step = 'password';
                } else if (this.otpAvailable) {
                    this.step = 'request_otp';
                } else {
                    this.step = 'no_auth';
                }
            }
        },

        async submitPassword() {
            const pw = this.password.trim();
            if (!pw) return;
            try {
                const r = await fetch(BASE + '/api/auth/login', {
                    method: 'POST',
                    credentials: 'same-origin',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ password: pw })
                });
                const d = await r.json();
                if (r.ok && (d.authenticated || d.token)) {
                    // Password-only login succeeded
                    localStorage.removeItem('token');
                    this.loginError = '';
                    this.loggedIn = true;
                    Alpine.store('app').init();
                } else if (r.ok && d.otp_required) {
                    // Password OK, now ask for OTP
                    this.loginError = '';
                    this.step = 'otp';
                } else {
                    this.loginError = Alpine.store('i18n').err(d) || 'Login failed';
                }
            } catch {
                this.loginError = 'Network error';
            }
        },

        async submitOTP() {
            const code = this.otpCode.trim();
            if (!code) return;
            try {
                const r = await fetch(BASE + '/api/auth/login', {
                    method: 'POST',
                    credentials: 'same-origin',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ code })
                });
                const d = await r.json();
                if (r.ok && (d.authenticated || d.token)) {
                    localStorage.removeItem('token');
                    this.loginError = '';
                    this.loggedIn = true;
                    Alpine.store('app').init();
                } else {
                    this.loginError = Alpine.store('i18n').err(d) || 'Invalid code';
                }
            } catch {
                this.loginError = 'Network error';
            }
        },

        backToPassword() {
            this.step = this.passwordRequired ? 'password' : 'request_otp';
            this.otpCode = '';
            this.loginError = '';
        },

        async requestOTP() {
            this.loginError = '';
            try {
                const r = await fetch(BASE + '/api/auth/login', {
                    method: 'POST',
                    credentials: 'same-origin',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ password: '_' })
                });
                const d = await r.json();
                if (r.ok && d.otp_required) {
                    this.step = 'otp';
                } else {
                    this.loginError = Alpine.store('i18n').err(d) || 'Failed to send code';
                }
            } catch {
                this.loginError = 'Network error';
            }
        },

        async logout() {
            api('/api/auth/logout', { method: 'POST' }).catch(() => {});
            this.clearSession();
        },

        async check() {
            try {
                const r = await api('/api/auth/check');
                if (r.ok) {
                    this.loggedIn = true;
                    Alpine.store('app').init();
                }
            } catch {}
        }
    });

    /* ── App Store ── */
    Alpine.store('app', {
        page: 'moderation',
        sidebarOpen: false,
        version: {},
        configMeta: {},
        configData: {},
        envOverrides: new Set(),
        chats: [],
        apiError: '',

        // Current page's query params, parsed from the location.hash query
        // string (e.g. "#messages?offset=50&user=foo"). Replaced wholesale on
        // every route change so paginated pages can react to it. Doubles as the
        // cross-page hand-off channel (e.g. "view messages from user" navigates
        // to "#messages?user=…").
        routeParams: {},

        navItems: [
            { page: 'moderation',  icon: '🛡️', i18nKey: 'nav_moderation' },
            { page: 'messages',    icon: '💬', i18nKey: 'nav_messages' },
            { page: 'profiles',    icon: '👤', i18nKey: 'nav_profiles' },
            { page: 'diagnostics', icon: '📡', i18nKey: 'nav_diagnostics' },
            { page: 'config',      icon: '⚙️', i18nKey: 'nav_config' },
            { page: 'logs',        icon: '📜', i18nKey: 'nav_logs' },
            { page: 'system',      icon: '🗄️', i18nKey: 'nav_system' },
        ],

        get versionLabel() {
            const v = this.version;
            const short = v.git_commit ? v.git_commit.substring(0, 7) : '';
            return (v.version || 'dev') + (short ? ' (' + short + ')' : '');
        },

        // Valid page ids, derived from navItems. Used by the hash router to
        // reject unknown / out-of-range location.hash fragments.
        get pageIds() {
            return this.navItems.map(n => n.page);
        },

        // ── Hash-based router ──
        // The active page and its view state (pagination, filters) are mirrored
        // in location.hash (e.g. "#messages?offset=50&user=foo") so the browser
        // Back / Forward buttons step through page and pagination changes
        // instead of leaving the app. navigate() / setRoute() update the hash; a
        // one-time hashchange listener (installed by setupRouter() on first
        // init) parses it into `page` + `routeParams` and applies it. Hash
        // routing needs no server changes and is unaffected by the configurable
        // path prefix.
        _routerReady: false,

        setupRouter() {
            if (this._routerReady) return;
            this._routerReady = true;
            window.addEventListener('hashchange', () => this._applyHash());
            this._applyHash();
        },

        // _parseHash splits location.hash into a validated page id and a plain
        // params object (unknown pages fall back to 'moderation').
        _parseHash() {
            const raw = (location.hash || '').replace(/^#/, '');
            const qi = raw.indexOf('?');
            const page = qi >= 0 ? raw.slice(0, qi) : raw;
            const params = {};
            if (qi >= 0) {
                new URLSearchParams(raw.slice(qi + 1)).forEach((v, k) => { params[k] = v; });
            }
            return { page: this.pageIds.includes(page) ? page : 'moderation', params };
        },

        // _buildHash serialises a page + params into a "#page?query" string,
        // dropping empty values to keep the URL clean.
        _buildHash(page, params) {
            const sp = new URLSearchParams();
            Object.keys(params || {}).forEach(k => {
                const v = params[k];
                if (v !== '' && v != null) sp.set(k, v);
            });
            const q = sp.toString();
            return '#' + page + (q ? '?' + q : '');
        },

        _applyHash() {
            const { page, params } = this._parseHash();
            this.routeParams = params;
            this._applyPage(page);
        },

        // _applyPage performs the actual page switch and side effects. It is
        // the single place that mutates `page`, called only from the router.
        _applyPage(page) {
            this.page = page;
            this.sidebarOpen = false;
            this.apiError = '';
            if (page === 'config') this.loadConfigMeta();
        },

        // navigate switches to another page (optionally seeding its view state,
        // e.g. a cross-page filter hand-off). setRoute updates only the query
        // params of the current page. Both record a history entry by writing
        // location.hash, which triggers the hashchange listener.
        navigate(page, params) {
            if (!this.pageIds.includes(page)) return;
            const target = this._buildHash(page, params);
            if (location.hash === target) {
                // Hash already matches: no hashchange event will fire, apply now.
                this.routeParams = params || {};
                this._applyPage(page);
            } else {
                location.hash = target;
            }
        },

        setRoute(params) {
            this.navigate(this.page, params);
        },

        // Jump to the Messages page pre-filtered to one author. Used by the
        // "view messages" link on profile cards.
        showUserMessages(userId) {
            this.navigate('messages', { user: String(userId) });
        },

        // Jump to the User Profiles page searching for one user (by id, which
        // the profile search also matches). Used by the "view profile" link on
        // message cards.
        showUserProfile(userId) {
            this.navigate('profiles', { search: String(userId) });
        },

        async init() {
            this.setupRouter();
            await Promise.all([this.loadVersion(), this.loadConfigMeta()]);
        },

        async loadVersion() {
            try { this.version = await apiJSON('/api/version'); } catch {}
        },

        async loadConfigMeta() {
            try {
                const [meta, cfg] = await Promise.all([
                    apiJSON('/api/config/meta'),
                    apiJSON('/api/config')
                ]);
                this.configMeta = meta;
                this.configData = cfg;
                this.envOverrides = new Set(meta.env_overrides || []);
                this.loadChats();
            } catch (e) { console.error('Failed to load config meta', e); }
        },

        async loadChats() {
            try {
                const list = await apiJSON('/api/chats');
                this.chats = Array.isArray(list) ? list : [];
            } catch { this.chats = []; }
        },

        chatLabel(id) {
            const n = Number(id);
            if (!n) return '';
            const c = (this.chats || []).find(x => Number(x.id) === n);
            if (c && c.title) return c.title + ' (' + n + ')';
            return String(n);
        }
    });

    /* Global init */
    Alpine.store('i18n').load().then(() => {
        Alpine.store('auth').fetchMode();
        Alpine.store('auth').check();
    });
});
