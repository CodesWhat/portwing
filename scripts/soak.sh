#!/usr/bin/env bash
#
# soak.sh — RSS + thread-drift soak for the Portwing agent.
#
# Stands up the long-lived topology the unit/integration tiers don't exercise:
#
#     loadgen (HTTP) ──▶ portwing (generic adapter) ──▶ mockdocker (unix socket)
#
# then drives a sustained mixed load — cached-inventory reads, version/info,
# a raw Docker proxy read, and (the leak stressor) a stream of SSE subscribers
# that connect, hold, and disconnect — for the configured duration. It samples
# the agent's resident set over the run and fails if working-set growth from
# the post-warmup baseline exceeds the threshold. That's the "zero RSS/goroutine
# growth over a long soak" signal you can't get from a short test.
#
# GitHub-hosted runners cap a job at 6h, so CI soaks for 4h by default — long
# enough that a per-request allocation/goroutine leak shows up as multi-MiB RSS
# growth well above the 64 MiB threshold. A self-hosted runner can push the
# duration input toward the 24h target.
#
# Usage:
#   scripts/soak.sh [--duration 4h] [--concurrency 20] \
#                   [--rss-growth-threshold-bytes 67108864] \
#                   [--warmup 30s] [--port 38080] [--dry-run]
#
set -euo pipefail

DURATION="4h"
CONCURRENCY="20"
RSS_THRESHOLD="67108864" # 64 MiB
WARMUP="30s"
PORT="38080"
DRY_RUN="0"

die() {
	echo "soak: $*" >&2
	exit 2
}

while [ $# -gt 0 ]; do
	case "$1" in
	--duration)
		DURATION="${2:?}"
		shift 2
		;;
	--concurrency)
		CONCURRENCY="${2:?}"
		shift 2
		;;
	--rss-growth-threshold-bytes)
		RSS_THRESHOLD="${2:?}"
		shift 2
		;;
	--warmup)
		WARMUP="${2:?}"
		shift 2
		;;
	--port)
		PORT="${2:?}"
		shift 2
		;;
	--dry-run)
		DRY_RUN="1"
		shift
		;;
	-h | --help)
		sed -n '2,30p' "$0"
		exit 0
		;;
	*) die "unknown argument: $1" ;;
	esac
done

# Validate the option surface (the workflow dry-runs this before a real soak).
[[ $DURATION =~ ^[0-9]+(h|m|s)([0-9]+(m|s))?$ ]] || die "invalid --duration: $DURATION"
[[ $WARMUP =~ ^[0-9]+(h|m|s)([0-9]+(m|s))?$ ]] || die "invalid --warmup: $WARMUP"
[[ $CONCURRENCY =~ ^[0-9]+$ ]] || die "invalid --concurrency: $CONCURRENCY"
[[ $RSS_THRESHOLD =~ ^[0-9]+$ ]] || die "invalid --rss-growth-threshold-bytes: $RSS_THRESHOLD"
[[ $PORT =~ ^[0-9]+$ ]] || die "invalid --port: $PORT"

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BINDIR="$(mktemp -d)"
RUNDIR="$(mktemp -d)"
SOCK="$RUNDIR/mock.sock"
TOKEN="soak-token"
BASE="http://127.0.0.1:$PORT"

cleanup() {
	[ -n "${SAMPLER_PID:-}" ] && kill "$SAMPLER_PID" 2>/dev/null || true
	[ -n "${PW_PID:-}" ] && kill "$PW_PID" 2>/dev/null || true
	[ -n "${MOCK_PID:-}" ] && kill "$MOCK_PID" 2>/dev/null || true
	rm -rf "$BINDIR" "$RUNDIR" 2>/dev/null || true
}
trap cleanup EXIT

echo "soak: building mockdocker, loadgen, portwing…"
(cd "$ROOT" && go build -o "$BINDIR/mockdocker" ./benchmarks/cmd/mockdocker)
(cd "$ROOT" && go build -o "$BINDIR/loadgen" ./benchmarks/cmd/loadgen)
(cd "$ROOT" && go build -o "$BINDIR/portwing" ./cmd/portwing)

echo "soak: resolved → duration=$DURATION concurrency=$CONCURRENCY warmup=$WARMUP port=$PORT threshold=${RSS_THRESHOLD}B"

if [ "$DRY_RUN" = "1" ]; then
	echo "soak: --dry-run OK (binaries build, parameters valid); not running the soak."
	exit 0
fi

# rss_kb PID → resident set in KiB (portable: ps works on Linux + macOS).
rss_kb() { ps -o rss= -p "$1" 2>/dev/null | tr -d ' '; }

echo "soak: starting mockdocker on $SOCK"
"$BINDIR/mockdocker" -socket "$SOCK" &
MOCK_PID=$!
for _ in $(seq 1 50); do
	[ -S "$SOCK" ] && break
	sleep 0.1
done
[ -S "$SOCK" ] || die "mockdocker socket never appeared"

echo "soak: starting portwing (generic adapter) on $BASE"
TOKEN="$TOKEN" ADAPTER=generic DOCKER_SOCKET="$SOCK" PORT="$PORT" \
	BIND_ADDRESS=127.0.0.1 LOG_LEVEL=warn REQUEST_TIMEOUT=10 NO_COLOR=1 \
	"$BINDIR/portwing" &
PW_PID=$!

ok=""
for _ in $(seq 1 60); do
	if curl -fsS "$BASE/_portwing/health" >/dev/null 2>&1; then
		ok=1
		break
	fi
	kill -0 "$PW_PID" 2>/dev/null || die "portwing exited during startup"
	sleep 0.5
done
[ -n "$ok" ] || die "portwing health never went green"

echo "soak: warmup ${WARMUP}…"
"$BINDIR/loadgen" -base "$BASE" -auth "$TOKEN" -path /api/v1/containers \
	-concurrency "$CONCURRENCY" -duration "$WARMUP" -scenario warmup >/dev/null 2>&1 || true

sleep 3
BASELINE_KB="$(rss_kb "$PW_PID")"
[ -n "$BASELINE_KB" ] || die "could not read portwing RSS (process gone?)"
echo "soak: post-warmup baseline RSS = ${BASELINE_KB} KiB"

# Background RSS sampler.
(
	start=$(date +%s)
	while sleep 60; do
		now=$(date +%s)
		cur="$(rss_kb "$PW_PID")"
		[ -n "$cur" ] || break
		echo "soak: [+$((now - start))s] rss=${cur} KiB"
	done
) &
SAMPLER_PID=$!
disown "$SAMPLER_PID" 2>/dev/null || true # silence job-control "Terminated" notice on kill

half=$((CONCURRENCY / 2))
[ "$half" -lt 1 ] && half=1

echo "soak: driving load for $DURATION (concurrency=$CONCURRENCY)…"
SUMMARY="$RUNDIR/summary.jsonl"
: >"$SUMMARY"
pids=()
"$BINDIR/loadgen" -base "$BASE" -auth "$TOKEN" -path /api/v1/containers -concurrency "$CONCURRENCY" -duration "$DURATION" -scenario inventory >>"$SUMMARY" &
pids+=($!)
"$BINDIR/loadgen" -base "$BASE" -auth "$TOKEN" -path /api/v1/version -concurrency "$half" -duration "$DURATION" -scenario version >>"$SUMMARY" &
pids+=($!)
"$BINDIR/loadgen" -base "$BASE" -auth "$TOKEN" -path /v1.44/containers/json -concurrency "$half" -duration "$DURATION" -scenario proxy >>"$SUMMARY" &
pids+=($!)
"$BINDIR/loadgen" -base "$BASE" -path /_portwing/health -concurrency 5 -duration "$DURATION" -scenario health >>"$SUMMARY" &
pids+=($!)
"$BINDIR/loadgen" -base "$BASE" -auth "$TOKEN" -path /api/v1/events -mode sse -sse-hold 1s -concurrency "$half" -duration "$DURATION" -scenario sse-churn >>"$SUMMARY" &
pids+=($!)

fail=0
for p in "${pids[@]}"; do wait "$p" || fail=1; done

kill "$SAMPLER_PID" 2>/dev/null || true
SAMPLER_PID=""

sleep 5
FINAL_KB="$(rss_kb "$PW_PID")"
[ -n "$FINAL_KB" ] || die "portwing exited during the soak"

GROWTH_BYTES=$(((FINAL_KB - BASELINE_KB) * 1024))

echo ""
echo "soak: ───────── per-scenario results ─────────"
cat "$SUMMARY"
echo "soak: ─────────────────────────────────────────"
echo "soak: baseline=${BASELINE_KB} KiB  final=${FINAL_KB} KiB  growth=${GROWTH_BYTES} B  threshold=${RSS_THRESHOLD} B"

if [ "$fail" -ne 0 ]; then
	die "a loadgen scenario exited non-zero"
fi
if [ "$GROWTH_BYTES" -gt "$RSS_THRESHOLD" ]; then
	echo "soak: FAIL — RSS grew ${GROWTH_BYTES} B, over the ${RSS_THRESHOLD} B budget" >&2
	exit 1
fi
echo "soak: PASS — RSS growth within budget"
