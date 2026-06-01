#!/bin/bash
# SubagentStop: a subagent finished -> increment done.
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
DONE=$((DONE + 1))
printf '%s %s\n' "$SPAWN" "$DONE" > "$DIR/agents"

IDX=0
[ -f "$DIR/turn" ] && read -r _st _ts IDX _la < "$DIR/turn"
[[ $IDX =~ ^[0-9]+$ ]] || IDX=0
printf '%s %s agent_done\n' "$IDX" "$NOW" >> "$DIR/log"
printf 'agent finished\n' > "$DIR/last"

if [ -f "$DIR/turn" ]; then
  read -r _st _ts _idx _la < "$DIR/turn"
  printf '%s %s %s %s\n' "$_st" "$_ts" "$_idx" "$NOW" > "$DIR/turn"
fi
exit 0
