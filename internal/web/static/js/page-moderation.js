// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires

/* Moderation */
function moderationPage() {
    return {
        actions: [],
        muted: [],
        showMsgPopup: null,
        loading: false,
        async load() {
            this.loading = true;
            try {
                const [a, m] = await Promise.all([apiJSON('/api/actions'), apiJSON('/api/muted')]);
                this.actions = a || [];
                this.muted = m || [];
            } catch (e) { console.error(e); }
            this.loading = false;
        },
        async unmuteUser(m) {
            const ok = await modAction({ user_id: m.user_id, chat_id: m.chat_id }, 'unmute');
            if (ok) await this.load();
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

