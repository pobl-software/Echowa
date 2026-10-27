#!/bin/sh
# Proves a redeploy drops no requests: requests URL about 20 times a second
# for SECONDS seconds, prints every non-200 and the first answer from each
# container (when the response is traefik/whoami's), then a summary. Start
# it, then deploy while it runs.
#
#   scripts/zero-downtime.sh [URL] [SECONDS]
#   scripts/zero-downtime.sh http://whoami.localhost:8090 90
#
# Exits 1 if any request failed.

url=${1:-http://whoami.localhost:8090}
end=$(($(date +%s) + ${2:-90}))
ok=0
fail=0
hosts=""

while [ "$(date +%s)" -lt "$end" ]; do
	out=$(curl -s -m 2 -w '\n%{http_code}' "$url")
	code=$(printf '%s\n' "$out" | tail -n 1)
	if [ "$code" = 200 ]; then
		ok=$((ok + 1))
		host=$(printf '%s\n' "$out" | sed -n 's/^Hostname: //p')
		if [ -n "$host" ]; then
			case " $hosts " in
			*" $host "*) ;;
			*)
				hosts="$hosts $host"
				echo "$(date +%T) first answer from container $host"
				;;
			esac
		fi
	else
		fail=$((fail + 1))
		echo "$(date +%T) HTTP $code"
	fi
	sleep 0.05
done

echo "ok=$ok failed=$fail"
[ "$fail" -eq 0 ]
