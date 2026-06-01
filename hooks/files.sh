#!/bin/bash
# PostToolUse Edit|Write|MultiEdit: record distinct edited files per project (current turn).
INPUT=$(cat)
SID=$(jq -r '.session_id // empty' <<<"$INPUT")
[ -z "$SID" ] && exit 0
DIR="$HOME/.claude/sessions/$SID"
mkdir -p "$DIR" 2>/dev/null
NOW=$(date +%s)

FILE=$(jq -r '.tool_input.file_path // empty' <<<"$INPUT")
CWD=$(jq -r '.cwd // empty' <<<"$INPUT")
[ -z "$FILE" ] && exit 0
case "$FILE" in /*) ;; *) FILE="$CWD/$FILE";; esac

ROOT=$(git -C "$(dirname "$FILE")" rev-parse --show-toplevel 2>/dev/null)
[ -z "$ROOT" ] && ROOT="$CWD"
REL="${FILE#$ROOT/}"
case "$REL" in
  */*) PROJECT="${REL%%/*}";;
  *)   PROJECT=$(basename "$ROOT");;
esac
[ -z "$PROJECT" ] && PROJECT=$(basename "$CWD")
[ -z "$PROJECT" ] && PROJECT="."

TMP="$DIR/files.tmp.$$"
: > "$TMP"
FOUND=0
if [ -f "$DIR/files" ]; then
  while IFS=$'\t' read -r p c paths; do
    [ -z "$p" ] && continue
    if [ "$p" = "$PROJECT" ]; then
      FOUND=1
      case "|$paths|" in
        *"|$FILE|"*) ;;  # already counted this path
        *) [ -z "$paths" ] && paths="$FILE" || paths="$paths|$FILE"; c=$((c + 1));;
      esac
    fi
    printf '%s\t%s\t%s\n' "$p" "$c" "$paths" >> "$TMP"
  done < "$DIR/files"
fi
[ "$FOUND" -eq 0 ] && printf '%s\t%s\t%s\n' "$PROJECT" 1 "$FILE" >> "$TMP"
mv "$TMP" "$DIR/files"

printf 'editing %s\n' "$(basename "$FILE")" > "$DIR/last"
rm -f "$DIR/wait"

if [ -f "$DIR/turn" ]; then
  read -r _st _ts _idx _la < "$DIR/turn"
  printf '%s %s %s %s\n' "$_st" "$_ts" "$_idx" "$NOW" > "$DIR/turn"
fi
exit 0
