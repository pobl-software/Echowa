#!/usr/bin/env bash
# Measures what the metrics collector costs, in the dev machine
# (docs/verification.md, "Metrics"):
#
#   - Echowa's own CPU time over a run of readings 15 seconds apart, from
#     `artisan metrics:sample`.
#   - dockerd's CPU while those readings run, against an idle stretch of the
#     same length just before.
#
# It starts throwaway whoami containers until at least 10 are running, and
# removes them on exit.
#
#   scripts/metrics-overhead.sh [minutes]    (default 5, per run)
#
# ECHOWA_DEV_VM names the machine (default echowa-dev).

set -euo pipefail
cd "$(dirname "$0")/.."

VM=${ECHOWA_DEV_VM:-echowa-dev}
minutes=${1:-5}
readings=$((minutes * 4 + 1))
seconds=$(((readings - 1) * 15))

in_vm() { orb -m "$VM" "$@"; }
# dockerd's user plus system time, in clock ticks.
dockerd_ticks() { in_vm sh -c 'awk "{print \$14 + \$15}" /proc/$(pidof dockerd)/stat'; }
hz=$(in_vm getconf CLK_TCK)

cleanup() { in_vm sh -c 'docker ps -aq -f label=echowa.overhead-test | xargs -r docker rm -f' >/dev/null; }
trap cleanup EXIT
running=$(in_vm docker ps -q | wc -l)
for _ in $(seq $((10 - running))); do
	in_vm docker run -d --label echowa.overhead-test traefik/whoami:v1.12.0 >/dev/null
done
echo "containers running: $(in_vm docker ps -q | wc -l | tr -d ' ')"

# Build first, so the timed run below doesn't include the cross-compile.
deploy/dev-vm.sh artisan list >/dev/null

echo "idle: ${seconds}s"
start=$(date +%s) before=$(dockerd_ticks)
sleep "$seconds"
idle_ticks=$(($(dockerd_ticks) - before)) idle_wall=$(($(date +%s) - start))

echo "sampling: $readings readings"
log=$(mktemp)
start=$(date +%s) before=$(dockerd_ticks)
deploy/dev-vm.sh artisan metrics:sample --readings "$readings" 2>"$log" >/dev/null
busy_ticks=$(($(dockerd_ticks) - before)) busy_wall=$(($(date +%s) - start))

slowest=$(sed -n 's/^reading .*: host \([^,]*\), docker \([^ ]*\) .*/\1 \2/p' "$log" | awk '
	function ms(d) { if (d ~ /µs$/) return d / 1000; if (d ~ /ms$/) return d + 0; return d * 1000 }
	{ t = ms($1) + ms($2); if (t > max) max = t }
	END { printf "%.1fms", max }')
grep -E '^(warning|CPU time)' "$log" || true
echo "slowest reading: $slowest"
awk -v i="$idle_ticks" -v iw="$idle_wall" -v b="$busy_ticks" -v bw="$busy_wall" -v hz="$hz" 'BEGIN {
	idle = i / hz / iw * 100; busy = b / hz / bw * 100
	printf "dockerd CPU: %.2f%% of a core idle, %.2f%% while sampling (+%.2f)\n", idle, busy, busy - idle
}'
rm -f "$log"
