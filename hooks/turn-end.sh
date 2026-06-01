#!/bin/bash
# Stop: end the turn. Mark idle, log a turn_end summary (file count) for the catch-up tail.
INPUT=$(cat)
SID=$(jq -r '.session_id // empty' <<<"$INPUT")
[ -z "$SID" ] && exit 0
DIR="$HOME/.claude/sessions/$SID"
mkdir -p "$DIR" 2>/dev/null
NOW=$(date +%s)

ST=idle TS=0 IDX=0 LA=0
[ -f "$DIR/turn" ] && read -r ST TS IDX LA < "$DIR/turn"
[[ $IDX =~ ^[0-9]+$ ]] || IDX=0
[[ $TS  =~ ^[0-9]+$ ]] || TS=0
printf '%s %s %s %s\n' idle "$TS" "$IDX" "$NOW" > "$DIR/turn"
rm -f "$DIR/wait"

FC=0
if [ -f "$DIR/files" ]; then
  while IFS=$'\t' read -r p c rest; do
    [ -z "$p" ] && continue
    [[ $c =~ ^[0-9]+$ ]] && FC=$((FC + c))
  done < "$DIR/files"
fi
printf '%s %s turn_end files=%s\n' "$IDX" "$NOW" "$FC" >> "$DIR/log"
exit 0
