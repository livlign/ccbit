#!/bin/bash
# SessionEnd: wipe the whole per-session directory.
INPUT=$(cat)
SID=$(jq -r '.session_id // empty' <<<"$INPUT")
[ -z "$SID" ] && exit 0
DIR="$HOME/.claude/sessions/$SID"
case "$DIR" in
  "$HOME/.claude/sessions/"?*) rm -rf "$DIR";;
esac
exit 0
