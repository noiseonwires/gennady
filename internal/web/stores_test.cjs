// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires

const assert = require('node:assert/strict');
const { readFileSync } = require('node:fs');
const path = require('node:path');
const { test } = require('node:test');
const vm = require('node:vm');

const source = readFileSync(path.join(__dirname, 'static', 'js', 'stores.js'), 'utf8');

function setup() {
    const stores = {};
    let init;
    const context = vm.createContext({
        Alpine: {
            store(name, value) {
                if (value) stores[name] = value;
                return stores[name];
            },
        },
        document: { addEventListener: (_, callback) => { init = callback; } },
        localStorage: { getItem: () => null },
        location: { hash: '#config' },
        window: { addEventListener: () => {} },
        BASE: '/admin1/modentry',
        fetch: async () => ({ ok: false }),
    });
    vm.runInContext(source, context);
    init();
    return { stores, context };
}

test('moderator mount gates navigation even with a super session or failed version request', async () => {
    const { stores, context } = setup();
    stores.auth.moderator = true;
    const calls = [];
    context.apiJSON = async url => {
        calls.push(url);
        if (url === '/api/version') return { role: 'super' };
        if (url === '/api/chats') return [];
        throw new Error('Unexpected API call: ' + url);
    };
    await stores.app.init();
    assert.equal(stores.app.isModerator, true);
    assert.equal(stores.app.page, 'moderation');
    assert.equal(Array.from(stores.app.pageIds).join(','), 'moderation,messages,profiles,diagnostics');
    assert.deepEqual(calls, ['/api/version', '/api/chats']);
    stores.app.navigate('config');
    assert.equal(context.location.hash, '#config');

    context.apiJSON = async url => {
        if (url === '/api/version') throw new Error('Version unavailable');
        if (url === '/api/chats') return [];
        throw new Error('Unexpected API call: ' + url);
    };
    await stores.app.init();
    assert.equal(stores.app.isModerator, true);
    assert.equal(stores.app.page, 'moderation');
});

test('admin mount retains full navigation and config loading', async () => {
    const { stores, context } = setup();
    context.location.hash = '';
    const calls = [];
    context.apiJSON = async url => {
        calls.push(url);
        if (url === '/api/version') return { role: 'super' };
        if (url === '/api/config/meta') return { fields: [] };
        if (url === '/api/config') return {};
        if (url === '/api/chats') return [];
        throw new Error('Unexpected API call: ' + url);
    };
    await stores.app.init();
    assert.equal(stores.app.isModerator, false);
    assert.equal(stores.app.pageIds.length, 7);
    assert.deepEqual(calls, ['/api/version', '/api/config/meta', '/api/config', '/api/chats']);
});

test('bootstrap waits for mount detection before checking a session', async () => {
    const { stores } = setup();
    let finish;
    const calls = [];
    stores.auth.fetchMode = () => {
        calls.push('mode');
        return new Promise(resolve => {
            finish = () => {
                stores.auth.moderator = true;
                resolve();
            };
        });
    };
    stores.auth.check = async () => {
        calls.push('check');
        assert.equal(stores.app.isModerator, true);
    };
    await new Promise(resolve => setImmediate(resolve));
    assert.deepEqual(calls, ['mode']);
    finish();
    await new Promise(resolve => setImmediate(resolve));
    assert.deepEqual(calls, ['mode', 'check']);
});
