#!/usr/bin/env bash
# Installs Echowa on a fresh Ubuntu or Debian server, or upgrades it (ADR 0021):
#
#   curl -fsSL @RELEASE_URL@/install.sh | sudo bash
#   curl -fsSL @RELEASE_URL@/install.sh | sudo bash -s -- --access https --domain echowa.example.com
#
# Run it on a fresh Ubuntu or Debian server. It downloads the newest release for this
# machine, checks it against the SHA-256 in latest.json, and runs
# `echowa setup` with any options given, which installs Docker, the Swarm,
# Traefik and Echowa itself. On a first install it asks how the UI should be
# reached: HTTP or HTTPS on port 8192, or a Cloudflare Tunnel.
# `echowa setup --help` lists the options.
#
# make publish uploads this file with @RELEASE_URL@ filled in.
# ECHOWA_CHANNEL points it at another channel, for testing.

set -euo pipefail

# Everything runs from main, called on the last line, so a download cut off
# half way runs nothing.
main() {
	local channel=${ECHOWA_CHANNEL:-@RELEASE_URL@}
	channel=${channel%/}

	[[ $(uname -s) == Linux ]] || fail "Echowa runs on Linux"
	[[ $EUID -eq 0 ]] || fail "run as root: curl -fsSL $channel/install.sh | sudo bash"
	command -v curl >/dev/null || fail "curl is needed"
	command -v sha256sum >/dev/null || fail "sha256sum is needed"

	local arch
	case $(uname -m) in
	x86_64 | amd64) arch=amd64 ;;
	aarch64 | arm64) arch=arm64 ;;
	*) fail "no Echowa build for $(uname -m)" ;;
	esac

	local tmp
	tmp=$(mktemp -d)
	# Expanded now: $tmp is local to main, gone by the time the trap runs.
	# The status line is cleared too, should Ctrl-C leave one.
	# shellcheck disable=SC2064
	trap "status ''; rm -rf '$tmp'" EXIT

	status "Finding the newest release"
	get "$channel/latest.json" "$tmp/latest.json"

	# make publish writes one line per binary: "linux/<arch>": { "path": ..., "sha256": ... }
	local version entry path sha256
	version=$(sed -n 's/^ *"version": *"\(v[0-9.]*\)".*/\1/p' "$tmp/latest.json")
	entry=$(grep "\"linux/$arch\"" "$tmp/latest.json" || true)
	path=$(sed -n 's/.*"path": *"\([^"]*\)".*/\1/p' <<<"$entry")
	sha256=$(sed -n 's/.*"sha256": *"\([0-9a-f]\{64\}\)".*/\1/p' <<<"$entry")
	[[ -n $version && -n $path && -n $sha256 ]] || fail "latest.json has no linux/$arch build"
	[[ $path == "releases/$version/"* && $path != *..* ]] || fail "latest.json points outside its release"

	status "Downloading Echowa $version for linux/$arch"
	get "$channel/$path" "$tmp/echowa"
	echo "$sha256  $tmp/echowa" | sha256sum -c --quiet - >/dev/null 2>&1 || fail "the download doesn't match latest.json's checksum"
	chmod 0755 "$tmp/echowa"
	status ""

	"$tmp/echowa" setup "$@"
}

# get downloads url to file, over HTTPS only unless the channel is a local
# test one.
get() {
	local proto=() err
	[[ $1 == https://* ]] && proto=(--proto '=https' --tlsv1.2)
	# curl's complaint goes in fail's message, not after the status line.
	err=$(curl -fsSL "${proto[@]}" -o "$2" "$1" 2>&1) || fail "couldn't download $1${err:+ ($err)}"
}

# status shows what's happening on one line that the next replaces, and
# that setup's own output then takes over. Off a terminal it's one line
# each.
status() {
	if [[ -t 1 && ${TERM:-} != dumb ]]; then
		printf '\r\033[K'
		[[ -z $1 ]] || printf '  %s\033[90m%s…\033[0m' "$(accent '● ')" "$1"
	elif [[ -n $1 ]]; then
		printf '%s\n' "$1"
	fi
}

# accent is the logo's orange, as setup draws it.
accent() {
	if [[ -t 1 && -z ${NO_COLOR:-} ]]; then
		printf '\033[38;5;166m%s\033[0m' "$1"
	else
		printf '%s' "$1"
	fi
}

fail() {
	status ""
	if [[ -t 2 && -z ${NO_COLOR:-} ]]; then
		printf '\n  \033[31m✗\033[0m \033[1mEchowa couldn'"'"'t install: %s\033[0m\n\n' "$*" >&2
	else
		printf 'error: %s\n' "$*" >&2
	fi
	exit 1
}

main "$@"
