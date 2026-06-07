#!/bin/bash
# ccbit installer. Fetches the prebuilt binary for this platform from the latest
# GitHub release (no Go required), falling back to building from source when Go
# is available. Merges the statusLine into ~/.claude/settings.json (backed up
# first, never overwritten wholesale) and removes config left by the legacy
# hooks-based installer.
set -e

REPO="livlign/ccbit"
DEST_DIR="$HOME/.claude/ccbit"
DEST="$DEST_DIR/ccbit"
SETTINGS="$HOME/.claude/settings.json"

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
  x86_64|amd64) ARCH=amd64 ;;
  arm64|aarch64) ARCH=arm64 ;;
  *) ARCH="" ;;
esac
case "$OS" in
  darwin|linux) ;;
  *) OS="" ;;
esac

ASSET="ccbit_${OS}_${ARCH}.tar.gz"
URL="https://github.com/$REPO/releases/latest/download/$ASSET"

fetch_release() {
  [ -n "$OS" ] && [ -n "$ARCH" ] || return 1
  command -v curl >/dev/null 2>&1 || return 1
  local tmp
  tmp=$(mktemp -d) || return 1
  # shellcheck disable=SC2064
  trap "rm -rf '$tmp'" RETURN 2>/dev/null || true
  echo "ccbit: downloading $ASSET from the latest release"
  curl -fsSL "$URL" -o "$tmp/$ASSET" || { rm -rf "$tmp"; return 1; }
  tar -xzf "$tmp/$ASSET" -C "$tmp" ccbit || { rm -rf "$tmp"; return 1; }
  mkdir -p "$DEST_DIR"
  install -m 0755 "$tmp/ccbit" "$DEST"
  rm -rf "$tmp"
}

build_from_source() {
  command -v go >/dev/null 2>&1 || return 1
  local src
  src="$(cd "$(dirname "${BASH_SOURCE[0]:-.}")" && pwd)"
  [ -f "$src/go.mod" ] || return 1 # piped through curl: no source tree here
  echo "ccbit: no prebuilt binary for ${OS:-$(uname -s)}/${ARCH:-$(uname -m)}; building from source"
  mkdir -p "$DEST_DIR"
  (cd "$src" && go build -o "$DEST" ./cmd/ccbit)
}

fetch_release || build_from_source || {
  echo "ccbit: could not install a binary." >&2
  echo "  - no release asset for ${OS:-$(uname -s)}/${ARCH:-$(uname -m)} (or curl is missing), and" >&2
  echo "  - no Go toolchain (or no source tree) to build from." >&2
  echo "Install Go 1.26+ and run: go build -o $DEST ./cmd/ccbit (from a clone)," >&2
  echo "or download an asset manually: https://github.com/$REPO/releases/latest" >&2
  exit 1
}
echo "ccbit: installed $DEST"

STATUSLINE='{"type":"command","command":"~/.claude/ccbit/ccbit","refreshInterval":1,"padding":1}'

if ! command -v jq >/dev/null 2>&1; then
  echo "ccbit: jq not found — add this to $SETTINGS yourself:"
  echo "{
  \"statusLine\": $STATUSLINE
}"
  exit 0
fi

if [ -f "$SETTINGS" ]; then
  BAK="$SETTINGS.bak.$(date +%Y%m%d%H%M%S)"
  cp "$SETTINGS" "$BAK"
  echo "ccbit: backed up settings.json -> $BAK"
  if jq -e '.statusLine and (.statusLine.command | test("ccbit/(ccbit|statusline.sh)") | not)' "$SETTINGS" >/dev/null 2>&1; then
    echo "ccbit: WARNING — replacing an existing statusLine config (only one is allowed). Old value is in the backup."
  fi
else
  mkdir -p "$(dirname "$SETTINGS")"
  echo '{}' > "$SETTINGS"
  echo "ccbit: created new settings.json"
fi

# Set the statusLine; drop hook entries left by the legacy shell installer
# (the Go binary needs none).
TMP="$SETTINGS.tmp.$$"
jq --argjson sl "$STATUSLINE" '
  def dropccbit:
    map(.hooks |= map(select(((.command // "") | test("ccbit/hooks/")) | not)))
    | map(select(.hooks != []));
  .statusLine = $sl
  | (if .hooks then
       .hooks |= (with_entries(.value |= dropccbit) | with_entries(select(.value != [])))
       | (if .hooks == {} then del(.hooks) else . end)
     else . end)
' "$SETTINGS" > "$TMP"
mv "$TMP" "$SETTINGS"

echo "ccbit: statusLine configured in $SETTINGS"
echo "ccbit: done. Open a new Claude Code session to see Bit."
