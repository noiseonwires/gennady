// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires

/* ── Constants ── */
const BASE = location.pathname.replace(/\/$/, '');

const SVC_LABELS = {
    azure_vision:   'Azure Vision',
    content_safety: 'Content Safety',
    ocr_space:      'OCR.space',
    weather:        'Weather (Open-Meteo)',
    holidays:       'Holidays (Calendarific)',
    wikipedia:      'Wikipedia',
    extractor_api:  'Extractor API',
    diffbot:        'Diffbot',
    cloudflare:     'Cloudflare Browser Rendering'
};

/* ── API helpers ── */
async function api(path, opts = {}) {
    const headers = { 'Content-Type': 'application/json', ...(opts.headers || {}) };
    let res;
    try {
        res = await fetch(BASE + path, { credentials: 'same-origin', ...opts, headers });
    } catch (e) {
        showApiError(e.message || 'Connection error');
        throw e;
    }
    if (res.status === 401) {
        const auth = Alpine.store('auth');
        if (auth.loggedIn) auth.clearSession();
        throw new Error('unauthorized');
    }
    if (res.status >= 500) {
        showApiError('Server error ' + res.status);
    }
    return res;
}

async function apiJSON(path, opts) {
    return (await api(path, opts)).json();
}

function showApiError(detail) {
    const app = Alpine.store('app');
    app.apiError = detail || 'Connection error';
}

/* ── Helpers ── */
function fmtTime(ts) {
    if (!ts) return '-';
    try { return new Date(ts).toLocaleString(); } catch { return String(ts); }
}

function fmtBytes(b) {
    if (b < 1024) return b + ' B';
    if (b < 1048576) return (b / 1024).toFixed(1) + ' KB';
    return (b / 1048576).toFixed(1) + ' MB';
}

function fmtBuildTime(bt) {
    try {
        const d = new Date(bt);
        if (!isNaN(d.getTime())) {
            const dd = String(d.getDate()).padStart(2, '0');
            const mm = String(d.getMonth() + 1).padStart(2, '0');
            const yyyy = d.getFullYear();
            const hh = String(d.getHours()).padStart(2, '0');
            const mi = String(d.getMinutes()).padStart(2, '0');
            return dd + '.' + mm + '.' + yyyy + ' ' + hh + ':' + mi;
        }
    } catch {}
    return bt;
}

// paginationWindow returns the 1-based page numbers to render in a pager,
// inserting the '…' sentinel where ranges are skipped. Always shows the first
// and last page plus a small window around the current page. current/total are
// 1-based; total is the page count.
function paginationWindow(current, total) {
    if (total <= 7) {
        return Array.from({ length: total }, (_, i) => i + 1);
    }
    const pages = [1];
    const start = Math.max(2, current - 1);
    const end = Math.min(total - 1, current + 1);
    if (start > 2) pages.push('…');
    for (let i = start; i <= end; i++) pages.push(i);
    if (end < total - 1) pages.push('…');
    pages.push(total);
    return pages;
}

function flashMsg(ctx, text, ok) {
    ctx.msg = text;
    ctx.msgClass = ok ? 'save-msg ok' : 'save-msg err';
    if (ctx._msgTimer) clearTimeout(ctx._msgTimer);
    // Errors linger longer so multi-line validation details stay readable.
    ctx._msgTimer = setTimeout(() => { ctx.msg = ''; }, ok ? 4000 : 12000);
}

const EVENT_LABELS = {
    morning_greeting: 'Morning Greeting',
    daily_summary: 'Daily Summary',
    message_cleanup: 'Message Cleanup',
    database_cleanup: 'Database Cleanup',
    user_profiles: 'User Profiles'
};

function eventDisplayName(name) {
    if (EVENT_LABELS[name]) return EVENT_LABELS[name];
    if (name && name.startsWith('rss_')) return 'RSS Feed (' + name.slice(4, 12) + ')';
    return name ? name.replace(/_/g, ' ').replace(/\b\w/g, c => c.toUpperCase()) : name;
}

/* ── Moderation helper ──
 * Performs a moderation action (warn / mute / cruel mute / unmute) by calling
 * the corresponding /api/moderation/* endpoint. The action type drives the
 * confirmation prompt; for mutes the duration is asked via prompt(), where
 * 0 (or empty) means "forever".
 *
 * target  – { user_id, chat_id, message_id? }
 * type    – 'warn' | 'mute' | 'cmute' | 'unmute'
 */
async function modAction(target, type) {
    if (!target || !target.user_id || !target.chat_id) {
        alert('user_id and chat_id are required');
        return false;
    }
    const i18n = Alpine.store('i18n');
    let duration = 0;
    let cruel = false;
    let path = '';
    let confirmMsg = '';
    switch (type) {
        case 'warn':
            if (!target.message_id) { alert(i18n.t('mod_warn_needs_msg')); return false; }
            path = '/api/moderation/warn';
            confirmMsg = i18n.t('mod_confirm_warn');
            break;
        case 'mute':
        case 'cmute': {
            cruel = (type === 'cmute');
            path = '/api/moderation/mute';
            const ans = prompt(i18n.t(cruel ? 'mod_cmute_prompt' : 'mod_mute_prompt'), '60');
            if (ans === null) return false;
            duration = parseInt(ans, 10);
            if (isNaN(duration) || duration < 0) duration = 0;
            confirmMsg = i18n.t(cruel ? 'mod_confirm_cmute' : 'mod_confirm_mute')
                .replace('{0}', duration === 0 ? '∞' : (duration + 'm'));
            break;
        }
        case 'unmute':
            path = '/api/moderation/unmute';
            confirmMsg = i18n.t('mod_confirm_unmute');
            break;
        case 'remoderate':
            if (!target.message_id) { alert(i18n.t('mod_warn_needs_msg')); return false; }
            path = '/api/moderation/remoderate';
            confirmMsg = i18n.t('mod_confirm_remoderate');
            break;
        default:
            return false;
    }
    if (!confirm(confirmMsg)) return false;
    try {
        const res = await api(path, {
            method: 'POST',
            body: JSON.stringify({
                user_id: Number(target.user_id),
                chat_id: Number(target.chat_id),
                message_id: target.message_id ? Number(target.message_id) : 0,
                duration: duration,
                cruel: cruel
            })
        });
        if (!res.ok) {
            let detail = '';
            try { const j = await res.json(); detail = i18n.err(j); } catch {}
            alert(i18n.t('mod_action_failed') + (detail ? ': ' + detail : ' (' + res.status + ')'));
            return false;
        }
        // After a successful mute / cruel mute, offer to purge the user's messages.
        if (type === 'mute' || type === 'cmute') {
            await promptDeleteUserMessages(target);
        }
        return true;
    } catch (e) {
        console.error('moderation action failed', e);
        alert(i18n.t('mod_action_failed') + ': ' + (e.message || ''));
        return false;
    }
}

/* ── Delete-user-messages helper ──
 * After a mute, asks the admin whether to delete the muted user's recent
 * messages and, if so, calls /api/moderation/delete-messages.
 *
 * target – { user_id, chat_id }
 */
async function promptDeleteUserMessages(target) {
    const i18n = Alpine.store('i18n');
    const ans = prompt(i18n.t('mod_delmsg_prompt'), 'nope');
    if (ans === null) return;
    const period = ans.trim().toLowerCase();
    if (!['1h', '1d', 'all'].includes(period)) return;
    try {
        const res = await api('/api/moderation/delete-messages', {
            method: 'POST',
            body: JSON.stringify({
                user_id: Number(target.user_id),
                chat_id: Number(target.chat_id),
                period: period
            })
        });
        if (!res.ok) {
            let detail = '';
            try { const j = await res.json(); detail = i18n.err(j); } catch {}
            alert(i18n.t('mod_action_failed') + (detail ? ': ' + detail : ' (' + res.status + ')'));
            return;
        }
        const j = await res.json();
        alert(i18n.t('mod_delmsg_done').replace('{0}', j.deleted));
    } catch (e) {
        console.error('delete user messages failed', e);
        alert(i18n.t('mod_action_failed') + ': ' + (e.message || ''));
    }
}

