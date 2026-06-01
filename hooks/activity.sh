#!/bin/bash
# PreToolUse (all tools): refresh last_activity so any tool call keeps the session "alive"
# and prevents the stall detector from false-firing during read-heavy or long turns.
INPUT=$(cat)
SID=$(jq -r '.session_id // empty' <<<"$INPUT")
[ -z "$SID" ] && exit 0
DIR="$HOME/.claude/sessions/$SID"
[ -d "$DIR" ] || exit 0
NOW=$(date +%s)
if [ -f "$DIR/turn" ]; then
  read -r _st _ts _idx _la < "$DIR/turn"
  printf '%s %s %s %s\n' "$_st" "$_ts" "$_idx" "$NOW" > "$DIR/turn"
fi
exit 0
