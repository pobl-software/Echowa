#!/usr/bin/env bash
# Builds a release and lays it out in tmp/channel the way the R2 bucket
# holds it, with install.sh pointed at http://127.0.0.1:8765, for testing
# the installer and the updater in a throwaway machine (docs/verification.md,
# "Installer" and "Updater").
#
# Everything is signed with a throwaway key (tmp/channel-key), and the
# binaries are built trusting that key instead of the real one, through a
# build overlay: app/release/minisign.pub isn't touched. Never publish them.
#
#   scripts/test-channel.sh v0.2.0            # build v0.2.0 and make it the newest
#   scripts/test-channel.sh v0.2.1 --broken   # a release whose server exits at start
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
v=${1:?usage: scripts/test-channel.sh vX.Y.Z [--broken]}
broken=${2:-}
command -v minisign >/dev/null || { echo "minisign isn't installed (brew install minisign)" >&2; exit 1; }

K=$PWD/tmp/channel-key C=tmp/channel
mkdir -p "$K"
[[ -f $K/test.key ]] || minisign -G -W -p "$K/test.pub" -s "$K/test.key" >/dev/null
replace="\"$PWD/app/release/minisign.pub\": \"$K/test.pub\""
if [[ $broken == --broken ]]; then
	cat >"$K/broken.go" <<'GO'
package main

import (
	"fmt"
	"os"
)

// Only the server (no arguments) fails: setup, version and backup still work.
func init() {
	if len(os.Args) == 1 {
		fmt.Println("this test release is broken on purpose")
		os.Exit(1)
	}
}
GO
	replace+=", \"$PWD/zz_broken.go\": \"$K/broken.go\""
fi
echo "{\"Replace\": {$replace}}" >"$K/overlay.json"

make release VERSION="$v" RELEASE_URL=http://127.0.0.1:8765 BUILDFLAGS="-tags release -trimpath -overlay=$K/overlay.json" 2>&1 | grep -E "^building"
mkdir -p "$C/releases/$v"
cp dist/echowa-linux-amd64 dist/echowa-linux-arm64 "$C/releases/$v/"
echo "- Test release $v${broken:+ (broken on purpose)}" >"$C/releases/$v/notes.md"
sed 's|@RELEASE_URL@|http://127.0.0.1:8765|g' deploy/install.sh >"$C/install.sh"
sha() { shasum -a 256 "$1" | cut -d' ' -f1; }
cat >"$C/latest.json" <<JSON
{
  "version": "$v",
  "published_at": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "notes": "releases/$v/notes.md",
  "binaries": {
    "linux/amd64": { "path": "releases/$v/echowa-linux-amd64", "sha256": "$(sha "$C/releases/$v/echowa-linux-amd64")" },
    "linux/arm64": { "path": "releases/$v/echowa-linux-arm64", "sha256": "$(sha "$C/releases/$v/echowa-linux-arm64")" }
  }
}
JSON
for file in "$C/releases/$v"/echowa-linux-* "$C/releases/$v/notes.md" "$C/latest.json"; do
	[[ $file == *.minisig ]] && continue
	minisign -S -s "$K/test.key" -m "$file" -t "echowa $v ${file##*/}" >/dev/null
done
echo "tmp/channel: $v is the newest release${broken:+ (broken on purpose)}"
