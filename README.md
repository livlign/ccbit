# ccbit

A session-awareness layer for [Claude Code](https://claude.com/claude-code), led by a stateful kaomoji face named **Bit**. Hooks observe session state as events fire, write it to per-session files, and a custom status line renders it — Bit's expression maps to what the session is doing right now.

ccbit answers one question at a glance when you return to a session: *is anything working, done, waiting, broken, or stopped here — and what happened while I was away?*

```
(◣_◢) editing lhproduct · 4 files · lhecommservice · 2 files · 2m14s
~/lhproduct · Opus · ctx 38% · 5h 23% (1h12m) · 7d 41% (2d3h)   ← dim, low-saturation
```

## Install

Requires `jq` and Claude Code ≥ v2.1.153 (for `COLUMNS`-based width fallback).

```sh
./install.sh
```

The installer:
- copies `statusline.sh` and `hooks/` into `~/.claude/ccbit/`,
- creates the state root `~/.claude/sessions/`,
- **merges** the status line + hooks into `~/.claude/settings.json` (backing it up first — existing hooks and configs are preserved; re-running is idempotent).

There is one caveat: a settings file may only define **one** `statusLine`. If you already have a custom one, the installer warns and replaces it (your old value is in the timestamped `.bak`).

Open a new Claude Code session to see Bit. No persistent state survives a session — `SessionEnd` wipes `~/.claude/sessions/<id>/`.

## States & faces

Eight states, one fixed face each, derived in priority order (first match wins):

| Priority | State | Face | When |
|---|---|---|---|
| 1 | Stopped | `(¬°-°)¬` | working but no activity for >45s (and not waiting on you) |
| 2 | Failed | `(╯°□°)╯︵ ┻━┻` | latest build/test exited non-zero |
| 3 | Waiting on you | `(◕_◕)?` | a question or plan is awaiting your answer |
| 4 | Agents running | `┏(•_•)┛ ⇄ ┗(•_•)┓` | subagents in flight (spawned − done > 0) |
| 5 | Working | `(◣_◢) ⇄ (◢_◣)` | a turn is in progress |
| 6 | Done (redeemed) | `(→_←")` | idle, latest build passed after a failure this turn |
| 7 | Done (normal) | `(つ•‿•)つ` | idle and the turn did real work — a build passed, or files were edited |
| 8 | Idle | `(•_•)` | nothing else applies (e.g. a turn that only read or answered) |

> The "Done" recap persists until your next prompt, so a finished turn always leaves a one-line summary of what it did (`edited N files · build succeeded`) rather than snapping back to a blank `idle`. A turn that edited nothing and ran no build/test stays `idle`.

Results are spelled out in words (`build succeeded` / `build failed`, `tests succeeded` / `tests failed`) rather than `✓`/`✗` glyphs, and the agent tally reads `5 spawned · 3 done · 2 running`. Line 2 is rendered dim (low-saturation) with no accent color. The current directory shows in full when short, collapsing to the last two segments, then the basename, as it gets longer (tune the cutoff with `CCBIT_PATHMAX`).

Motion exists only in Working and Agents-running — a 2-frame swap on a ~2s wall-clock cycle (`frame = (epoch/2) % 2`), stateless and time-derived so concurrent repaints never jitter. The felt liveness during a long turn comes from the **numbers** ticking (file count, elapsed clock), not the face.

**Width safety:** when `COLUMNS < 60`, width-risky faces fall back to ASCII-safe forms (`(つ•‿•)つ → (•‿•)`, the table flip → `(>_<) FAILED`, the agent arms → `(•_•)> ⇄ <(•_•)`). If `COLUMNS` is unset (older CC), ccbit assumes wide and never crashes.

### Catch-up tail

When you return after **more than one** turn has completed unattended (e.g. auto-pilot ran several turns), a third line summarizes them:

```
while away — turn 2: 5 agents · turn 3: tests failed · turn 4: 2 files
```

Capped at 5 turn groups (older ones collapse to `+N earlier turns`) and aggregated per turn. It clears on your next prompt. It's a glance/index — full results live in the transcript.

## Configuration

Environment variables (set them in your shell or Claude Code env):

| Var | Default | Meaning |
|---|---|---|
| `CCBIT_STALL` | `180` | seconds of inactivity before a working session reads as Stopped |
| `CCBIT_NARROW` | `60` | `COLUMNS` below this triggers ASCII-safe faces |
| `CCBIT_PATHMAX` | `28` | cwd char length above which the path collapses to fewer segments |

## How it works

```
Claude Code events ──► hooks (sensors) ──► per-session state files ──► status line (renderer)
                                                  └──► append-only log (catch-up)
```

Hooks only write; the renderer only reads. State lives under `~/.claude/sessions/<session_id>/`:

| File | Written by | Holds |
|---|---|---|
| `turn` | turn-start, turn-end, every PostToolUse | `state turn_start_epoch turn_index last_activity_epoch` |
| `files` | files.sh | distinct edited paths per project, this turn |
| `build` | bash.sh | latest build/test `kind exit epoch project` |
| `agents` | agent-spawn.sh, agent-done.sh | `spawned done` |
| `log` | several | append-only, turn-grouped event history |
| `wait` / `last` / `seen` | wait.sh / sensors / turn-start | waiting marker / last action / presence baseline |

### Two implementation decisions worth flagging

The PRD left two points open; this build resolves them as follows:

1. **Agent spawn counting** (PRD §7.4, the preferred option). Each `Task` tool call spawns exactly one subagent, so a `PostToolUse: Task` hook (`agent-spawn.sh`) increments `spawned` by 1 per call; `SubagentStop` increments `done`. Running = `spawned − done`, clamped at 0. The running count is **inferred, not measured** — it drifts if an agent dies without firing `SubagentStop`.
2. **Waiting-on-you detection** (PRD §3, priority 3 — no sensor was specified). A `wait.sh` hook on `PreToolUse`/`PostToolUse` for `AskUserQuestion|ExitPlanMode` sets the marker when Claude asks and clears it when you answer. While the marker is set, stall detection is suppressed so "waiting on you" never decays into "stopped".
3. **Liveness / stall accuracy.** The PRD's hooks only bump `last_activity` on file edits, Bash, and agent events, so a turn full of reads (or long thinking, or a pending question) would falsely read as Stopped. An `activity.sh` hook on `PreToolUse` for **all** tools refreshes `last_activity` on every tool call, and the stall threshold defaults to **180s** — long enough that genuine think/work stretches don't trip it. A turn with zero tool calls for >180s can still read as Stopped (there is no assistant-heartbeat hook); raise `CCBIT_STALL` if you hit that.

## Known limitations

1. **Single-window only.** ccbit tells you about a session only when you're looking at it — cross-session alerting is out of scope by design (a status line can't reach outside its window).
2. **Running agent count is inferred** (`spawned − done`), not measured; clamps at 0.
3. **Stall detection is time-based** (180s default; refreshed by every tool call). A turn with no tool calls at all for longer than the threshold could still read as Stopped — tune `CCBIT_STALL`.
4. **Motion is 2s / 2-frame, not smooth** — a hard floor from `refreshInterval`'s 1s minimum.
5. **Width fallbacks are heuristic** (`COLUMNS`-based); terminals that don't set `COLUMNS` are assumed wide.
6. **Catch-up has no read-receipt.** "Since you were last present" is approximated by turn boundaries (presence = your last prompt), capped at 5 groups.
7. **Build/test signal is exit-code only** (v1 / tier 1). The line shows `build succeeded` / `build failed` / `tests succeeded`, never counts or reasons. Detection is heuristic keyword-matching on the command (`build`, `compile`, `test`, `dotnet test`, `pytest`, `cargo`, …). Test counts and error diagnosis are v2 (see PRD §10), gated on confirming the Bash `tool_response` carries stdout.

## Layout

```
ccbit/
  statusline.sh              # renderer (stateless)
  hooks/
    turn-start.sh            # UserPromptSubmit
    activity.sh              # PreToolUse (all tools) — refreshes last_activity
    files.sh                 # PostToolUse Edit|Write|MultiEdit
    bash.sh                  # PostToolUse Bash
    agent-spawn.sh           # PostToolUse Task
    agent-done.sh            # SubagentStop
    wait.sh                  # Pre/PostToolUse AskUserQuestion|ExitPlanMode
    turn-end.sh              # Stop
    cleanup.sh               # SessionEnd
  install.sh                 # merge-not-overwrite installer
  error-signatures           # v2 tier-2 dictionary (user-editable, unused in v1)
  README.md
```
