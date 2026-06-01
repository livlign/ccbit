#!/bin/bash
# PostToolUse Bash: detect build/test commands, record exit-code result (tier 1).
INPUT=$(cat)
SID=$(jq -r '.session_id // empty' <<<"$INPUT")
[ -z "$SID" ] && exit 0
DIR="$HOME/.claude/sessions/$SID"
mkdir -p "$DIR" 2>/dev/null
NOW=$(date +%s)

CMD=$(jq -r '.tool_input.command // empty' <<<"$INPUT")
EXIT=$(jq -r '.tool_response.exit_code // empty' <<<"$INPUT")
CWD=$(jq -r '.cwd // empty' <<<"$INPUT")
[ -z "$EXIT" ] && EXIT=0

rm -f "$DIR/wait"
_idx=0
if [ -f "$DIR/turn" ]; then
  read -r _st _ts _idx _la < "$DIR/turn"
  printf '%s %s %s %s\n' "$_st" "$_ts" "$_idx" "$NOW" > "$DIR/turn"
fi
[[ $_idx =~ ^[0-9]+$ ]] || _idx=0

LC=$(printf '%s' "$CMD" | tr '[:upper:]' '[:lower:]')
KIND=""
case "$LC" in
  *"dotnet test"*|*"go test"*|*"cargo test"*|*pytest*|*jest*|*vitest*|*"npm test"*|*"yarn test"*|*"pnpm test"*|*" test "*|*" test") KIND=test;;
  *build*|*compile*|*tsc*|*"make "*|make|*gradle*|*"mvn "*) KIND=build;;
esac
[ -z "$KIND" ] && exit 0

ROOT=$(git -C "$CWD" rev-parse --show-toplevel 2>/dev/null)
[ -z "$ROOT" ] && ROOT="$CWD"
PROJECT=$(basename "$ROOT")
[ -z "$PROJECT" ] && PROJECT="."

printf '%s %s %s %s\n' "$KIND" "$EXIT" "$NOW" "$PROJECT" > "$DIR/build"
printf '%s %s %s %s %s\n' "$_idx" "$NOW" "$KIND" "$EXIT" "$PROJECT" >> "$DIR/log"
printf 'running %s\n' "$KIND" > "$DIR/last"
exit 0
