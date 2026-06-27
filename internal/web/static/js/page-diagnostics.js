// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires

/* Diagnostics */
function diagnosticsPage() {
    return {
        services: {},
        models: [],
        promptWarnings: [],
        events: [],
        serverTimezone: '',
        telegram: {},
        bunnyEnv: {},
        chats: [],
        uptimeSeconds: null,
        tokenUsage: { day: '', rows: [], services: [], totals: {} },
        webhookInfo: null,
        webhookError: '',
        webhookLoading: false,
        loading: false,
        svcList: Object.entries(SVC_LABELS).map(([key, label]) => ({ key, label })),
        testingKey: null,

        // Debug-popup state (shared for moderation prompt and URL extraction).
        debugOpen: false,
        debugMode: '',          // 'moderation' | 'extract' | 'ocr'
        debugServiceKey: '',
        debugServiceLabel: '',
        debugInput: '',
        debugMessageId: '',     // optional: debug a stored message by its Telegram ID
        debugChatId: '',        // optional chat scope for the message ID lookup
        debugUseMessageId: false, // moderation mode: toggle between custom text and message ID
        debugImage: null,       // { dataURL, name, size } for OCR mode
        debugRunning: false,
        debugResult: null,      // { system_prompt, user_prompt, response, raw, error, response_time_ms, info }

        async load() {
            this.loading = true;
            try {
                const d = await apiJSON('/api/diagnostics');
                this.services = d.services || {};
                this.models = d.models || [];
                this.promptWarnings = d.prompt_warnings || [];
                this.events = d.scheduled_events || [];
                this.serverTimezone = d.server_timezone || '';
                this.telegram = d.telegram || {};
                this.bunnyEnv = d.bunny_env || {};
                this.chats = d.chats || [];
                this.uptimeSeconds = d.uptime_seconds != null ? d.uptime_seconds : null;
                if (this.telegram.mode === 'webhook') {
                    this.loadWebhookInfo();
                }
            } catch (e) { console.error(e); }
            try {
                this.tokenUsage = await apiJSON('/api/tokens');
            } catch (e) { console.error(e); }
            this.loading = false;
        },

        async loadWebhookInfo() {
            this.webhookLoading = true;
            this.webhookError = '';
            try {
                const d = await apiJSON('/api/diagnostics/webhook');
                if (d.ok && d.result) {
                    this.webhookInfo = d.result;
                } else {
                    this.webhookError = d.description || 'unexpected response';
                }
            } catch (e) {
                this.webhookError = e.message || String(e);
            }
            this.webhookLoading = false;
        },

        // webhookErrorActive reports whether the webhook's last error reported by
        // Telegram should still be highlighted as a problem. The error is treated
        // as stale (not active) when a good webhook event was received after it,
        // or when it occurred before the bot started.
        webhookErrorActive() {
            const info = this.webhookInfo;
            if (!info || !info.last_error_message || !info.last_error_date) return false;
            const errMs = info.last_error_date * 1000;
            const tg = this.telegram || {};
            // A successful webhook event received since the error clears it.
            if (tg.last_webhook_at && tg.last_webhook_at !== '0001-01-01T00:00:00Z') {
                const whMs = new Date(tg.last_webhook_at).getTime();
                if (!isNaN(whMs) && whMs >= errMs) return false;
            }
            // An error from before the bot started is no longer relevant.
            if (tg.connected_since && tg.connected_since !== '0001-01-01T00:00:00Z') {
                const startMs = new Date(tg.connected_since).getTime();
                if (!isNaN(startMs) && errMs < startMs) return false;
            }
            return true;
        },

        formatUptime(s) {
            if (s == null) return '';
            const days = Math.floor(s / 86400);
            const hours = Math.floor((s % 86400) / 3600);
            const mins = Math.floor((s % 3600) / 60);
            const secs = s % 60;
            const parts = [];
            if (days > 0) parts.push(days + 'd');
            if (hours > 0) parts.push(hours + 'h');
            if (mins > 0) parts.push(mins + 'm');
            parts.push(secs + 's');
            return parts.join(' ');
        },

        svcDot(key) {
            const r = this.services[key];
            if (!r) return 'unknown';
            return r.success ? 'ok' : 'err';
        },

        formatLastRun(lastFiredAt) {
            if (!lastFiredAt) return '-';
            const d = new Date(lastFiredAt);
            if (isNaN(d.getTime())) return '-';
            if (d.getFullYear() < 2000 || d.getTime() > Date.now()) {
                return '<em>' + Alpine.store('i18n').t('diag_event_first_run') + '</em>';
            }
            return lastFiredAt.replace('T', ' ').replace(/Z$/, '').replace(/\.[0-9]+/, '');
        },

        async testSvc(key) {
            this.testingKey = key;
            try {
                await api('/api/diagnostics/test/' + encodeURIComponent(key), { method: 'POST' });
                await this.load();
            } catch (e) {
                console.error(e);
            }
            this.testingKey = null;
        },

        // Opens the debug popup. mode = 'moderation' | 'extract' | 'ocr'.
        openDebug(mode, key, label) {
            this.debugMode = mode;
            this.debugServiceKey = key;
            this.debugServiceLabel = label || key;
            this.debugInput = '';
            this.debugMessageId = '';
            this.debugChatId = '';
            this.debugUseMessageId = false;
            this.debugImage = null;
            this.debugResult = null;
            this.debugRunning = false;
            this.debugOpen = true;
        },
        closeDebug() {
            this.debugOpen = false;
            this.debugResult = null;
            this.debugRunning = false;
            this.debugImage = null;
        },
        // Reads the selected image file into a base64 data URL for OCR debug.
        onDebugImage(event) {
            const file = event.target.files && event.target.files[0];
            if (!file) { this.debugImage = null; return; }
            const reader = new FileReader();
            reader.onload = () => {
                this.debugImage = { dataURL: reader.result, name: file.name, size: file.size };
            };
            reader.readAsDataURL(file);
        },
        async runDebug() {
            if (this.debugRunning) return;
            if (this.debugMode === 'ocr') {
                if (!this.debugImage || !this.debugImage.dataURL) return;
            } else if (this.debugMode === 'moderation' && this.debugUseMessageId) {
                if (!String(this.debugMessageId).trim()) return;
            } else if (!(this.debugInput || '').trim()) {
                return;
            }
            this.debugRunning = true;
            this.debugResult = null;
            try {
                if (this.debugMode === 'moderation' && this.debugUseMessageId) {
                    const payload = { message_id: parseInt(String(this.debugMessageId).trim(), 10) };
                    const chat = String(this.debugChatId).trim();
                    if (chat) payload.chat_id = parseInt(chat, 10);
                    this.debugResult = await apiJSON(
                        '/api/diagnostics/debug/moderation-by-id/' + encodeURIComponent(this.debugServiceKey),
                        { method: 'POST', body: JSON.stringify(payload) }
                    );
                } else if (this.debugMode === 'moderation') {
                    this.debugResult = await apiJSON(
                        '/api/diagnostics/debug/moderation/' + encodeURIComponent(this.debugServiceKey),
                        { method: 'POST', body: JSON.stringify({ message: this.debugInput.trim() }) }
                    );
                } else if (this.debugMode === 'ocr') {
                    this.debugResult = await apiJSON(
                        '/api/diagnostics/debug/ocr/' + encodeURIComponent(this.debugServiceKey),
                        { method: 'POST', body: JSON.stringify({ image: this.debugImage.dataURL }) }
                    );
                } else {
                    this.debugResult = await apiJSON(
                        '/api/diagnostics/debug/extract/' + encodeURIComponent(this.debugServiceKey),
                        { method: 'POST', body: JSON.stringify({ url: this.debugInput.trim() }) }
                    );
                }
            } catch (e) {
                this.debugResult = { error: e.message || String(e) };
            }
            this.debugRunning = false;
        },
        // Returns true if a Debug button should be offered for the given
        // non-AI service key (i.e. URL-extractor services).
        isExtractorService(key) {
            return key === 'extractor_api' || key === 'diffbot' || key === 'cloudflare';
        },
        // Returns true if an image-upload OCR debug button should be offered.
        isOCRService(key) {
            return key === 'azure_vision' || key === 'ocr_space';
        }
    };
}

