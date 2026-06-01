#!/bin/bash
# PostToolUse Task: each Task call spawns one subagent -> increment spawned by 1.
INPUT=$(cat)
SID=$(jq -r '.session_id // empty' <<<"$INPUT")
[ -z "$SID" ] && exit 0
DIR="$HOME/.claude/sessions/$SID"
mkdir -p "$DIR" 2>/dev/null
NOW=$(date +%s)

SPAWN=0 DONE=0
[ -s "$DIR/agents" ] && read -r SPAWN DONE < "$DIR/agents"
[[ $SPAWN =~ ^[0-9]+$ ]] || SPAWN=0
[[ $DONE  =~ ^[0-9]+$ ]] || DONE=0
SPAWN=$((SPAWN + 1))
printf '%s %s\n' "$SPAWN" "$DONE" > "$DIR/agents"

printf 'spawning agent\n' > "$DIR/last"
rm -f "$DIR/wait"

if [ -f "$DIR/turn" ]; then
  read -r _st _ts _idx _la < "$DIR/turn"
  printf '%s %s %s %s\n' "$_st" "$_ts" "$_idx" "$NOW" > "$DIR/turn"
fi
exit 0
