#!/usr/bin/env bash
# Publishes the release at HEAD to the R2 release channel (ADR 0021): builds
# it, signs every file with the release key, writes latest.json, checks all
# the signatures, then uploads, latest.json last.
#
#   make publish TAG=v1.3.0                       # tag HEAD, then publish it
#   make publish                                  # publish HEAD's existing tag
#   scripts/publish.sh --dry-run [--version v0.0.0]   # build and sign into dist/channel only
#
# With TAG, your editor (git's core.editor, or $EDITOR) opens on the
# release notes' headings, New, Changes and Fixed, for you to fill in with
# "- " bullets. Saving tags HEAD with them, less any section left empty; a
# file with nothing filled in stops. Settings, Updates shows the headings
# and bullets as such.
#
# Needs a clean tree, minisign and the release key (MINISIGN_KEY, default ~/.minisign/echowa.key),
# RELEASE_URL and RELEASE_BUCKET (set in the Makefile), and wrangler signed in
# (`npx wrangler login`), or CLOUDFLARE_API_TOKEN and CLOUDFLARE_ACCOUNT_ID.

set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

WRANGLER=(npx --yes wrangler@4.152.0)
PUBKEY=app/release/minisign.pub
OUT=dist/channel
MINISIGN_KEY=${MINISIGN_KEY:-$HOME/.minisign/echowa.key}
RELEASE_URL=${RELEASE_URL%/}
RELEASE_BUCKET=${RELEASE_BUCKET:-}

log() { printf '\033[1m==>\033[0m %s\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

# The release notes' sections, for you to fill in.
NOTES_TEMPLATE='## New

- 

## Changes

- 

## Fixed

- 
'

# The notes less empty bullets, and less any heading with nothing under it.
tidy_notes() {
	awk '
		function flush() {
			if (body ~ /[^[:space:]]/) printf "%s%s", (heading == "" ? "" : heading "\n"), body
			heading = ""; body = ""
		}
		/^[-*][[:space:]]*$/ { next }
		/^#/ { flush(); heading = $0; next }
		{ body = body $0 "\n" }
		END { flush() }' "$1"
}

dry_run=0 version="" tag=""
while (($#)); do
	case $1 in
	--dry-run) dry_run=1; shift ;;
	--version) version=${2:?--version needs vX.Y.Z}; shift 2 ;;
	--tag) tag=${2:?--tag needs vX.Y.Z}; shift 2 ;;
	*) die "unknown option: $1" ;;
	esac
done

# 1. What's being published, and whether it may be.
current_tag=$(git describe --exact-match --tags --match 'v[0-9]*' HEAD 2>/dev/null || true)
if ((dry_run)); then
	[[ -z $tag ]] || die "--tag doesn't go with --dry-run, which never tags; use --version"
	version=${version:-${current_tag:-v0.0.0}}
else
	[[ -z $version ]] || die "--version is for --dry-run; use TAG=vX.Y.Z"
	[[ -z $(git status --porcelain) ]] || die "the tree isn't clean: commit or stash first"
	if [[ -n $tag ]]; then
		# Run again after a failure, the tag made last time is reused.
		[[ -z $current_tag || $current_tag == "$tag" ]] || die "HEAD is already tagged $current_tag"
		[[ $current_tag == "$tag" ]] || ! git rev-parse -q --verify "refs/tags/$tag" >/dev/null ||
			die "$tag already tags another commit"
		version=$tag
	else
		version=$current_tag
		[[ -n $version ]] || die "HEAD has no version tag: make publish TAG=vX.Y.Z"
	fi
	[[ -n $RELEASE_URL && -n $RELEASE_BUCKET ]] || die "set RELEASE_URL and RELEASE_BUCKET in the Makefile"
fi
[[ $version =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] || die "$version isn't vMAJOR.MINOR.PATCH"
command -v minisign >/dev/null || die "minisign isn't installed (brew install minisign)"
[[ -f $MINISIGN_KEY ]] || die "no release key at $MINISIGN_KEY (make release-key, or set MINISIGN_KEY)"
grep -q 'no release key yet' "$PUBKEY" && die "$PUBKEY is still the placeholder (make release-key)"

if ((!dry_run)); then
	if curl -fsI -o /dev/null "$RELEASE_URL/releases/$version/echowa-linux-amd64"; then
		die "$version is already published; releases never change, so tag a new version"
	fi
	current=$(curl -fsS "$RELEASE_URL/latest.json" 2>/dev/null | sed -n 's/.*"version": *"\(v[0-9.]*\)".*/\1/p' || true)
	if [[ -n $current ]]; then
		newest=$(printf '%s\n%s\n' "${current#v}" "${version#v}" | sort -t. -k1,1n -k2,2n -k3,3n | tail -1)
		[[ v$newest == "$version" && $current != "$version" ]] || die "$version isn't newer than the published $current"
		log "Publishing $version (the channel has $current)"
	else
		log "Publishing $version, the channel's first release"
	fi
fi

# Tag HEAD with the notes, once the checks above can no longer leave a tag
# that won't publish.
if [[ -n $tag && $current_tag != "$tag" ]]; then
	draft=$(mktemp)
	trap 'rm -f "$draft"' EXIT
	printf '%s' "$NOTES_TEMPLATE" >"$draft"
	log "Opening the notes for $version in your editor; fill them in and save to tag HEAD"
	# Run as git runs it: git's own -e would tag an emptied file.
	sh -c "$(git var GIT_EDITOR) \"\$@\"" editor "$draft" </dev/tty >/dev/tty || die "not tagged: the editor failed"
	notes=$(tidy_notes "$draft")
	[[ $notes == *[![:space:]]* ]] || die "not tagged: the notes are empty"
	printf '%s\n' "$notes" | git tag -a "$version" --cleanup=whitespace -F -
	log "Tagged $version:"
	git tag -l --format='%(contents)' "$version" | sed 's/^/    /'
fi

# 2. Build, and lay the files out as they'll be in the bucket.
make release VERSION="$version" RELEASE_URL="$RELEASE_URL"
rm -rf "$OUT"
dir=$OUT/releases/$version
mkdir -p "$dir"
cp dist/echowa-linux-amd64 dist/echowa-linux-arm64 dist/licenses.txt "$dir/"

if [[ $(git cat-file -t "$version" 2>/dev/null) == tag ]]; then
	git tag -l --format='%(contents)' "$version" >"$dir/notes.md"
else
	previous=$(git describe --tags --abbrev=0 --match 'v[0-9]*' --exclude "$version" HEAD 2>/dev/null || true)
	git log --no-merges --format='- %s' "${previous:+$previous..}HEAD" >"$dir/notes.md"
fi

sha256() { shasum -a 256 "$1" | cut -d' ' -f1; }
# deploy/install.sh reads this with sed, so keep one line per binary.
cat >"$OUT/latest.json" <<JSON
{
  "version": "$version",
  "published_at": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "notes": "releases/$version/notes.md",
  "binaries": {
    "linux/amd64": { "path": "releases/$version/echowa-linux-amd64", "sha256": "$(sha256 "$dir/echowa-linux-amd64")" },
    "linux/arm64": { "path": "releases/$version/echowa-linux-arm64", "sha256": "$(sha256 "$dir/echowa-linux-arm64")" }
  }
}
JSON

# 3. Sign each file, with the version and its name as the trusted comment
# (release.TrustedComment). The passphrase is asked for once and piped in.
files=("$dir/echowa-linux-amd64" "$dir/echowa-linux-arm64" "$dir/notes.md" "$dir/licenses.txt" "$OUT/latest.json")
# The one-line installer, pointed at this channel.
sed "s|@RELEASE_URL@|${RELEASE_URL:-https://get.example.com}|g" deploy/install.sh >"$OUT/install.sh"
files+=("$OUT/install.sh")
read -rsp "Passphrase for $MINISIGN_KEY: " passphrase </dev/tty
echo
for file in "${files[@]}"; do
	printf '%s\n' "$passphrase" | minisign -S -s "$MINISIGN_KEY" -m "$file" \
		-t "echowa $version ${file##*/}" -c "echowa $version" >/dev/null ||
		die "signing ${file##*/} failed (wrong passphrase?)"
done
unset passphrase

# 4. Check every signature against the public key that's built into Echowa.
for file in "${files[@]}"; do
	minisign -V -q -p "$PUBKEY" -m "$file" || die "${file##*/}: the signature doesn't match $PUBKEY"
done
log "Signed and checked $((${#files[@]})) files in $OUT"

if ((dry_run)); then
	find "$OUT" -type f | sort
	exit 0
fi

# 5. Upload. Release files first, cached for good since they never change;
# latest.json last, so it never names a file that isn't there yet.
put() {
	local file=$1 type=$2 cache=$3
	"${WRANGLER[@]}" r2 object put "$RELEASE_BUCKET/${file#"$OUT"/}" --remote \
		--file "$file" --content-type "$type" --cache-control "$cache" >/dev/null
	echo "  ${file#"$OUT"/}"
}
forever="public, max-age=31536000, immutable"
log "Uploading to $RELEASE_BUCKET"
for file in "$dir"/*; do
	case $file in
	*.minisig) put "$file" text/plain "$forever" ;;
	*.md) put "$file" "text/markdown; charset=utf-8" "$forever" ;;
	*) put "$file" application/octet-stream "$forever" ;;
	esac
done
put "$OUT/install.sh" "text/x-shellscript; charset=utf-8" no-cache
put "$OUT/install.sh.minisig" text/plain no-cache
# no-cache, not a short max-age: a file and its signature cached separately
# could pair an old latest.json with the new signature, which fails to verify.
put "$OUT/latest.json.minisig" text/plain no-cache
put "$OUT/latest.json" application/json no-cache

# 6. Read it back from the public URL, as a server will.
curl -fsS -o "$OUT/check.json" "$RELEASE_URL/latest.json"
curl -fsS -o "$OUT/check.json.minisig" "$RELEASE_URL/latest.json.minisig"
minisign -V -q -p "$PUBKEY" -m "$OUT/check.json" -x "$OUT/check.json.minisig" ||
	die "$RELEASE_URL/latest.json doesn't verify against $PUBKEY"
log "Published $version: $RELEASE_URL/latest.json"
