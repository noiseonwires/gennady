// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires

/* Config Page */
function configPage() {
    return {
        openSections: {},
        _ready: false,
        loading: false,

        get visibleSections() {
            const meta = Alpine.store('app').configMeta;
            if (!meta.sections) return [];
            const fieldsBySection = {};
            (meta.fields || []).forEach(f => {
                if (!fieldsBySection[f.section]) fieldsBySection[f.section] = [];
                fieldsBySection[f.section].push(f);
            });
            const modelSections = ['ai_light_model', 'ai_full_model'];
            return meta.sections.filter(sec => {
                const fields = fieldsBySection[sec.id] || [];
                return fields.length > 0 || sec.id === 'ai_rss' || modelSections.includes(sec.id);
            });
        },

        secLabel(sec) {
            return Alpine.store('i18n').lang === 'ru' ? sec.label_ru : sec.label_en;
        },

        async refresh() {
            this.loading = true;
            try {
                await Alpine.store('app').loadConfigMeta();
            } finally {
                this.loading = false;
            }
            this._ready = true;
        }
    };
}

/* Config Section (regular fields) */
function configSection(sectionId) {
    return {
        saving: false,
        msg: '',
        msgClass: 'save-msg',
        revealed: {},

        get fields() {
            const meta = Alpine.store('app').configMeta;
            return (meta.fields || []).filter(f => f.section === sectionId);
        },

        get values() {
            // Build reactive proxy over configData
            return Alpine.store('app').configData;
        },

        fieldLabel(f) {
            return Alpine.store('i18n').lang === 'ru' ? f.label_ru : f.label_en;
        },

        fieldDesc(f) {
            return Alpine.store('i18n').lang === 'ru' ? (f.desc_ru || f.desc_en) : f.desc_en;
        },

        isTextarea(f) {
            return f.type === 'string' && !f.sensitive &&
                (f.key.includes('prompt.') || f.key.includes('chat_rules') ||
                 f.key.includes('warning_mute') || f.key.includes('new_user_rules'));
        },

        insertPh(ev, key, name) {
            const ta = ev.target.closest('.field').querySelector('textarea');
            if (!ta) return;
            const s = ta.selectionStart, e = ta.selectionEnd;
            const ph = '{{' + name + '}}';
            const v = this.values[key] || '';
            this.values[key] = v.slice(0, s) + ph + v.slice(e);
            this.$nextTick(() => {
                ta.selectionStart = ta.selectionEnd = s + ph.length;
                ta.focus();
            });
        },

        async save() {
            this.saving = true;
            const meta = Alpine.store('app').configMeta;
            const fieldTypes = {};
            (meta.fields || []).forEach(f => { fieldTypes[f.key] = f.type; });

            const updates = {};
            this.fields.forEach(f => {
                const key = f.key;
                const ftype = fieldTypes[key] || 'string';
                const raw = this.values[key];

                if (ftype === 'bool') {
                    updates[key] = !!raw;
                } else if (ftype === 'int' || ftype === 'int64') {
                    updates[key] = raw === '' || raw === undefined ? 0 : parseInt(raw, 10);
                } else if (ftype === 'float64') {
                    updates[key] = raw === '' || raw === undefined ? 0 : parseFloat(raw);
                } else if (ftype === '[]chat_topic') {
                    // Owned by chatTopicListField subcomponent; pass through as-is.
                    // Topic uses Number() with explicit fallback so -1/0 are preserved.
                    updates[key] = Array.isArray(raw) ? raw.map(r => {
                        const t = Number(r.topic);
                        return {
                            chat: Number(r.chat) || 0,
                            topic: Number.isFinite(t) ? t : -1,
                        };
                    }) : [];
                } else if (ftype.startsWith('[]')) {
                    // Existing slice handling: comma-separated text input → typed array.
                    // Also accept arrays-in-place (already-coerced) for forward-compat.
                    if (Array.isArray(raw)) {
                        updates[key] = ftype === '[]string' ? raw.map(String) : raw.map(Number);
                    } else {
                        const val = String(raw || '');
                        const parts = val.split(/[,;]/).map(s => s.trim()).filter(s => s !== '');
                        updates[key] = ftype === '[]string' ? parts : parts.map(Number);
                    }
                } else {
                    updates[key] = raw != null ? String(raw) : '';
                }
            });

            try {
                const r = await api('/api/config', { method: 'PUT', body: JSON.stringify(updates) });
                const d = await r.json();
                if (r.ok) {
                    Object.assign(Alpine.store('app').configData, updates);
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

/* Chat Topic List field - reusable widget for `[]chat_topic` config fields
 * (excluded_topics / included_topics / post_to / rss feed post_to).
 *
 * Operates as a sub-editor of configSection: edits in place on
 * Alpine.store('app').configData[fieldKey] so the surrounding section's
 * Save button is the single save action.
 *
 * Topic uses fixed sentinels:
 *   -1 → any topic (incl. main)
 *    0 → main area only
 *  > 0 → specific forum thread ID
 */
function chatTopicListField(fieldKey) {
    return {
        fieldKey,

        // Destination fields (post_to) publish bot-authored messages, so a
        // wildcard "any topic" makes no sense there - only "main" or a specific
        // topic. Scope fields (included/excluded_topics) keep the wildcard.
        get isDestination() {
            return /(^|\.)post_to$/.test(this.fieldKey);
        },

        get items() {
            const v = Alpine.store('app').configData[fieldKey];
            return Array.isArray(v) ? v : [];
        },

        get chats() {
            return Alpine.store('app').chats || [];
        },

        chatLabel(id) {
            return Alpine.store('app').chatLabel(id);
        },

        ensureArray() {
            const v = Alpine.store('app').configData[this.fieldKey];
            if (!Array.isArray(v)) {
                Alpine.store('app').configData[this.fieldKey] = [];
            }
        },

        defaultChatID() {
            return this.chats.length === 1 ? Number(this.chats[0].id) : 0;
        },

        add() {
            this.ensureArray();
            // Scope fields default to "any topic"; destination (post_to) fields
            // default to the main area (0), since "any" is not a valid target.
            const defaultTopic = this.isDestination ? 0 : -1;
            Alpine.store('app').configData[this.fieldKey].push({ chat: this.defaultChatID(), topic: defaultTopic });
        },

        remove(i) {
            this.ensureArray();
            Alpine.store('app').configData[this.fieldKey].splice(i, 1);
        },

        topicMode(item) {
            if (!item) return this.isDestination ? 'main' : 'any';
            const t = Number(item.topic);
            if (t === -1) return this.isDestination ? 'main' : 'any';
            if (t === 0) return 'main';
            return 'specific';
        },

        setTopicMode(item, mode) {
            if (mode === 'any') { item.topic = -1; return; }
            if (mode === 'main') { item.topic = 0; return; }
            if (mode === 'specific') {
                if (!Number.isInteger(Number(item.topic)) || Number(item.topic) <= 0) item.topic = 1;
            }
        }
    };
}

