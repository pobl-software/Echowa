#!/usr/bin/env bash
# Development server: an OrbStack Ubuntu 24.04 machine that runs Docker,
# Swarm, Traefik and Echowa itself, as production will. The Mac keeps the
# editor, Vite and the browser. Echowa is cross-compiled on the Mac and run
# in the machine from the shared repo folder, next to its Docker socket, so
# the Docker API is never exposed. OrbStack forwards the machine's
# localhost ports to the Mac's localhost:
#
#   http://localhost:3000            Echowa (UI and webhooks)
#   http://<app>.localhost:8090      deployed apps, through Traefik
#   http://localhost:7070            an S3 server for backups (deploy/s3)
#   http://localhost:8125            Mailpit, SMTP on 2525 (deploy/notify)
#   http://localhost:8095            ntfy (deploy/notify)
#
# Usage, from the repo root:
#
#   deploy/dev-vm.sh up              create or update the machine (idempotent)
#   deploy/dev-vm.sh serve           build and run Echowa in it
#   deploy/dev-vm.sh build           only build the Linux binary (air runs this,
#                                    then serve with ECHOWA_SKIP_BUILD=1)
#   deploy/dev-vm.sh stop            stop Echowa in the machine
#   deploy/dev-vm.sh artisan <cmd>   run an artisan command against its database
#   deploy/dev-vm.sh test-docker     run the Docker, S3 and restore integration tests
#                                    in it
#   deploy/dev-vm.sh docker-conns    print Echowa's open connections to dockerd
#                                    every second; each open log stream holds one
#   deploy/dev-vm.sh shell           open a shell in the machine
#   deploy/dev-vm.sh destroy         delete the machine and everything in it
#
# ECHOWA_DEV_VM names the machine (default echowa-dev).

set -euo pipefail

VM=${ECHOWA_DEV_VM:-echowa-dev}
TRAEFIK_PORT=8090
S3_PORT=7070
S3_BUCKET=echowa-dev
DATA_DIR=/var/lib/echowa-dev
BIN=tmp/echowa-linux

log() { printf '\033[1m==>\033[0m %s\n' "$*" >&2; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

cd "$(dirname "$0")/.."
command -v orb >/dev/null || die "OrbStack's orb CLI isn't installed (https://orbstack.dev)"

in_vm() { orb -m "$VM" "$@"; }

# For anything unattended: checks, helpers, and Echowa itself under air.
# orb forwards stdin, and these mustn't eat input meant for the command
# that follows (e.g. a password on stdin). orb also sets up the terminal
# when any of its streams is one, and in a background process group (air
# runs its commands in one) that gets it stopped by SIGTTOU: the command
# hangs. So it gets no terminal at all: stdin from /dev/null, stdout and
# stderr through pipes.
in_vm_quiet() { orb -m "$VM" "$@" </dev/null > >(cat) 2> >(cat >&2); }

vm_exists() { orb list --quiet 2>/dev/null | grep -qx "$VM"; }

require_vm() {
	vm_exists || die "no machine $VM yet; run deploy/dev-vm.sh up"
	in_vm_quiet true >/dev/null 2>&1 || orb start "$VM" >/dev/null
}

up() {
	if vm_exists; then
		log "Machine $VM exists"
		in_vm true >/dev/null 2>&1 || orb start "$VM" >/dev/null
	else
		log "Creating Ubuntu 24.04 machine $VM"
		orb create ubuntu:noble "$VM" >/dev/null
	fi

	# Docker, the Swarm and the network, the way servers get them.
	build
	in_vm sudo "./$BIN" setup --host-only

	local user
	user=$(in_vm id -un)
	if ! in_vm id -nG | grep -qw docker; then
		log "Adding $user to the docker group in $VM"
		in_vm sudo usermod -aG docker "$user"
	fi
	in_vm sudo install -d -o "$user" "$DATA_DIR"

	log "Deploying Traefik on port $TRAEFIK_PORT (http://<app>.localhost:$TRAEFIK_PORT)"
	in_vm sudo env TRAEFIK_HTTP_PORT=$TRAEFIK_PORT \
		docker stack deploy --detach=false --quiet -c deploy/traefik/stack.local.yml traefik >/dev/null

	log "Deploying an S3 server on port $S3_PORT (bucket $S3_BUCKET, keys in deploy/s3/stack.local.yml)"
	in_vm sudo docker stack deploy --detach=false --quiet -c deploy/s3/stack.local.yml s3 >/dev/null
	s3_bucket

	log "Deploying Mailpit (SMTP on 2525, mail at http://localhost:8125) and ntfy (http://localhost:8095)"
	in_vm sudo docker stack deploy --detach=false --quiet -c deploy/notify/stack.local.yml notify >/dev/null

	log "Migrating $DATA_DIR/echowa.sqlite"
	run_bin in_vm_quiet artisan migrate

	log "Ready. Next: deploy/dev-vm.sh artisan user:create, then start the dev servers (AGENTS.md)."
}

# s3_bucket makes the dev bucket once the S3 server answers. A bucket that
# already exists answers 409, which is fine.
s3_bucket() {
	in_vm_quiet bash -c "for _ in \$(seq 1 30); do
			code=\$(curl -s -o /dev/null -w '%{http_code}' --aws-sigv4 aws:amz:us-east-1:s3 \
				--user echowa-dev:echowa-dev-secret -X PUT http://localhost:$S3_PORT/$S3_BUCKET) || code=000
			case \$code in 200 | 409) exit 0 ;; esac
			sleep 1
		done
		echo \"the S3 server didn't make the bucket (last answer: \$code)\" >&2
		exit 1"
}

# Cross-compiles for the machine. Pure Go (no cgo), so it's a plain build.
build() {
	local arch
	case $(in_vm_quiet uname -m) in
	aarch64 | arm64) arch=arm64 ;;
	x86_64) arch=amd64 ;;
	*) die "unsupported machine architecture" ;;
	esac
	GOOS=linux GOARCH=$arch CGO_ENABLED=0 go build -o "$BIN" .
}

# run_bin <in_vm|in_vm_quiet> [args]: runs the Linux binary in the
# machine with the dev settings. Real environment variables win over .env,
# which it still reads from the repo. Use in_vm only for commands that need
# the terminal (artisan's prompts).
run_bin() {
	local run=$1
	shift
	"$run" env \
		DB_DATABASE="$DATA_DIR/echowa.sqlite" \
		APP_HOST=127.0.0.1 \
		APP_PORT=3000 \
		APP_URL=http://localhost:3000 \
		"./$BIN" "$@"
}

# Stopping the orb client doesn't stop the process inside the machine, so
# stop it explicitly. Goravel waits for running deploys before exiting.
stop_server() {
	in_vm_quiet bash -c "pkill -TERM -f '^./$BIN\$' || exit 0
		for _ in \$(seq 1 30); do pgrep -f '^./$BIN\$' >/dev/null || exit 0; sleep 1; done
		pkill -KILL -f '^./$BIN\$' || true"
}

serve() {
	require_vm
	[[ ${ECHOWA_SKIP_BUILD:-} == 1 ]] || build
	stop_server
	trap 'stop_server; exit 0' INT TERM
	log "Echowa on http://localhost:3000 (running in $VM)"
	run_bin in_vm_quiet &
	wait $!
}

# docker_conns counts Echowa's connections to dockerd's socket: its unix
# sockets whose peer belongs to dockerd. The loop runs on the Mac, because
# stopping orb doesn't stop what it started in the machine.
docker_conns() {
	require_vm
	log "Echowa's open connections to dockerd (Ctrl-C to stop)"
	while :; do
		# shellcheck disable=SC2016 # expanded in the machine
		count=$(in_vm_quiet sudo sh -c '
			sockets=$(ss -xpH)
			n=0
			for peer in $(printf "%s\n" "$sockets" | awk "/\"echowa-linux\"/ {print \$8}"); do
				printf "%s\n" "$sockets" | grep -w "$peer" | grep -q "\"dockerd\"" && n=$((n + 1))
			done
			echo $n')
		printf '%s  %s\n' "$(date +%T)" "$count"
		sleep 1
	done
}

case ${1:-} in
up) up ;;
build) require_vm && build ;;
serve) serve ;;
stop) require_vm && stop_server ;;
artisan)
	shift
	require_vm
	build
	run_bin in_vm artisan "$@"
	;;
test-docker)
	require_vm
	for pkg in docker templates backups; do
		GOOS=linux GOARCH=$(go env GOARCH) CGO_ENABLED=0 go test -c -tags docker -o "tmp/$pkg.test" "./app/$pkg"
		in_vm "./tmp/$pkg.test" -test.count=1 -test.v
	done
	# Echowa's own restore, from its deploy through the dev S3 server.
	GOOS=linux GOARCH=$(go env GOARCH) CGO_ENABLED=0 go test -c -tags docker -o tmp/feature.test ./tests/feature
	(cd tests/feature && in_vm ../../tmp/feature.test -test.count=1 -test.v -test.run 'Docker')
	;;
docker-conns) docker_conns ;;
shell) require_vm && orb -m "$VM" ;;
destroy)
	vm_exists || exit 0
	read -r -p "Delete machine $VM and everything in it? [y/N] " answer
	[[ $answer == [yY] ]] && orb delete -f "$VM"
	;;
*)
	sed -n '2,30p' "$0" | sed 's/^# \{0,1\}//'
	exit 1
	;;
esac
