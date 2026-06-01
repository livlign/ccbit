#!/bin/bash
# UserPromptSubmit: begin a new turn. Resets per-turn state, advances presence marker.
INPUT=$(cat)
SID=$(jq -r '.session_id // empty' <<<"$INPUT")
[ -z "$SID" ] && exit 0
DIR="$HOME/.claude/sessions/$SID"
mkdir -p "$DIR" 2>/dev/null
NOW=$(date +%s)

IDX=0
[ -f "$DIR/turn" ] && read -r _ _ IDX _ < "$DIR/turn"
[[ $IDX =~ ^[0-9]+$ ]] || IDX=0
IDX=$((IDX + 1))

printf '%s %s %s %s\n' working "$NOW" "$IDX" "$NOW" > "$DIR/turn"
: > "$DIR/files"
: > "$DIR/build"
printf '0 0\n' > "$DIR/agents"
rm -f "$DIR/wait" "$DIR/last"

# Presence: starting turn N means turns 1..N-1 have been seen by the user.
printf '%s\n' "$((IDX - 1))" > "$DIR/seen"

printf '%s %s turn_start\n' "$IDX" "$NOW" >> "$DIR/log"
exit 0
