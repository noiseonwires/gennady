#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# CI check: verify i18n keys, config labels, and static assets are consistent.
set -euo pipefail

ERRORS=0

# ─── Helper ───────────────────────────────────────────────────────────
fail() {
    echo "  MISSING: $1"
    ERRORS=$((ERRORS + 1))
}

# Extract JSON keys from a file (top-level flat object).
json_keys() {
    grep -oP '^\s*"([^"]+)"' "$1" | sed 's/.*"\(.*\)"/\1/'
}

# ─── 1. Web UI i18n: keys used in index.html / js/*.js must exist in both i18n_en.json and i18n_ru.json ───

echo "=== Check 1: Web UI i18n keys ==="

WEB_I18N_EN="internal/web/data/i18n_en.json"
WEB_I18N_RU="internal/web/data/i18n_ru.json"
INDEX_HTML="internal/web/static/index.html"
JS_DIR="internal/web/static/js"

en_web_keys=$(json_keys "$WEB_I18N_EN" | sort -u)
ru_web_keys=$(json_keys "$WEB_I18N_RU" | sort -u)

# Extract i18n keys used in HTML: $store.i18n.t('key')
html_keys=$(grep -oP "\\\$store\.i18n\.t\(\s*'([^']+)'" "$INDEX_HTML" | sed "s/.*'\\(.*\\)'.*/\\1/" | sort -u)

# Extract i18n keys used in the JS (nav i18nKey values), scanning all split files.
js_keys=""
if [ -d "$JS_DIR" ]; then
    js_keys=$(grep -rhoP "i18nKey:\s*'([^']+)'" "$JS_DIR" | sed "s/.*'\\(.*\\)'.*/\\1/" | sort -u)
fi

all_web_used=$(echo -e "${html_keys}\n${js_keys}" | sort -u | grep -v '^$')

for key in $all_web_used; do
    grep -qxF "$key" <<< "$en_web_keys" || fail "$key missing in $WEB_I18N_EN"
    grep -qxF "$key" <<< "$ru_web_keys" || fail "$key missing in $WEB_I18N_RU"
done

# Also check EN vs RU parity
for key in $en_web_keys; do
    grep -qxF "$key" <<< "$ru_web_keys" || fail "$key present in EN but missing in $WEB_I18N_RU"
done
for key in $ru_web_keys; do
    grep -qxF "$key" <<< "$en_web_keys" || fail "$key present in RU but missing in $WEB_I18N_EN"
done

echo "  Web UI keys checked: $(echo "$all_web_used" | wc -l) used, $(echo "$en_web_keys" | wc -l) in EN, $(echo "$ru_web_keys" | wc -l) in RU"

# ─── 2. Config labels: every section from config_labels_en must also be in config_labels_ru and vice versa ───

echo "=== Check 2: Config labels parity (EN ↔ RU) ==="

LABELS_EN="internal/web/data/config_labels_en.json"
LABELS_RU="internal/web/data/config_labels_ru.json"

en_label_keys=$(json_keys "$LABELS_EN" | sort -u)
ru_label_keys=$(json_keys "$LABELS_RU" | sort -u)

for key in $en_label_keys; do
    grep -qxF "$key" <<< "$ru_label_keys" || fail "$key present in EN labels but missing in $LABELS_RU"
done
for key in $ru_label_keys; do
    grep -qxF "$key" <<< "$en_label_keys" || fail "$key present in RU labels but missing in $LABELS_EN"
done

echo "  Config labels checked: $(echo "$en_label_keys" | wc -l) EN, $(echo "$ru_label_keys" | wc -l) RU"

# ─── 2b. Config labels coverage: every reflected config field must have a label ───

echo "=== Check 2b: Config labels vs reflected config fields ==="

# Get all reflected field keys and section IDs from the Go struct.
# Allow $GO to override the go binary path (useful in WSL where the system
# may not have Go installed but a Windows go.exe is reachable on /mnt/c).
# Alternatively, set $LABELS_META_FILE to a pre-rendered output file produced
# by `go run ./tools/check_labels all` from a host that has Go available -
# this avoids the cross-filesystem `go run` slowdown on /mnt/c.
if [ -n "${LABELS_META_FILE:-}" ] && [ -f "$LABELS_META_FILE" ]; then
    # Strip CR + UTF-8 BOM in case the file was produced on Windows.
    reflected_output=$(tr -d '\r' < "$LABELS_META_FILE" | sed '1s/^\xEF\xBB\xBF//')
else
    GO_BIN="${GO:-go}"
    reflected_output=$("$GO_BIN" run ./tools/check_labels all 2>/dev/null)
fi
reflected_fields=$(echo "$reflected_output" | grep '^field:' | sed 's/^field://' | sort -u)
reflected_sections=$(echo "$reflected_output" | grep '^section:' | sed 's/^section://' | sort -u)

# Check that every reflected field has a label in EN
for key in $reflected_fields; do
    grep -qxF "$key" <<< "$en_label_keys" || fail "$key from config struct has no label in $LABELS_EN"
done

# Check that every reflected section has a label in EN
for sec in $reflected_sections; do
    grep -qxF "section:$sec" <<< "$en_label_keys" || fail "section:$sec from config struct has no label in $LABELS_EN"
done

echo "  Reflected fields: $(echo "$reflected_fields" | wc -l), sections: $(echo "$reflected_sections" | wc -l)"

# ─── 3. Bot i18n: keys used in Go code must exist in both bot_en.json and bot_ru.json ───

echo "=== Check 3: Bot i18n keys ==="

BOT_I18N_EN="internal/i18n/data/bot_en.json"
BOT_I18N_RU="internal/i18n/data/bot_ru.json"

en_bot_keys=$(json_keys "$BOT_I18N_EN" | sort -u)
ru_bot_keys=$(json_keys "$BOT_I18N_RU" | sort -u)

# Extract all i18n.T("key") and i18n.Tf("key", ...) calls from Go source
go_i18n_keys=$(grep -rhoP 'i18n\.Tf?\("([^"]+)"' internal/bot/*.go | sed 's/.*"\(.*\)"/\1/' | sort -u)

for key in $go_i18n_keys; do
    grep -qxF "$key" <<< "$en_bot_keys" || fail "$key used in Go code but missing in $BOT_I18N_EN"
    grep -qxF "$key" <<< "$ru_bot_keys" || fail "$key used in Go code but missing in $BOT_I18N_RU"
done

# Also check EN vs RU parity
for key in $en_bot_keys; do
    grep -qxF "$key" <<< "$ru_bot_keys" || fail "$key present in bot EN but missing in $BOT_I18N_RU"
done
for key in $ru_bot_keys; do
    grep -qxF "$key" <<< "$en_bot_keys" || fail "$key present in bot RU but missing in $BOT_I18N_EN"
done

echo "  Bot i18n keys checked: $(echo "$go_i18n_keys" | wc -l) used in Go, $(echo "$en_bot_keys" | wc -l) in EN, $(echo "$ru_bot_keys" | wc -l) in RU"

# ─── 4. Static assets: local script/link references in index.html must point to existing files ───

echo "=== Check 4: Static asset references in index.html ==="

STATIC_DIR="internal/web/static"

# Extract local script src (skip http/https URLs)
local_scripts=$(grep -oP '<script[^>]+src="([^"]+)"' "$INDEX_HTML" | sed 's/.*src="\(.*\)"/\1/' | grep -v '^https\?://')

# Extract local CSS links
local_styles=$(grep -oP '<link[^>]+href="([^"]+)"' "$INDEX_HTML" | sed 's/.*href="\(.*\)"/\1/' | grep -v '^https\?://')

for asset in $local_scripts $local_styles; do
    if [ ! -f "$STATIC_DIR/$asset" ]; then
        fail "$asset referenced in index.html but not found in $STATIC_DIR/"
    fi
done

echo "  Local assets checked: $(echo $local_scripts $local_styles | wc -w) references"

# ─── 5. External scripts: CDN URLs referenced in index.html must be reachable ───

echo "=== Check 5: External script URLs ==="

external_scripts=$(grep -oP '<script[^>]+src="(https?://[^"]+)"' "$INDEX_HTML" | sed 's/.*src="\(.*\)"/\1/')

# Transient network blips (DNS, CDN edge, runner egress) occasionally yield
# HTTP 000. Retry with an incremental (doubling) backoff before declaring a URL
# unreachable. The pause grows 5s → 10s → 20s → … and is capped at 1 minute.
EXT_URL_ATTEMPTS=6
EXT_URL_PAUSE=5        # initial pause; doubles after each failed attempt
EXT_URL_PAUSE_MAX=60  # cap the incremental backoff at 1 minute

ext_count=0
for url in $external_scripts; do
    ext_count=$((ext_count + 1))
    status=000
    pause=$EXT_URL_PAUSE
    for attempt in $(seq 1 "$EXT_URL_ATTEMPTS"); do
        status=$(curl -o /dev/null -s -w '%{http_code}' -L --max-time 15 "$url" || true)
        if [ "$status" -ge 200 ] 2>/dev/null && [ "$status" -lt 400 ] 2>/dev/null; then
            break
        fi
        if [ "$attempt" -lt "$EXT_URL_ATTEMPTS" ]; then
            echo "  retry $attempt/$EXT_URL_ATTEMPTS for $url (HTTP $status), waiting ${pause}s…"
            sleep "$pause"
            pause=$((pause * 2))
            if [ "$pause" -gt "$EXT_URL_PAUSE_MAX" ]; then
                pause=$EXT_URL_PAUSE_MAX
            fi
        fi
    done
    if [ "$status" -ge 200 ] 2>/dev/null && [ "$status" -lt 400 ] 2>/dev/null; then
        echo "  OK ($status): $url"
    else
        fail "external script unreachable after $EXT_URL_ATTEMPTS attempts (HTTP $status): $url"
    fi
done

echo "  External URLs checked: $ext_count"

# ─── Summary ──────────────────────────────────────────────────────────

echo ""
if [ "$ERRORS" -gt 0 ]; then
    echo "FAILED: $ERRORS error(s) found."
    exit 1
else
    echo "ALL CHECKS PASSED."
fi
