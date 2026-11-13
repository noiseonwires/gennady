// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires

const assert = require('node:assert/strict');
const { readFileSync } = require('node:fs');
const path = require('node:path');
const { test } = require('node:test');
const vm = require('node:vm');

const source = readFileSync(path.join(__dirname, 'static', 'js', 'page-config.js'), 'utf8');

function setup(value = '__redacted__', api = async () => {
    throw new Error('Unexpected API call');
}) {
    const app = {
        configData: { bot_token: value, language: 'en' },
        configMeta: { fields: [
            { key: 'bot_token', section: 'general', type: 'string', sensitive: true },
            { key: 'language', section: 'general', type: 'string' },
        ] },
    };
    const i18n = { t: key => key, err: data => data.error };
    const context = vm.createContext({
        Alpine: { store: name => ({ app, i18n })[name] },
        api,
        flashMsg: (ctx, text, ok) => { ctx.msg = text; ctx.ok = ok; },
    });
    vm.runInContext(source, context);
    return context.configSection('general');
}

test('eye fetches the actual secret once, then hides and reveals locally', async () => {
    const calls = [];
    const section = setup('__redacted__', async url => {
        calls.push(url);
        return { ok: true, json: async () => ({ value: 'real-secret' }) };
    });
    await section.toggleSecret('bot_token');
    assert.deepEqual(calls, ['/api/config/secret?key=bot_token']);
    assert.equal(section.values.bot_token, 'real-secret');
    assert.equal(section.revealed.bot_token, true);
    assert.equal(section.revealing.bot_token, false);
    await section.toggleSecret('bot_token');
    assert.equal(section.revealed.bot_token, false);
    await section.toggleSecret('bot_token');
    assert.equal(section.revealed.bot_token, true);
    assert.equal(calls.length, 1);
});

for (const value of ['replacement-token', '']) {
    test(`eye does not fetch or replace a locally edited value: ${JSON.stringify(value)}`, async () => {
        const section = setup(value);
        await section.toggleSecret('bot_token');
        assert.equal(section.values.bot_token, value);
        assert.equal(section.revealed.bot_token, true);
    });
}

test('repeated clicks do not duplicate a pending reveal or overwrite a new edit', async () => {
    let finish;
    let calls = 0;
    const section = setup('__redacted__', () => {
        calls++;
        return new Promise(resolve => { finish = resolve; });
    });
    const pending = section.toggleSecret('bot_token');
    assert.equal(section.revealing.bot_token, true);
    await section.toggleSecret('bot_token');
    assert.equal(calls, 1);
    section.values.bot_token = 'new-edit';
    finish({ ok: true, json: async () => ({ value: 'old-secret' }) });
    await pending;
    assert.equal(section.values.bot_token, 'new-edit');
    assert.equal(section.revealed.bot_token, true);
});

for (const response of [
    async () => ({ ok: false, json: async () => ({ error: 'Cannot reveal hashed password' }) }),
    async () => { throw new Error('Network failure'); },
    async () => ({ ok: true, json: async () => ({}) }),
]) {
    test('failed reveal stays masked, reports an error, and allows retry', async () => {
        const section = setup('__redacted__', response);
        await section.toggleSecret('bot_token');
        assert.equal(section.values.bot_token, '__redacted__');
        assert.notEqual(section.revealed.bot_token, true);
        assert.equal(section.revealing.bot_token, false);
        assert.equal(section.ok, false);
        assert.ok(section.msg);
    });
}

for (const value of ['__redacted__', 'replacement-token', '']) {
    test(`save preserves the exact secret value: ${JSON.stringify(value)}`, async () => {
        let updates;
        const section = setup(value, async (url, options) => {
            assert.equal(url, '/api/config');
            assert.equal(options.method, 'PUT');
            updates = JSON.parse(options.body);
            return { ok: true, json: async () => ({ status: 'ok' }) };
        });
        await section.save();
        assert.equal(updates.bot_token, value);
        assert.equal(updates.language, 'en');
        assert.equal(section.values.bot_token, value);
        assert.equal(section.ok, true);
    });
}
