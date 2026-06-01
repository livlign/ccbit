#!/bin/bash
# ccbit status line. Stateless: reads stdin JSON + per-session state files, prints 2-3 lines.
# Always exits 0 and always prints something (empty output blanks the CC status line).

INPUT=$(cat)
NOW=$(date +%s)
STALL=${CCBIT_STALL:-180}
NARROW_AT=${CCBIT_NARROW:-60}

SID=$(jq -r '.session_id // empty' <<<"$INPUT" 2>/dev/null)
MODEL=$(jq -r '.model.display_name // "?"' <<<"$INPUT" 2>/dev/null)
CDIR=$(jq -r '.workspace.current_dir // .cwd // empty' <<<"$INPUT" 2>/dev/null)
CTX=$(jq -r '.context_window.used_percentage // empty' <<<"$INPUT" 2>/dev/null)
FH=$(jq -r '.rate_limits.five_hour.used_percentage // empty' <<<"$INPUT" 2>/dev/null)
FHR=$(jq -r '.rate_limits.five_hour.resets_at // empty' <<<"$INPUT" 2>/dev/null)
SD=$(jq -r '.rate_limits.seven_day.used_percentage // empty' <<<"$INPUT" 2>/dev/null)
SDR=$(jq -r '.rate_limits.seven_day.resets_at // empty' <<<"$INPUT" 2>/dev/null)

DIR="$HOME/.claude/sessions/$SID"
LOG="$DIR/log"

# ---- helpers ----------------------------------------------------------------
fmt_dur() {  # seconds -> 45s | 2m14s | 1h05m
  local s=$1
  [ "$s" -lt 0 ] 2>/dev/null && s=0
  if [ "$s" -lt 60 ]; then printf '%ds' "$s"
  elif [ "$s" -lt 3600 ]; then printf '%dm%02ds' $((s / 60)) $((s % 60))
  else printf '%dh%02dm' $((s / 3600)) $(((s % 3600) / 60)); fi
}

fmt_reset() {  # seconds remaining -> 12m | 1h12m | 2d3h
  local s=$1
  [ "$s" -lt 0 ] 2>/dev/null && s=0
  if [ "$s" -lt 3600 ]; then printf '%dm' $((s / 60))
  elif [ "$s" -lt 86400 ]; then printf '%dh%02dm' $((s / 3600)) $(((s % 3600) / 60))
  else printf '%dd%dh' $((s / 86400)) $(((s % 86400) / 3600)); fi
}

nfiles() {  # 1 -> "1 file", N -> "N files"
  [ "$1" = 1 ] && printf '1 file' || printf '%s files' "$1"
}

disp_path() {  # full home-relative path if short; else last 2 segments; else basename
  local p=$1 max=${CCBIT_PATHMAX:-28}
  { [ -z "$p" ] || [ "$p" = "?" ]; } && { printf '%s' "${p:-?}"; return; }
  [ ${#p} -le "$max" ] && { printf '%s' "$p"; return; }
  local IFS=/
  local arr=($p)
  local n=${#arr[@]}
  if [ "$n" -ge 2 ]; then
    local two="${arr[n-2]}/${arr[n-1]}"
    [ ${#two} -le "$max" ] && { printf '%s' "$two"; return; }
    printf '%s' "${arr[n-1]}"   # basename
    return
  fi
  printf '%s' "$p"
}

parse_epoch() {  # epoch passthrough or ISO-8601 -> epoch ("" on failure)
  local v=$1
  [ -z "$v" ] && { echo ""; return; }
  [ "$v" = null ] && { echo ""; return; }
  case "$v" in
    *[!0-9]*) TZ=UTC date -j -f "%Y-%m-%dT%H:%M:%S" "${v:0:19}" +%s 2>/dev/null || echo "" ;;
    *) echo "$v" ;;
  esac
}

# ---- read state -------------------------------------------------------------
state=idle; ts=0; idx=0; la=0
[ -f "$DIR/turn" ] && read -r state ts idx la < "$DIR/turn" 2>/dev/null
[[ $ts  =~ ^[0-9]+$ ]] || ts=0
[[ $idx =~ ^[0-9]+$ ]] || idx=0
[[ $la  =~ ^[0-9]+$ ]] || la=0

bkind=""; bexit=""; bproj=""
if [ -s "$DIR/build" ]; then read -r bkind bexit _bepoch bproj < "$DIR/build" 2>/dev/null; fi
[[ $bexit =~ ^-?[0-9]+$ ]] || bexit=""

spawned=0; done=0
[ -s "$DIR/agents" ] && read -r spawned done < "$DIR/agents" 2>/dev/null
[[ $spawned =~ ^[0-9]+$ ]] || spawned=0
[[ $done    =~ ^[0-9]+$ ]] || done=0
running=$((spawned - done)); [ "$running" -lt 0 ] && running=0

waiting=0; [ -f "$DIR/wait" ] && waiting=1

# ---- per-turn build/test + file summary (used by derivation and the done line)
turn_build=$(awk -v t="$idx" '$1==t && $3=="build"{e=$4} END{if(e!="")print e}' "$LOG" 2>/dev/null)
turn_test=$(awk  -v t="$idx" '$1==t && $3=="test"{e=$4}  END{if(e!="")print e}' "$LOG" 2>/dev/null)
total_files=0
if [ -f "$DIR/files" ]; then
  while IFS=$'\t' read -r p c _rest; do
    [ -z "$p" ] && continue
    [[ $c =~ ^[0-9]+$ ]] && total_files=$((total_files + c))
  done < "$DIR/files"
fi

# ---- derive state (priority order, first match wins) ------------------------
DSTATE=idle
if [ "$state" = working ] && [ "$waiting" -eq 0 ] && [ $((NOW - la)) -gt "$STALL" ]; then
  DSTATE=stopped
elif [ -n "$bexit" ] && [ "$bexit" -ne 0 ]; then
  DSTATE=failed
elif [ "$waiting" -eq 1 ]; then
  DSTATE=waiting
elif [ "$running" -gt 0 ]; then
  DSTATE=agents
elif [ "$state" = working ]; then
  DSTATE=working
elif [ "$state" = idle ] && [ -n "$bexit" ] && [ "$bexit" -eq 0 ]; then
  # redeemed = within this turn, latest build/test passed but the prior result failed
  prev=""; last=""
  while read -r e; do prev=$last; last=$e; done < <(awk -v t="$idx" '$1==t && ($3=="build"||$3=="test"){print $4}' "$LOG" 2>/dev/null)
  if [ "$last" = 0 ] && [ -n "$prev" ] && [ "$prev" != 0 ]; then DSTATE=redeemed; else DSTATE=done; fi
elif [ "$state" = idle ] && [ "$total_files" -gt 0 ]; then
  DSTATE=done   # turn ended having edited files but ran no build/test — still show the recap
fi

result_segs() {  # appends build/test result segments to $1-style echo
  local out=""
  [ -n "$turn_build" ] && { [ "$turn_build" = 0 ] && out="$out · build succeeded" || out="$out · build failed"; }
  [ -n "$turn_test" ]  && { [ "$turn_test"  = 0 ] && out="$out · tests succeeded" || out="$out · tests failed"; }
  printf '%s' "$out"
}

# ---- face (with wall-clock motion + width fallback) -------------------------
NARROW=0
[[ $COLUMNS =~ ^[0-9]+$ ]] && [ "$COLUMNS" -lt "$NARROW_AT" ] && NARROW=1
frame=$(( (NOW / 2) % 2 ))   # 2s wall-clock swap, stateless

case $DSTATE in
  idle)     face='(•_•)';;
  working)  [ $frame -eq 0 ] && face='(◣_◢)' || face='(◢_◣)';;
  agents)   if [ "$NARROW" -eq 1 ]; then [ $frame -eq 0 ] && face='(•_•)>' || face='<(•_•)';
            else [ $frame -eq 0 ] && face='┏(•_•)┛' || face='┗(•_•)┓'; fi;;
  done)     [ "$NARROW" -eq 1 ] && face='(•‿•)' || face='(つ•‿•)つ';;
  redeemed) face='(→_←")';;
  waiting)  face='(◕_◕)?';;
  failed)   [ "$NARROW" -eq 1 ] && face='(>_<) FAILED' || face='(╯°□°)╯︵ ┻━┻';;
  stopped)  face='(¬°-°)¬';;
esac

# ---- line 1 text ------------------------------------------------------------
case $DSTATE in
  idle)
    text='idle';;
  working)
    if [ -s "$DIR/files" ]; then
      seg=""
      while IFS=$'\t' read -r p c _rest; do
        [ -z "$p" ] && continue
        s="$p · $(nfiles "$c")"
        [ -z "$seg" ] && seg="$s" || seg="$seg · $s"
      done < "$DIR/files"
      text="editing $seg · $(fmt_dur $((NOW - ts)))"
    else
      text="working · $(fmt_dur $((NOW - ts)))"
    fi;;
  agents)
    text="agents: $spawned spawned · $done done · $running running · $(fmt_dur $((NOW - ts)))";;
  waiting)
    text='waiting on you';;
  failed)
    [ "$bkind" = test ] && lbl=tests || lbl=build
    text="${bproj:-build} $lbl failed";;
  done)
    text="edited $(nfiles "$total_files")$(result_segs)";;
  redeemed)
    text="edited $(nfiles "$total_files")$(result_segs) (recovered)";;
  stopped)
    ago=$((NOW - la))
    [ "$ago" -lt 60 ] && agotxt="${ago}s ago" || agotxt="$((ago / 60))m ago"
    lastdesc=$(cat "$DIR/last" 2>/dev/null)
    text="stopped · last: ${lastdesc:-unknown} · $agotxt";;
esac

LINE1="$face $text"

# ---- catch-up tail (third line; only when >1 completed turn since presence) -
SEEN=0; [ -s "$DIR/seen" ] && read -r SEEN < "$DIR/seen" 2>/dev/null
[[ $SEEN =~ ^[0-9]+$ ]] || SEEN=0
TAIL=""
if [ -f "$LOG" ]; then
  comp=$(awk -v s="$SEEN" '$3=="turn_end" && $1>s{print $1}' "$LOG" 2>/dev/null | sort -n | uniq)
  cnt=$(printf '%s\n' "$comp" | grep -c .)
  if [ "$cnt" -gt 1 ]; then
    turns=($comp)
    n=${#turns[@]}
    tsum=()
    for ((i = 0; i < n; i++)); do
      t=${turns[i]}
      fc=$(awk -v t="$t" '$1==t && $3=="turn_end"{for(j=4;j<=NF;j++) if($j ~ /^files=/){split($j,a,"=");print a[2]}}' "$LOG" 2>/dev/null | head -1)
      tb=$(awk -v t="$t" '$1==t && $3=="build"{e=$4} END{if(e!="")print e}' "$LOG" 2>/dev/null)
      tt=$(awk -v t="$t" '$1==t && $3=="test"{e=$4}  END{if(e!="")print e}' "$LOG" 2>/dev/null)
      ag=$(awk -v t="$t" '$1==t && $3=="agent_done"{c++} END{print c+0}' "$LOG" 2>/dev/null)
      segs=""
      [ -n "$tb" ] && { [ "$tb" = 0 ] && segs="${segs:+$segs, }build succeeded" || segs="${segs:+$segs, }build failed"; }
      [ -n "$tt" ] && { [ "$tt" = 0 ] && segs="${segs:+$segs, }tests succeeded" || segs="${segs:+$segs, }tests failed"; }
      { [[ $fc =~ ^[0-9]+$ ]] && [ "$fc" -gt 0 ]; } && segs="${segs:+$segs, }$(nfiles "$fc")"
      [ "$ag" -gt 0 ] 2>/dev/null && segs="${segs:+$segs, }$ag agents"
      [ -z "$segs" ] && segs="done"
      tsum[i]="turn $t: $segs"
    done

    # Assemble from turn index $1; turns before it collapse to "+N earlier turns".
    assemble() {
      local st=$1 out="while away —" i
      [ "$st" -gt 0 ] && out="$out +$st earlier turns ·"
      for ((i = st; i < n; i++)); do out="$out ${tsum[i]} ·"; done
      printf '%s' "${out% ·}"
    }

    start=0
    [ "$n" -gt 5 ] && start=$((n - 5))   # hard cap: max 5 turn groups
    TAIL=$(assemble "$start")
    # Width-permitting (§5.3): collapse more oldest turns until the line fits COLUMNS.
    if [[ $COLUMNS =~ ^[0-9]+$ ]]; then
      while [ "${#TAIL}" -gt "$COLUMNS" ] && [ "$start" -lt $((n - 1)) ]; do
        start=$((start + 1))
        TAIL=$(assemble "$start")
      done
    fi
  fi
fi

# ---- line 2 ambient ---------------------------------------------------------
case "$CDIR" in
  "$HOME")   DSP='~';;
  "$HOME"/*) DSP="~${CDIR#$HOME}";;
  '')        DSP='?';;
  *)         DSP="$CDIR";;
esac
DSP=$(disp_path "$DSP")

if [ -z "$CTX" ] || [ "$CTX" = null ]; then
  CTXSEG='ctx --'
else
  ci=${CTX%.*}; [[ $ci =~ ^[0-9]+$ ]] || ci=0
  CTXSEG="ctx ${ci}%"
fi

AMB="$DSP · $MODEL · $CTXSEG"
if [ -n "$FH" ] && [ "$FH" != null ]; then
  fhi=${FH%.*}; seg="5h ${fhi}%"
  e=$(parse_epoch "$FHR"); [ -n "$e" ] && seg="$seg ($(fmt_reset $((e - NOW))))"
  AMB="$AMB · $seg"
fi
if [ -n "$SD" ] && [ "$SD" != null ]; then
  sdi=${SD%.*}; seg="7d ${sdi}%"
  e=$(parse_epoch "$SDR"); [ -n "$e" ] && seg="$seg ($(fmt_reset $((e - NOW))))"
  AMB="$AMB · $seg"
fi

# ---- print ------------------------------------------------------------------
printf '%s\n' "$LINE1"
[ -n "$TAIL" ] && printf '%s\n' "$TAIL"
printf '%b%s%b\n' '\033[2m' "$AMB" '\033[0m'   # ambient line: dim / low-saturation
exit 0
