#!/bin/bash
# AskUserQuestion|ExitPlanMode: PreToolUse sets the "waiting on you" marker; PostToolUse clears it.
INPUT=$(cat)
SID=$(jq -r '.session_id // empty' <<<"$INPUT")
[ -z "$SID" ] && exit 0
DIR="$HOME/.claude/sessions/$SID"
mkdir -p "$DIR" 2>/dev/null
NOW=$(date +%s)

EVENT=$(jq -r '.hook_event_name // empty' <<<"$INPUT")
TOOL=$(jq -r '.tool_name // empty' <<<"$INPUT")

if [ "$EVENT" = "PreToolUse" ]; then
  printf '%s %s\n' "${TOOL:-wait}" "$NOW" > "$DIR/wait"
  IDX=0
  [ -f "$DIR/turn" ] && read -r _ _ IDX _ < "$DIR/turn"
  [[ $IDX =~ ^[0-9]+$ ]] || IDX=0
  printf '%s %s wait %s\n' "$IDX" "$NOW" "${TOOL:-wait}" >> "$DIR/log"
else
  rm -f "$DIR/wait"
fi
exit 0
