#!/bin/bash
# ccbit installer. Copies scripts to ~/.claude/ccbit and MERGES (never overwrites) the
# statusLine + hooks config into ~/.claude/settings.json. Backs settings.json up first.
set -e

SRC="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEST="$HOME/.claude/ccbit"
SETTINGS="$HOME/.claude/settings.json"

command -v jq >/dev/null 2>&1 || { echo "ccbit: jq is required but not found in PATH." >&2; exit 1; }

echo "ccbit: installing scripts to $DEST"
mkdir -p "$DEST/hooks"
mkdir -p "$HOME/.claude/sessions"
cp "$SRC/statusline.sh" "$DEST/statusline.sh"
cp "$SRC"/hooks/*.sh "$DEST/hooks/"
[ -f "$SRC/error-signatures" ] && cp "$SRC/error-signatures" "$DEST/error-signatures"
chmod +x "$DEST/statusline.sh" "$DEST"/hooks/*.sh

STATUSLINE='{"type":"command","command":"~/.claude/ccbit/statusline.sh","refreshInterval":1,"padding":1}'
HOOKS='{
  "UserPromptSubmit": [{"hooks":[{"type":"command","command":"~/.claude/ccbit/hooks/turn-start.sh"}]}],
  "PostToolUse": [
    {"matcher":"Edit|Write|MultiEdit","hooks":[{"type":"command","command":"~/.claude/ccbit/hooks/files.sh"}]},
    {"matcher":"Bash","hooks":[{"type":"command","command":"~/.claude/ccbit/hooks/bash.sh"}]},
    {"matcher":"Task","hooks":[{"type":"command","command":"~/.claude/ccbit/hooks/agent-spawn.sh"}]},
    {"matcher":"AskUserQuestion|ExitPlanMode","hooks":[{"type":"command","command":"~/.claude/ccbit/hooks/wait.sh"}]}
  ],
  "PreToolUse": [
    {"hooks":[{"type":"command","command":"~/.claude/ccbit/hooks/activity.sh"}]},
    {"matcher":"AskUserQuestion|ExitPlanMode","hooks":[{"type":"command","command":"~/.claude/ccbit/hooks/wait.sh"}]}
  ],
  "SubagentStop": [{"hooks":[{"type":"command","command":"~/.claude/ccbit/hooks/agent-done.sh"}]}],
  "Stop": [{"hooks":[{"type":"command","command":"~/.claude/ccbit/hooks/turn-end.sh"}]}],
  "SessionEnd": [{"hooks":[{"type":"command","command":"~/.claude/ccbit/hooks/cleanup.sh"}]}]
}'

if [ -f "$SETTINGS" ]; then
  BAK="$SETTINGS.bak.$(date +%Y%m%d%H%M%S)"
  cp "$SETTINGS" "$BAK"
  echo "ccbit: backed up settings.json -> $BAK"
  if jq -e '.statusLine and (.statusLine.command | test("ccbit/statusline.sh") | not)' "$SETTINGS" >/dev/null 2>&1; then
    echo "ccbit: WARNING — replacing an existing statusLine config (only one is allowed). Old value is in the backup."
  fi
else
  mkdir -p "$(dirname "$SETTINGS")"
  echo '{}' > "$SETTINGS"
  echo "ccbit: created new settings.json"
fi

# Merge: set statusLine; for each event, drop any prior copies of our entries, then append ours.
TMP="$SETTINGS.tmp.$$"
jq \
  --argjson sl "$STATUSLINE" \
  --argjson nh "$HOOKS" '
  .statusLine = $sl
  | .hooks = (.hooks // {})
  | reduce ($nh | keys[]) as $ev (.;
      .hooks[$ev] = (
        ((.hooks[$ev] // [])
          | map(select(. as $e | ([ $nh[$ev][] | tojson ] | index($e | tojson)) == null)))
        + $nh[$ev]
      )
    )
' "$SETTINGS" > "$TMP"

mv "$TMP" "$SETTINGS"
echo "ccbit: merged statusLine + hooks into $SETTINGS"
echo "ccbit: done. Open a new Claude Code session to see Bit."
