// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires

/* Logs Page */
function logsPage() {
    return {
        logText: '',
        autoScroll: false,
        _interval: null,

        start() {
            if (this._interval) return;
            this.refresh();
            this._interval = setInterval(() => this.refresh(), 3000);
        },

        stop() {
            if (this._interval) { clearInterval(this._interval); this._interval = null; }
        },

        async refresh() {
            if (Alpine.store('app').page !== 'logs' || !Alpine.store('auth').loggedIn) { this.stop(); return; }
            try {
                const entries = await apiJSON('/api/logs');
                if (entries && entries.length) {
                    this.logText = entries.map(e => {
                        const ts = e.time ? new Date(e.time).toLocaleTimeString() : '';
                        return ts ? ts + ' ' + e.message : e.message;
                    }).join('\n');
                } else {
                    this.logText = '';
                }
                if (this.autoScroll && this.$refs.viewer) {
                    this.$nextTick(() => { this.$refs.viewer.scrollTop = this.$refs.viewer.scrollHeight; });
                }
            } catch (e) { console.error(e); }
        }
    };
}

/* System Page */
function systemPage() {
    return {
        stats: {},
        restarting: false,
        uploading: false,
        uploadingLabel: '',
        cloning: false,
        loading: false,

        async load() {
            this.loading = true;
            try { this.stats = await apiJSON('/api/stats'); } catch (e) { console.error(e); }
            this.loading = false;
        },

        async restart(mode) {
            const confirmKey = mode === 'hard' ? 'sys_hard_restart_confirm' : 'sys_soft_restart_confirm';
            if (!confirm(Alpine.store('i18n').t(confirmKey))) return;
            this.restarting = true;
            try {
                const r = await api('/api/restart', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ mode })
                });
                if (r.ok) {
                    const delay = mode === 'hard' ? 10000 : 5000;
                    alert(Alpine.store('i18n').t('sys_restart_ok'));
                    setTimeout(() => location.reload(), delay);
                } else {
                    const d = await r.json();
                    alert(Alpine.store('i18n').err(d) || 'Restart failed');
                    this.restarting = false;
                }
            } catch {
                alert(Alpine.store('i18n').t('sys_restart_ok'));
                setTimeout(() => location.reload(), 5000);
            }
        },

        async downloadFile(path, filename) {
            try {
                let dlPath = path;
                if (path.includes('/files/db')) {
                    const includeConfig = confirm(Alpine.store('i18n').t('sys_db_include_config'));
                    if (includeConfig) dlPath += '?include_config=1';
                }
                const r = await api(dlPath);
                const blob = await r.blob();
                const url = URL.createObjectURL(blob);
                const a = document.createElement('a');
                a.href = url;
                a.download = filename;
                a.click();
                URL.revokeObjectURL(url);
            } catch { alert(Alpine.store('i18n').t('sys_dl_fail')); }
        },

        uploadFile(path) {
            const inp = document.createElement('input');
            inp.type = 'file';
            inp.addEventListener('change', async () => {
                if (!inp.files.length) return;
                const file = inp.files[0];
                if (!confirm(Alpine.store('i18n').t('sys_upload_confirm', file.name))) return;
                let uploadPath = path;
                if (path.includes('/files/db')) {
                    const includeConfig = confirm(Alpine.store('i18n').t('sys_db_include_config'));
                    if (includeConfig) uploadPath += '?include_config=1';
                }
                this.uploading = true;
                this.uploadingLabel = file.name;
                try {
                    const data = await file.arrayBuffer();
                    const controller = new AbortController();
                    const timeoutId = setTimeout(() => controller.abort(), 300000); // 5 min timeout for large DB imports
                    const r = await fetch(BASE + uploadPath, {
                        method: 'POST',
                        credentials: 'same-origin',
                        headers: { 'Content-Type': 'application/octet-stream' },
                        body: data,
                        signal: controller.signal
                    });
                    clearTimeout(timeoutId);
                    const d = await r.json();
                    if (r.ok) {
                        alert(Alpine.store('i18n').t('sys_upload_ok'));
                        location.reload();
                    } else {
                        alert(Alpine.store('i18n').err(d) || Alpine.store('i18n').t('sys_upload_fail'));
                    }
                } catch (e) {
                    if (e.name === 'AbortError') alert(Alpine.store('i18n').t('sys_upload_timeout'));
                    else alert(Alpine.store('i18n').t('sys_upload_fail') + ' ' + (e.message || e));
                } finally {
                    this.uploading = false;
                    this.uploadingLabel = '';
                }
            });
            inp.click();
        },

        async copyConfigToDB() {
            if (!confirm(Alpine.store('i18n').t('sys_copy_to_db_confirm'))) return;
            try {
                const r = await api('/api/config/copy-to-db', { method: 'POST' });
                const d = await r.json();
                if (r.ok) alert(d.message || 'Copied successfully');
                else alert(Alpine.store('i18n').err(d) || 'Copy failed');
            } catch { alert('Copy to DB failed'); }
        },

        // cloneDB performs a server-side full clone of the database between the
        // live remote DB and a local SQLite file at the configured path.
        // direction: 'to-local' (remote → local file) or 'to-remote' (local file → remote).
        async cloneDB(direction) {
            const confirmKey = direction === 'to-local' ? 'sys_clone_to_local_confirm' : 'sys_clone_to_remote_confirm';
            if (!confirm(Alpine.store('i18n').t(confirmKey))) return;
            let path = direction === 'to-local' ? '/api/files/db/clone-to-local' : '/api/files/db/clone-to-remote';
            const includeConfig = confirm(Alpine.store('i18n').t('sys_db_include_config'));
            if (includeConfig) path += '?include_config=1';
            this.cloning = true;
            try {
                const r = await api(path, { method: 'POST' });
                const d = await r.json();
                if (r.ok) {
                    alert(d.message || Alpine.store('i18n').t('sys_clone_ok'));
                    this.load();
                } else {
                    alert(Alpine.store('i18n').err(d) || Alpine.store('i18n').t('sys_clone_fail'));
                }
            } catch (e) {
                alert(Alpine.store('i18n').t('sys_clone_fail') + ' ' + (e.message || e));
            } finally {
                this.cloning = false;
            }
        }
    };
}
