// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires

/* Chat Rules Overrides Editor - per-chat appendix to ai.chat_rules.
 * Has its own GET/PUT endpoint, mirroring the moderationRulesEditor shape.
 */
function chatRulesOverridesEditor() {
    return {
        overrides: [],
        saving: false,
        loading: false,
        msg: '',
        msgClass: 'save-msg',

        get chats() {
            return Alpine.store('app').chats || [];
        },

        chatLabel(id) {
            return Alpine.store('app').chatLabel(id);
        },

        async load() {
            this.loading = true;
            try {
                const data = await apiJSON('/api/config/chat-rules-overrides');
                const list = (data && data.overrides) ? data.overrides : [];
                this.overrides = list.map(o => ({
                    chat: Number(o.chat) || 0,
                    rules: o.rules || '',
                }));
            } catch (e) {
                console.error('Failed to load chat rules overrides', e);
            }
            this.loading = false;
        },

        add() {
            const defaultChat = this.chats.length === 1 ? Number(this.chats[0].id) : 0;
            this.overrides.push({ chat: defaultChat, rules: '' });
        },

        async save() {
            this.saving = true;
            try {
                const payload = {
                    overrides: this.overrides.map(o => ({
                        chat: Number(o.chat) || 0,
                        rules: o.rules || '',
                    })),
                };
                const r = await api('/api/config/chat-rules-overrides', { method: 'PUT', body: JSON.stringify(payload) });
                if (r.ok) {
                    flashMsg(this, Alpine.store('i18n').t('cfg_saved'), true);
                } else {
                    const d = await r.json();
                    flashMsg(this, Alpine.store('i18n').err(d) || Alpine.store('i18n').t('cfg_error'), false);
                }
            } catch {
                flashMsg(this, Alpine.store('i18n').t('cfg_error'), false);
            }
            this.saving = false;
        }
    };
}

/* RSS Feeds Editor */
function rssFeedsEditor() {
    return {
        feeds: [],
        saving: false,
        loading: false,
        msg: '',
        msgClass: 'save-msg',

        get chats() {
            return Alpine.store('app').chats || [];
        },

        chatLabel(id) {
            return Alpine.store('app').chatLabel(id);
        },

        defaultChatID() {
            return this.chats.length === 1 ? Number(this.chats[0].id) : 0;
        },

        ensurePostTo(feed) {
            if (!Array.isArray(feed.post_to)) feed.post_to = [];
            return feed.post_to;
        },

        addPostTo(feed) {
            // RSS publishing is a destination - default to the main area, not "any".
            this.ensurePostTo(feed).push({ chat: this.defaultChatID(), topic: 0 });
        },

        removePostTo(feed, i) {
            this.ensurePostTo(feed).splice(i, 1);
        },

        topicMode(item) {
            if (!item) return 'main';
            const t = Number(item.topic);
            if (t === -1) return 'main';
            if (t === 0) return 'main';
            return 'specific';
        },

        setTopicMode(item, mode) {
            if (mode === 'any') { item.topic = -1; return; }
            if (mode === 'main') { item.topic = 0; return; }
            if (mode === 'specific') {
                if (!Number.isInteger(Number(item.topic)) || Number(item.topic) <= 0) item.topic = 1;
            }
        },

        async load() {
            this.loading = true;
            try {
                const feeds = await apiJSON('/api/config/rss') || [];
                this.feeds = feeds.map(f => ({
                    ...f,
                    post_to: Array.isArray(f.post_to) ? f.post_to.map(t => ({
                        chat: Number(t.chat) || 0,
                        topic: Number(t.topic),
                    })) : [],
                }));
            } catch {}
            this.loading = false;
        },

        async save() {
            this.saving = true;
            try {
                const payload = this.feeds.map(f => ({
                    ...f,
                    post_to: (f.post_to || []).map(t => ({
                        chat: Number(t.chat) || 0,
                        topic: Number(t.topic),
                    })),
                }));
                const r = await api('/api/config/rss', { method: 'PUT', body: JSON.stringify(payload) });
                if (r.ok) {
                    flashMsg(this, Alpine.store('i18n').t('cfg_saved'), true);
                } else {
                    const d = await r.json();
                    flashMsg(this, Alpine.store('i18n').err(d) || Alpine.store('i18n').t('cfg_error'), false);
                }
            } catch {
                flashMsg(this, Alpine.store('i18n').t('cfg_error'), false);
            }
            this.saving = false;
        }
    };
}

/* Topic Names Editor
 * Edits the static forum-topic name registry (config `topics`). Each row maps
 * a (chat, forum thread id) pair to a human-readable name, shown in moderation
 * reports and the web message view. Live-observed names take precedence; these
 * entries fill the gaps (e.g. topics created before the bot joined). Changes
 * take effect after a restart. */
function topicsEditor() {
    return {
        topics: [],
        saving: false,
        loading: false,
        msg: '',
        msgClass: 'save-msg',

        get chats() {
            return Alpine.store('app').chats || [];
        },

        defaultChatID() {
            return this.chats.length === 1 ? Number(this.chats[0].id) : 0;
        },

        async load() {
            this.loading = true;
            try {
                const list = await apiJSON('/api/config/topics') || [];
                this.topics = list.map(t => ({
                    chat: Number(t.chat) || 0,
                    topic: Number(t.topic) || 0,
                    name: t.name || '',
                }));
            } catch {}
            this.loading = false;
        },

        add() {
            this.topics.push({ chat: this.defaultChatID(), topic: 0, name: '' });
        },

        async save() {
            this.saving = true;
            try {
                const payload = this.topics.map(t => ({
                    chat: Number(t.chat) || 0,
                    topic: Number(t.topic) || 0,
                    name: (t.name || '').trim(),
                }));
                const r = await api('/api/config/topics', { method: 'PUT', body: JSON.stringify(payload) });
                if (r.ok) {
                    flashMsg(this, Alpine.store('i18n').t('cfg_saved'), true);
                } else {
                    const d = await r.json();
                    flashMsg(this, Alpine.store('i18n').err(d) || Alpine.store('i18n').t('cfg_error'), false);
                }
            } catch {
                flashMsg(this, Alpine.store('i18n').t('cfg_error'), false);
            }
            this.saving = false;
        }
    };
}

/* Moderation Rules Editor
 * Edits the LLM-output → action rule list under ai.content_moderation.rules.
 * Each rule pairs a substring (case-insensitive match against the LLM's
 * response) with one of: report, warn, delete, ban. Rules are evaluated in
 * the order shown and the first match wins. */
function moderationRulesEditor() {
    return {
        rules: [],
        saving: false,
        loading: false,
        msg: '',
        msgClass: 'save-msg',

        async load() {
            this.loading = true;
            try {
                const data = await apiJSON('/api/config/moderation-rules');
                const list = (data && data.rules) ? data.rules : [];
                this.rules = list.map(r => ({
                    trigger: r.trigger || '',
                    action: r.action || 'report',
                    description: r.description || '',
                    notify_admin: r.notify_admin !== false,
                }));
            } catch (e) {
                console.error('Failed to load moderation rules', e);
            }
            this.loading = false;
        },

        move(index, dir) {
            const target = index + dir;
            if (target < 0 || target >= this.rules.length) return;
            const tmp = this.rules[index];
            this.rules.splice(index, 1);
            this.rules.splice(target, 0, tmp);
        },

        async save() {
            this.saving = true;
            try {
                const payload = {
                    rules: this.rules.map(r => ({
                        trigger: r.trigger,
                        action: r.action,
                        description: r.description || '',
                        notify_admin: r.notify_admin !== false,
                    })),
                };
                const r = await api('/api/config/moderation-rules', { method: 'PUT', body: JSON.stringify(payload) });
                if (r.ok) {
                    flashMsg(this, Alpine.store('i18n').t('cfg_saved'), true);
                } else {
                    const d = await r.json();
                    flashMsg(this, Alpine.store('i18n').err(d) || Alpine.store('i18n').t('cfg_error'), false);
                }
            } catch {
                flashMsg(this, Alpine.store('i18n').t('cfg_error'), false);
            }
            this.saving = false;
        }
    };
}

/* Model Editor */
function modelEditor(which) {
    return {
        which,
        models: [],
        saving: false,
        loading: false,
        msg: '',
        msgClass: 'save-msg',

        async load() {
            this.loading = true;
            try {
                const data = await apiJSON('/api/config/models');
                const arr = data[which + '_model'] || [];
                this.models = arr.length ? arr.map(m => ({ ...m, _show: false }))
                    : [{ provider: '', endpoint: '', api_key: '', deployment_name: '', temperature: null, omit_max_tokens: false, _show: false }];
            } catch (e) { console.error('Failed to load models', e); }
            this.loading = false;
        },

        moveModel(index, dir) {
            const target = index + dir;
            if (target < 0 || target >= this.models.length) return;
            const tmp = this.models[index];
            this.models.splice(index, 1);
            this.models.splice(target, 0, tmp);
        },

        async save() {
            this.saving = true;
            // Collect both sides: re-fetch the other side so we don't lose it
            let payload;
            try {
                const current = await apiJSON('/api/config/models');
                const clean = this.models.map(({ _show, ...rest }) => rest);
                current[this.which + '_model'] = clean;
                payload = current;
            } catch {
                payload = { [this.which + '_model']: this.models.map(({ _show, ...rest }) => rest) };
            }

            try {
                const r = await api('/api/config/models', { method: 'PUT', body: JSON.stringify(payload) });
                const d = await r.json();
                if (r.ok) {
                    flashMsg(this, Alpine.store('i18n').t('cfg_saved'), true);
                } else {
                    flashMsg(this, Alpine.store('i18n').err(d) || Alpine.store('i18n').t('cfg_error'), false);
                }
            } catch {
                flashMsg(this, Alpine.store('i18n').t('cfg_error'), false);
            }
            this.saving = false;
        }
    };
}

