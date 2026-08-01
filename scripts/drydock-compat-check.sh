#!/usr/bin/env bash
# drydock-compat-check.sh — Drydock compatibility smoke test for Portwing
#
# Usage:
#   ./scripts/drydock-compat-check.sh [HOST] [TOKEN]
#
# Arguments:
#   HOST   Base URL of the Portwing instance (default: http://localhost:3000)
#   TOKEN  Shared secret (X-Portwing-Token)
#          If omitted, no auth header is sent (works when Portwing runs without a token).
#
# Environment (used when the positional argument is omitted):
#   PORTWING_URL   Same as HOST
#   PORTWING_TOKEN Same as TOKEN (TOKEN env var also accepted)
#
# Requirements: curl, jq
#
# Exit codes:
#   0  All checks passed
#   1  One or more checks failed

set -euo pipefail

HOST="${1:-${PORTWING_URL:-http://localhost:3000}}"
TOKEN="${2:-${PORTWING_TOKEN:-${TOKEN:-}}}"

PASS=0
FAIL=0
FAILURES=()

# ── helpers ───────────────────────────────────────────────────────────────────

red() { printf '\033[31m%s\033[0m\n' "$*"; }
green() { printf '\033[32m%s\033[0m\n' "$*"; }
bold() { printf '\033[1m%s\033[0m\n' "$*"; }

# Note: ${arr[@]+...} expansions keep bash 3.2 (macOS default) happy under
# set -u when an array is empty.
curl_api() {
	local url="$1"
	shift
	local extra_args=("$@")
	local auth_args=()
	if [[ -n $TOKEN ]]; then
		auth_args=(-H "X-Portwing-Token: ${TOKEN}")
	fi
	curl -sf --max-time 10 \
		${auth_args[@]+"${auth_args[@]}"} \
		${extra_args[@]+"${extra_args[@]}"} \
		"${HOST}${url}"
}

assert() {
	local name="$1"
	local result="$2" # "pass" or "fail"
	local detail="${3:-}"

	if [[ $result == "pass" ]]; then
		green "  ✓ ${name}"
		PASS=$((PASS + 1))
	else
		red "  ✗ ${name}"
		if [[ -n $detail ]]; then
			printf '    %s\n' "$detail"
		fi
		FAIL=$((FAIL + 1))
		FAILURES+=("${name}")
	fi
}

assert_equal() {
	local actual="$1"
	local expected="$2"
	local name="$3"
	local detail="${4:-}"

	if [[ $actual == "$expected" ]]; then
		assert "$name" "pass"
	else
		assert "$name" "fail" "$detail"
	fi
}

assert_present() {
	local value="$1"
	local name="$2"
	local detail="${3:-}"

	if [[ -n $value ]]; then
		assert "$name" "pass"
	else
		assert "$name" "fail" "$detail"
	fi
}

# ── /health ───────────────────────────────────────────────────────────────────

bold "1. Health endpoint (unauthenticated)"

HEALTH=$(curl -sf --max-time 10 "${HOST}/health" 2>/dev/null) && HEALTH_OK=true || HEALTH_OK=false
if [[ $HEALTH_OK == "true" ]]; then
	STATUS=$(printf '%s' "$HEALTH" | jq -r '.status // empty' 2>/dev/null || true)
	assert "/health returns 200" "pass"
	if [[ -n $STATUS ]]; then
		assert "/health has status field" "pass"
	else
		assert "/health has status field" "fail" "got: $HEALTH"
	fi
else
	assert "/health returns 200" "fail" "curl failed — is Portwing running at ${HOST}?"
	assert "/health has status field" "fail" "skipped (health request failed)"
fi

# ── /api/containers ───────────────────────────────────────────────────────────

bold "2. GET /api/containers"

CONTAINERS=$(curl_api /api/containers 2>/dev/null) && CONTAINERS_OK=true || CONTAINERS_OK=false
if [[ $CONTAINERS_OK == "true" ]]; then
	assert "/api/containers returns 200" "pass"

	IS_ARRAY=$(printf '%s' "$CONTAINERS" | jq 'type == "array"' 2>/dev/null || true)
	if [[ $IS_ARRAY == "true" ]]; then
		assert "/api/containers body is JSON array" "pass"
	else
		assert "/api/containers body is JSON array" "fail" "got: $(printf '%s' "$CONTAINERS" | head -c 200)"
	fi

	# Validate first container shape if any exist
	COUNT=$(printf '%s' "$CONTAINERS" | jq 'length' 2>/dev/null || true)
	if [[ $COUNT -gt 0 ]]; then
		CONTAINER_ID=$(printf '%s' "$CONTAINERS" | jq -r '.[0].id // empty')
		CONTAINER_STATUS=$(printf '%s' "$CONTAINERS" | jq -r '.[0].status // empty')
		CONTAINER_WATCHER=$(printf '%s' "$CONTAINERS" | jq -r '.[0].watcher // empty')
		CONTAINER_IMAGE_ID=$(printf '%s' "$CONTAINERS" | jq -r '.[0].image.id // empty')
		CONTAINER_IMAGE_REG=$(printf '%s' "$CONTAINERS" | jq -r '.[0].image.registry // empty')

		assert_present "$CONTAINER_ID" "containers[0].id present"
		assert_present "$CONTAINER_STATUS" "containers[0].status present"
		assert_present "$CONTAINER_WATCHER" "containers[0].watcher present"
		assert_present "$CONTAINER_IMAGE_ID" "containers[0].image.id present"
		assert_present "$CONTAINER_IMAGE_REG" "containers[0].image.registry present"
	else
		printf '    (no containers running — shape assertions skipped)\n'
	fi
else
	assert "/api/containers returns 200" "fail" "curl failed"
	assert "/api/containers body is JSON array" "fail" "skipped"
fi

# ── /api/watchers ─────────────────────────────────────────────────────────────

bold "3. GET /api/watchers"

WATCHERS=$(curl_api /api/watchers 2>/dev/null) && WATCHERS_OK=true || WATCHERS_OK=false
if [[ $WATCHERS_OK == "true" ]]; then
	assert "/api/watchers returns 200" "pass"

	IS_ARRAY=$(printf '%s' "$WATCHERS" | jq 'type == "array"' 2>/dev/null || true)
	assert_equal "$IS_ARRAY" "true" "/api/watchers body is JSON array" "got: $(printf '%s' "$WATCHERS" | head -c 200)"

	W_TYPE=$(printf '%s' "$WATCHERS" | jq -r '.[0].type // empty' 2>/dev/null || true)
	W_NAME=$(printf '%s' "$WATCHERS" | jq -r '.[0].name // empty' 2>/dev/null || true)
	assert_present "$W_TYPE" "watchers[0].type present"
	assert_present "$W_NAME" "watchers[0].name present"
	W_TRANSPORT=$(printf '%s' "$WATCHERS" | jq -r '.[0].configuration.transport // empty' 2>/dev/null || true)
	W_EXECUTION=$(printf '%s' "$WATCHERS" | jq -r '.[0].configuration.execution // empty' 2>/dev/null || true)
	W_EVENTS=$(printf '%s' "$WATCHERS" | jq -r '.[0].configuration.events // empty' 2>/dev/null || true)
	assert_equal "$W_TRANSPORT" "docker-api" "watcher transport == docker-api" "got: $W_TRANSPORT"
	assert_equal "$W_EXECUTION" "controller" "watcher execution == controller" "got: $W_EXECUTION"
	assert_equal "$W_EVENTS" "portwing" "watcher events == portwing" "got: $W_EVENTS"
else
	assert "/api/watchers returns 200" "fail" "curl failed"
	assert "/api/watchers body is JSON array" "fail" "skipped"
fi

# ── GET /api/watchers/{type}/{name} ──────────────────────────────────────────

bold "4. GET /api/watchers/docker/docker (single watcher)"

WATCHER1=$(curl_api /api/watchers/docker/docker 2>/dev/null) && WATCHER1_OK=true || WATCHER1_OK=false
if [[ $WATCHER1_OK == "true" ]]; then
	assert "/api/watchers/docker/docker returns 200" "pass"

	IS_OBJ=$(printf '%s' "$WATCHER1" | jq 'type == "object"' 2>/dev/null || true)
	assert_equal "$IS_OBJ" "true" "/api/watchers/docker/docker body is JSON object" "got: $(printf '%s' "$WATCHER1" | head -c 200)"

	W1_TYPE=$(printf '%s' "$WATCHER1" | jq -r '.type // empty' 2>/dev/null || true)
	W1_NAME=$(printf '%s' "$WATCHER1" | jq -r '.name // empty' 2>/dev/null || true)
	assert_equal "$W1_TYPE" "docker" "watcher type == docker" "got: $W1_TYPE"
	assert_equal "$W1_NAME" "docker" "watcher name == docker" "got: $W1_NAME"
else
	assert "/api/watchers/docker/docker returns 200" "fail" "curl failed (endpoint may be missing)"
	assert "/api/watchers/docker/docker body is JSON object" "fail" "skipped"
fi

# ── /api/watchers/unknown/missing should 404 ─────────────────────────────────

bold "5. GET /api/watchers/unknown/missing (should 404)"

if [[ -n $TOKEN ]]; then
	HTTP_CODE=$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 \
		-H "X-Portwing-Token: ${TOKEN}" \
		"${HOST}/api/watchers/unknown/missing" 2>/dev/null || echo "000")
else
	HTTP_CODE=$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 \
		"${HOST}/api/watchers/unknown/missing" 2>/dev/null || echo "000")
fi

assert_equal "$HTTP_CODE" "404" "/api/watchers/unknown/missing returns 404" "got HTTP $HTTP_CODE"

# ── /api/triggers ─────────────────────────────────────────────────────────────

bold "6. GET /api/triggers"

TRIGGERS=$(curl_api /api/triggers 2>/dev/null) && TRIGGERS_OK=true || TRIGGERS_OK=false
if [[ $TRIGGERS_OK == "true" ]]; then
	assert "/api/triggers returns 200" "pass"
	IS_ARRAY=$(printf '%s' "$TRIGGERS" | jq 'type == "array"' 2>/dev/null || true)
	assert_equal "$IS_ARRAY" "true" "/api/triggers body is JSON array" "got: $(printf '%s' "$TRIGGERS" | head -c 200)"
	TRIGGER_COUNT=$(printf '%s' "$TRIGGERS" | jq 'length' 2>/dev/null || true)
	assert_equal "$TRIGGER_COUNT" "0" "/api/triggers advertises no remote trigger" "got: $TRIGGERS"
else
	assert "/api/triggers returns 200" "fail" "curl failed"
	assert "/api/triggers body is JSON array" "fail" "skipped"
	assert "/api/triggers advertises no remote trigger" "fail" "skipped"
fi

# ── /api/log/entries ──────────────────────────────────────────────────────────

bold "7. GET /api/log/entries"

LOG_ENTRIES=$(curl_api /api/log/entries 2>/dev/null) && LOG_OK=true || LOG_OK=false
if [[ $LOG_OK == "true" ]]; then
	assert "/api/log/entries returns 200" "pass"
	IS_ARRAY=$(printf '%s' "$LOG_ENTRIES" | jq 'type == "array"' 2>/dev/null || true)
	assert_equal "$IS_ARRAY" "true" "/api/log/entries body is JSON array" "got: $(printf '%s' "$LOG_ENTRIES" | head -c 200)"
else
	assert "/api/log/entries returns 200" "fail" "curl failed (endpoint may be missing)"
	assert "/api/log/entries body is JSON array" "fail" "skipped"
fi

# ── /api/events SSE headers ──────────────────────────────────────────────────

bold "8. GET /api/events SSE headers"

AUTH_ARGS=()
[[ -n $TOKEN ]] && AUTH_ARGS+=(-H "X-Portwing-Token: ${TOKEN}")

SSE_HEADERS=$(curl -sf --max-time 5 -D - -o /dev/null \
	${AUTH_ARGS[@]+"${AUTH_ARGS[@]}"} \
	"${HOST}/api/events" 2>/dev/null || true)

CT=$(printf '%s' "$SSE_HEADERS" | grep -i 'content-type:' | head -1 | tr -d '\r') || true
CC=$(printf '%s' "$SSE_HEADERS" | grep -i 'cache-control:' | head -1 | tr -d '\r') || true

if printf '%s' "$CT" | grep -qi 'text/event-stream'; then
	assert "/api/events Content-Type: text/event-stream" "pass"
else
	assert "/api/events Content-Type: text/event-stream" "fail" "got: $CT"
fi

if printf '%s' "$CC" | grep -qi 'no-cache'; then
	assert "/api/events Cache-Control: no-cache" "pass"
else
	assert "/api/events Cache-Control: no-cache" "fail" "got: $CC"
fi

# ── /api/events dd:ack payload ──────────────────────────────────────────────

bold "9. GET /api/events dd:ack event shape"

SSE_BODY=$(curl -sf --max-time 5 \
	${AUTH_ARGS[@]+"${AUTH_ARGS[@]}"} \
	"${HOST}/api/events" 2>/dev/null || true)

ACK_LINE=$(printf '%s' "$SSE_BODY" | grep '^data:' | head -1 | sed 's/^data: //') || true

if [[ -n $ACK_LINE ]]; then
	ACK_TYPE=$(printf '%s' "$ACK_LINE" | jq -r '.type // empty' 2>/dev/null || true)
	ACK_VERSION=$(printf '%s' "$ACK_LINE" | jq -r '.data.version // empty' 2>/dev/null || true)
	ACK_OS=$(printf '%s' "$ACK_LINE" | jq -r '.data.os // empty' 2>/dev/null || true)
	ACK_ARCH=$(printf '%s' "$ACK_LINE" | jq -r '.data.arch // empty' 2>/dev/null || true)
	ACK_CPUS=$(printf '%s' "$ACK_LINE" | jq -r '.data.cpus // empty' 2>/dev/null || true)
	ACK_UPTIME=$(printf '%s' "$ACK_LINE" | jq -r '.data.uptimeSeconds // empty' 2>/dev/null || true)
	ACK_LAST=$(printf '%s' "$ACK_LINE" | jq -r '.data.lastSeen // empty' 2>/dev/null || true)

	assert_equal "$ACK_TYPE" "dd:ack" "dd:ack event type is dd:ack" "got: $ACK_TYPE"
	assert_present "$ACK_VERSION" "dd:ack data.version present"
	assert_present "$ACK_OS" "dd:ack data.os present"
	assert_present "$ACK_ARCH" "dd:ack data.arch present"
	assert_present "$ACK_CPUS" "dd:ack data.cpus present"
	assert_present "$ACK_UPTIME" "dd:ack data.uptimeSeconds present"
	assert_present "$ACK_LAST" "dd:ack data.lastSeen present"
else
	assert "dd:ack event type is dd:ack" "fail" "no data: line received from /api/events"
	for f in "data.version" "data.os" "data.arch" "data.cpus" "data.uptimeSeconds" "data.lastSeen"; do
		assert "dd:ack ${f} present" "fail" "skipped"
	done
fi

# ── summary ───────────────────────────────────────────────────────────────────

printf '\n'
bold "Results: ${PASS} passed, ${FAIL} failed"

if [[ $FAIL -gt 0 ]]; then
	red "Failed checks:"
	for f in "${FAILURES[@]}"; do
		red "  - $f"
	done
	exit 1
fi

green "All Drydock compatibility checks passed."
exit 0
