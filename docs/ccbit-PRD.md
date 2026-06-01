# ccbit — Product Requirements & Implementation Spec

**Repo:** `livlign/ccbit`
**Audience:** Claude Code (build handoff). This document is implementation-precise. Treat every field name, file path, event name, and state value as authoritative unless flagged `[VERIFY]`.
**Status:** v1 = build now. v2 = specced, gated on the `[VERIFY]` checks in §10.

---

## 1. What ccbit is

A session-awareness layer for Claude Code. Hooks observe session state as events fire, write that state to per-session files, and a custom status line renders it — led by a stateful kaomoji face named **Bit** whose expression maps to the current session state.

The status line is the visible surface. The substance is the sensor-and-log layer underneath it. ccbit answers one question at a glance when the user returns to a session: *is anything working, done, waiting, broken, or stopped here — and what happened while I was away?*

### 1.1 The problem it solves

The user runs multiple Claude Code sessions daily and context-switches between them. Returning to a session costs a re-orientation tax: read the bottom of the transcript, then scroll up to reconstruct what happened. The status line is the only output that is always visible and never scrolls away, so it is the right surface for a persistent "where is this session" indicator. ccbit turns that always-on row into a session secretary: state at a glance, plus a turn-grouped catch-up of what happened during the user's absence.

### 1.2 Explicit non-goals

- **Not a cross-session alert layer.** ccbit only informs the user when they are looking at a given session's window. Notifying the user in session B that session A needs attention is out of scope. (A status line cannot reach outside its own window; that would require OS notifications, deliberately excluded.)
- **Not persistent history.** Catch-up is live-session only. Per-session state and logs are wiped on `SessionEnd`. No retention policy, no resume-across-restart, no `SessionStart` history surfacing.
- **Not real-time animation.** Motion is a 2-frame swap on a ~2s wall-clock cycle, not smooth animation. The `refreshInterval` floor (1s) makes smooth motion impossible and undesirable.

---

## 2. Architecture

```
Claude Code events ──► hooks (sensors) ──► per-session state files ──► status line (renderer)
                                                  │
                                                  └──► per-session append-only log (catch-up)
```

- **Sensors** are hooks. They are the only components that observe session state. They never render; they only write files.
- **State files** hold current values (latest tally, latest build result, working flag, agent counts). Read by the status line every render.
- **Log file** is append-only, grouped by turn. Backs both the catch-up tail and the agent-finish history.
- **Renderer** is the status line command. It is stateless between invocations — it reads files, picks a face, prints two lines. It never writes session state.

### 2.1 Per-session isolation

All session state lives under:

```
~/.claude/sessions/<session_id>/
```

Keyed by `session_id` (from hook/status-line stdin). Not `/tmp` — a reboot mid-session must not wipe a live session's state. `SessionEnd` removes the whole directory. This is the entire isolation + cleanup story: the directory exists exactly as long as the session does.

Files inside that directory (see §6 for formats):

| File | Written by | Read by |
|---|---|---|
| `turn` | `UserPromptSubmit`, `Stop` | status line |
| `files` | PostToolUse Edit/Write | status line |
| `build` | PostToolUse Bash | status line |
| `agents` | PostToolUse Bash (spawn), `SubagentStop` | status line |
| `log` | `UserPromptSubmit`, `Stop`, PostToolUse Bash, `SubagentStop` | status line (tail) |

---

## 3. Functional states

Bit has exactly **eight** top-level states. One face per state, fixed — never randomize. State is derived by the renderer from the state files, in priority order (first match wins):

| Priority | State | Derivation (from state files) |
|---|---|---|
| 1 | **Stopped** | `turn` = working AND last activity older than the stall threshold (see §7.3), or an explicit interrupt marker |
| 2 | **Failed** | latest `build` result = fail (non-zero exit) |
| 3 | **Waiting on you** | last event was `AskUserQuestion` or `ExitPlanMode` |
| 4 | **Agents running** | `agents` shows running > 0 |
| 5 | **Working** | `turn` = working |
| 6 | **Done (redeemed)** | `turn` = idle AND latest `build` = pass AND the immediately previous `build` in this turn group = fail |
| 7 | **Done (normal)** | `turn` = idle AND latest `build` = pass |
| 8 | **Idle** | none of the above (default) |

Priority matters: a failed build outranks "working" so a red build is never hidden by an in-progress next turn; "stopped" outranks everything because a stalled session is the most important thing to surface.

---

## 4. Face vocabulary

`()` shell throughout. One face per state. Motion only where noted.

| State | Face | Motion |
|---|---|---|
| Idle | `(•_•)` | static |
| Working | `(◣_◢)` ⇄ `(◢_◣)` | 2-frame, ~2s, wall-clock driven |
| Agents running | `┏(•_•)┛` ⇄ `┗(•_•)┓` | 2-frame shimmy, ~2s, wall-clock driven |
| Done (normal) | `(つ•‿•)つ` | static |
| Done (redeemed) | `(→_←")` | static |
| Waiting on you | `(◕_◕)?` | static |
| Failed | `(╯°□°)╯︵ ┻━┻` | static |
| Stopped | `(¬°-°)¬` | static |

### 4.1 Motion rule (load-bearing)

- Motion exists **only** in Working and Agents-running. Every other face is static.
- The animated frame is selected by wall-clock, not by render count: `frame = (epoch_seconds / 2) % 2`. This yields a ~2s swap regardless of how often the line repaints. Do **not** advance a stored frame counter — it must be stateless and time-derived so concurrent renders and event-driven repaints don't jitter the phase.
- Liveness the user actually feels during a long turn comes from the **numbers** ticking (file count, elapsed clock) next to a calm 2s face swap — not from the face itself moving fast.

### 4.2 Width safety

Single-width-safe core (render anywhere): `(•_•)`, `(◣_◢)`, `(◢_◣)`, `(◕_◕)`, `(¬°-°)¬`.

Width-risky glyphs requiring fallback: `つ` (hug), `︵` (table flip), and the box arms `┏ ┛ ┗ ┓`. These are double-width or presentation-form characters that can wrap or corrupt on narrow terminals.

Read terminal width from the `COLUMNS` env var (Claude Code sets it before running the status line; requires CC ≥ v2.1.153). When `COLUMNS` is below the fallback threshold (default **60**), substitute ASCII-safe forms:

| Full | Narrow fallback |
|---|---|
| `(つ•‿•)つ` | `(•‿•)` |
| `(╯°□°)╯︵ ┻━┻` | `(>_<) FAILED` |
| `┏(•_•)┛`⇄`┗(•_•)┓` | `(•_•)>`⇄`<(•_•)` |

If `COLUMNS` is unset (older CC), assume wide (no fallback) but never crash.

---

## 5. UX — two-line layout

The status line outputs two rows (two `echo`/`print` statements).

### 5.1 Line 1 — reactive (face leads)

Face first, then state-specific text. Examples by state:

```
(◣_◢) editing lhproduct · 4 · lhecommservice · 2 · 2m14s
┗(•_•)┓ agents: 5 spawned · 3✓ · 2⟳ · 4m08s
(◕_◕)? waiting on you
(╯°□°)╯︵ ┻━┻ lhproduct build failed
(つ•‿•)つ edited 7 files · build ✓ · tests ✓
(→_←") edited 3 files · build ✓ (recovered)
(¬°-°)¬ stopped · last: editing OrderService.cs · 3m ago
(•_•) idle
```

**Working text:** per-project split while in progress (`project · N` segments), driven by the `files` state file. "files so far" is a count of **distinct** file paths per project, not edit count. Elapsed clock from turn-start timestamp.

**Done text:** collapse the per-project split to a total (`edited N files`) plus build/test result. The split earns its width during the turn and steps aside after.

**Agents text:** `spawned · done✓ · running⟳ · elapsed`. Running count is derived (spawned − done), see §7.4 for the caveat.

### 5.2 Line 2 — ambient (static, low-saturation)

```
~/lhproduct · Opus · ctx 38% · 5h 23% (resets 1h12m) · 7d 41% (resets 2d3h)
```

Fields, all from status-line stdin:
- `workspace.current_dir` → display as `~`-relative or basename
- `model.display_name`
- `context_window.used_percentage` (handle null → `--`)
- `rate_limits.five_hour.used_percentage` + countdown from `resets_at` (absent for non-Pro/Max → omit segment)
- `rate_limits.seven_day.used_percentage` + countdown from `resets_at` (same)

Ambient line never animates and never changes color except threshold coloring on `ctx%` (green <70, yellow 70–89, red ≥90).

### 5.3 Catch-up tail (turn-grouped)

When the user returns and there is more than one completed turn group since the last one they were present for, the reactive line (or an extra line below it) surfaces a turn-grouped recap:

```
while away — turn 1: build ✓, 3 files · turn 2: 5 agents, 1 failed · turn 3: tests red on lhproduct
```

Rules:
- **Window:** all turn groups completed since the user was last present, NOT just the last turn. (Auto-pilot / headless dev-agent can complete several turns unattended; showing only the last would lie by omission.)
- **Caps (both required):** max **5** turn groups shown; max **10** events per group. Oldest groups beyond the cap collapse to `+N earlier turns`. A single runaway turn cannot flood the view.
- The tail is a glance/index. Full per-agent results and reasoning live in the `log` file and the transcript; ccbit points, it does not reproduce.

> **Open rendering decision for Claude Code to choose during build:** whether the catch-up tail is a third line that appears only when a multi-turn gap exists, or replaces Line 1 until the user submits the next prompt. Recommend: third line, present only when `turn-groups-since-presence > 1`, cleared on next `UserPromptSubmit`. Width-permitting; otherwise truncate with `+N earlier turns`.

---

## 6. State file formats

Plain text, one record per file, space- or pipe-delimited for cheap `read`/`jq`-free parsing where possible. All under `~/.claude/sessions/<session_id>/`.

### 6.1 `turn`
```
<state> <turn_start_epoch> <turn_index> <last_activity_epoch>
```
- `state`: `working` | `idle`
- Set `working` + new `turn_start_epoch` + increment `turn_index` on `UserPromptSubmit`.
- Set `idle` on `Stop`.
- `last_activity_epoch` bumped by any PostToolUse to feed the stall detector (§7.3).

### 6.2 `files`
Per-project distinct-file sets for the **current turn only** (reset on `UserPromptSubmit`). Store as one line per project:
```
<project>\t<count>\t<path1>|<path2>|...
```
- `project` = path segment after repo root (see §7.2). No allowlist — unknown paths bucket under their own leading segment so the per-project sum always equals the total.
- Dedupe by full path before counting. Editing the same file twice = 1.

### 6.3 `build`
Latest build/test result this turn (v1 = exit code only):
```
<kind> <exit_code> <epoch> <project>
```
- `kind`: `build` | `test`
- `exit_code`: integer (0 = pass)
- Append-only history of build results within a turn is what powers the "redeemed" detection (§3, priority 6) — keep the last two results in the turn group, or scan the `log`.

### 6.4 `agents`
```
<spawned> <done>
```
- `spawned` incremented when a subagent-spawning Bash/Agent tool is detected (see §7.4 `[VERIFY]`).
- `done` incremented on each `SubagentStop`.
- running = spawned − done (derived by renderer).

### 6.5 `log` (append-only, turn-grouped)
One event per line, prefixed with turn index and epoch:
```
<turn_index> <epoch> <event_type> <summary>
```
- `event_type`: `turn_start` | `turn_end` | `build` | `test` | `agent_done` | `wait` | `stop`
- Renderer groups by `turn_index` for the catch-up tail.
- No rotation; whole file dropped on `SessionEnd`. Bounded by session lifetime.

---

## 7. Sensors (hooks)

All hooks are `command` type. All read JSON on stdin, parse with `jq`, write to `~/.claude/sessions/<session_id>/`. All exit 0 (never block tool execution — these are observers, not gates). All resolve the session dir from `.session_id` and `mkdir -p` it defensively.

### 7.1 Hook registration (settings.json)
```json
{
  "statusLine": {
    "type": "command",
    "command": "~/.claude/ccbit/statusline.sh",
    "refreshInterval": 1,
    "padding": 1
  },
  "hooks": {
    "UserPromptSubmit": [
      { "hooks": [{ "type": "command", "command": "~/.claude/ccbit/hooks/turn-start.sh" }] }
    ],
    "PostToolUse": [
      { "matcher": "Edit|Write|MultiEdit", "hooks": [{ "type": "command", "command": "~/.claude/ccbit/hooks/files.sh" }] },
      { "matcher": "Bash", "hooks": [{ "type": "command", "command": "~/.claude/ccbit/hooks/bash.sh" }] }
    ],
    "SubagentStop": [
      { "hooks": [{ "type": "command", "command": "~/.claude/ccbit/hooks/agent-done.sh" }] }
    ],
    "Stop": [
      { "hooks": [{ "type": "command", "command": "~/.claude/ccbit/hooks/turn-end.sh" }] }
    ],
    "SessionEnd": [
      { "hooks": [{ "type": "command", "command": "~/.claude/ccbit/hooks/cleanup.sh" }] }
    ]
  }
}
```

`refreshInterval: 1` is load-bearing: without it, Line 1's file count and elapsed clock freeze between assistant messages during a long turn, and the agents tally goes stale during a subagent wait (event triggers go quiet while a coordinator waits on background subagents).

### 7.2 Path → project resolution
- Take `tool_input.file_path`, make it relative to the git repo root (`git rev-parse --show-toplevel`) or to `workspace.project_dir` if not in git.
- `project` = first path segment of that relative path. If the file is at repo root, `project` = repo basename.
- Generalizes to any repo; no hardcoded `lhproduct`/`lhecommservice`.

### 7.3 Stall / stopped detection
- `turn-start.sh` sets `state=working`. Every PostToolUse bumps `last_activity_epoch`.
- The renderer treats the session as **Stopped** when `state=working` AND `now − last_activity_epoch > STALL_THRESHOLD` (default **45s** — long enough to not false-positive on a slow API turn, short enough to catch the "Claude stopped, no sign" case the user hit).
- `Stop` firing normally sets `state=idle`, so a clean finish never trips the stall path. The stall path catches the case where the turn ends *without* a `Stop` (unexpected interruption).
- `[VERIFY]` whether an explicit interrupt produces any hook signal. If not, the time-based stall detector is the only mechanism and 45s is the tuning knob.

### 7.4 Agent spawn counting `[VERIFY]`
- `SubagentStop` reliably fires per finished agent → `done` increment is solid.
- **Spawn count has no dedicated event.** Options, in order of preference to try at build time:
  1. Detect the subagent-spawning tool in PostToolUse (the `Agent`/`Task` tool) and increment `spawned` from its input (number of agents requested).
  2. If the tool's input doesn't expose a clean count, derive `spawned` lazily as `max(seen_done, running_observed)` — weaker, drifts if an agent dies without `SubagentStop`.
- Document the chosen mechanism. The running count is **inferred, not measured** — acceptable, but it's the known soft spot. If `done > spawned` ever occurs, clamp running to 0.

---

## 8. Renderer (status line)

`~/.claude/ccbit/statusline.sh`. Stateless. Reads stdin JSON + state files, prints two lines.

Algorithm:
1. Parse stdin: `session_id`, `model.display_name`, `workspace.current_dir`, `context_window.used_percentage`, `rate_limits.*`. Read `COLUMNS` env.
2. Read state files from `~/.claude/sessions/<session_id>/` (missing files → safe defaults; never crash on a fresh session).
3. Derive state by §3 priority order.
4. Pick face by §4; apply §4.1 motion (wall-clock frame) and §4.2 width fallback.
5. Build Line 1 text for the derived state (§5.1).
6. If multi-turn catch-up window exists, build the tail (§5.3).
7. Build Line 2 ambient (§5.2).
8. Print. Keep total runtime fast (cache nothing that needs git unless stale; see CC docs caching pattern keyed by `session_id`).

Robustness requirements (from CC status-line docs):
- Non-zero exit or empty output blanks the status line — always exit 0 and always print something.
- Slow scripts block updates and get cancelled if a new update fires mid-run — keep it fast; avoid unguarded `git` calls on every render.
- Handle `null` `used_percentage` (early session, post-`/compact`) with `// 0` / `--`.

---

## 9. v1 build scope (build now)

Everything above except the items in §10. Concretely:

- Six hooks + cleanup, registered per §7.1.
- Five state files + log, formats per §6.
- Eight-state derivation, face vocabulary, motion, width fallback.
- Two-line layout; per-project split during turn, collapse on done.
- Idle handling: inert when idle, elapsed clock + `refreshInterval` liveness during long turns, `SubagentStop` tally during agent waits.
- Per-session isolation under `~/.claude/sessions/<id>/`, wiped on `SessionEnd`.
- Turn-grouped catch-up tail with both caps.
- **Build/test signal = exit code only** (tier 1). Line shows `build ✓` / `build failed` / `tests ✓`, NOT counts, NOT reasons.

v1 uses only fields confirmed present in the status-line stdin schema and the documented PostToolUse fields. It does not depend on parsing command stdout.

---

## 10. v2 scope (specced, gated on `[VERIFY]`)

These require reading a command's **stdout**, which v1 deliberately avoids. Gate: confirm the Bash `tool_response` actually carries stdout to the hook, and what field holds it. `[VERIFY: does PostToolUse Bash tool_response include the command's stdout, and under what key? Docs indicate .tool_response.exit_code exists post-execution; stdout field name unconfirmed.]`

### 10.1 Test-case counts
- Parse `dotnet test` stdout for the passed/failed/total counts (`Passed: N`, `Failed: N`).
- Render: `tests ✓ lhproduct (222) · lhecommservice (15)`.
- Per-project breakdown depends on whether Claude runs `dotnet test` per project (clean — one result each, accumulate) or once at solution level (one mixed blob to parse). Handle both; per-project is cleaner when commands are per-project.

### 10.2 Tier-2 error diagnosis (signature dictionary)
- On build/test failure, grep stderr/stdout against a user-maintained pattern → message map.
- Example entries (the user's recurring errors): a 401/auth failure mentioning `codeartifact` → `renew AWS SSO for codeartifact`; NuGet restore auth failure → its known remedy.
- Unknown errors fall back to tier-1 (`build failed — look`). Gold for recurring errors, silent on novel ones. Pattern file lives at `~/.claude/ccbit/error-signatures` (user-editable).

### 10.3 Tier-3 error diagnosis (model-assisted)
- On failure with no tier-2 match, pipe the error text to a cheap model call, write back a one-line diagnosis to `build`/`log`.
- Generalizes to any error. Cost: a small API call per failure + latency in the hook path. This reintroduces the synthesis layer v1 avoids — implement as opt-in (config flag `CCBIT_TIER3=1`), default off.

### 10.4 "Needs attention" specificity
- v1: generic derived state (failed build / waiting). v2: specific message using tier-2/3 output (`blocked: lhproduct build failed — renew AWS SSO`).

---

## 11. Packaging & repo

- Repo: `livlign/ccbit` (GitHub username confirmed available under the `livlign` handle).
- `[VERIFY]` npm package name `ccbit` — run `npm view ccbit` (404 = free). If taken, publish as scoped `@livlign/ccbit`; repo name unchanged.
- Layout:
  ```
  ccbit/
    statusline.sh
    hooks/
      turn-start.sh  files.sh  bash.sh  agent-done.sh  turn-end.sh  cleanup.sh
    install.sh            # writes/merges settings.json, chmod +x, mkdir state root
    error-signatures      # v2, user-editable
    README.md
  ```
- `install.sh` must **merge** into existing `~/.claude/settings.json` (the user has other hooks/statusline configs), not overwrite. Back up settings.json before writing.
- All scripts `chmod +x`. No comments in scripts unless the logic is non-obvious (wall-clock frame math and stall detection are the two places a one-line comment is warranted).

---

## 12. Known limitations (state in README)

1. **Single-window only.** ccbit tells you about a session only when you're looking at it. By design — cross-session alerting is out of scope.
2. **Running agent count is inferred** (spawned − done), not measured. Drifts if an agent dies without `SubagentStop`; clamps at 0.
3. **Stall detection is time-based** (45s default). A genuinely slow API turn could momentarily read as Stopped; tune `STALL_THRESHOLD` if false positives occur.
4. **Motion is 2s/2-frame, not smooth.** Hard floor from `refreshInterval` minimum of 1s.
5. **Width fallbacks are heuristic.** `COLUMNS`-based; on terminals that don't set it (CC < 2.1.153) ccbit assumes wide.
6. **Catch-up has no read-receipt.** "Since you were last present" is approximated by turn boundaries, capped at 5 groups / 10 events.

---

## 13. Build order (suggested)

1. State root + `session_id` plumbing + `cleanup.sh` (so test sessions don't leak dirs).
2. `turn-start.sh` / `turn-end.sh` + `turn` file + renderer skeleton printing Idle/Working faces. Verify with mock stdin (CC docs §Tips mock-input command).
3. `files.sh` + per-project split + elapsed clock.
4. `bash.sh` tier-1 build/test exit-code signal + Failed/Done/redeemed states.
5. `agent-done.sh` + `agents` file + agent state and tally; resolve §7.4 spawn count.
6. Line 2 ambient + width fallback + motion.
7. `log` + turn-grouped catch-up tail with caps.
8. `install.sh` (merge-not-overwrite) + README + limitations.
9. v2 behind `[VERIFY]` of the stdout field.

Each step is independently testable with the mock-stdin pattern before wiring the real hook.
