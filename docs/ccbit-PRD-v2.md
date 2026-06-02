# ccbit — Product Requirements & Implementation Spec (v2)

**Repo:** `livlign/ccbit`
**Audience:** Claude Code (build handoff). Implementation-precise. Treat every field name, path, and value as authoritative unless flagged `[VERIFY]` or `[DECISION PENDING]`.
**Status:** v2 supersedes v1. Architecture changed from hooks + bash to **transcript-driven, single Go binary, zero hooks**, based on a live probe of the transcript JSONL (results in §A). v1's hook/state-file/cleanup machinery is removed.

---

## 0. As-built status (updated 2026-06-02)

The renderer (build-order steps 1–9) is **implemented, tested, and installed** on the dev machine: a Go module (`go 1.26`) with `cmd/ccbit` + `internal/{transcript,state,render,input}`, unit-tested, built to `~/.claude/ccbit/ccbit.exe`, and wired as the global `statusLine`. Deltas from the original v2 text below — all reflected in the relevant sections:

- **Catch-up line REMOVED (was §5.3).** The "while away" recap had no read-receipt and couldn't distinguish "auto-pilot ran while away" from "user is firing quick prompts," so it fired during normal active use. The status line is now **always exactly two lines**. (§5.3, §1, §12, §13 updated.)
- **Subagent sidechain files EXIST** (supersedes Appendix A-E "no separate jsonl"). Current CC writes `<transcript-dir>/<session-id>/subagents/agent-<id>.jsonl`. Running-agent detection now reads that dir — the only reliable signal, since a long agent run writes nothing to the main transcript. (§6.4, §6.5, §A-E.)
- **Stall default 45s → 90s**, overridable via `CCBIT_STALL` (seconds). Stopped is now **suppressed while an agent is running**, and subagent file activity folds into "last activity." (§3, §6.5.)
- **Per-state line-1 color** (whole line): Failed=red, Stopped=bright-red, Waiting=yellow, Done=green, Working/Agents=cyan, Idle=white. `ctx%` colors only at thresholds (≥70 yellow, ≥90 red; grey below). Line 2 is plain (no dim). (§5.)
- **Working face decided:** `-(๏_๏)-` ⇄ `৲(๏_๏)৲` (§4.3 resolved). Agents box-arms confirmed rendering on the target Windows terminal.
- **Line-1 text wording:** Working per-project segments are `project (N files)` (count bound to project, never a bare number); Agents reads `N agents running · M done · elapsed`.
- **Renderer caching:** bounded tail-read (2 MiB) + on-disk repo-root cache keyed by cwd (the byte-offset cache of §8.1 was not needed). (§8.)
- **Deferred (not built):** plugin/marketplace packaging + cross-platform binaries (§11.3 still pending — only the Windows binary exists), tier-2/3 error diagnosis (§10), and removal of the leftover v1 `hooks/`, `statusline.sh`, `install.sh` from the repo.

---

## 1. What ccbit is

A session-awareness status line for Claude Code. A single program runs as the `statusLine` command. On each render it reads the session's transcript JSONL (path supplied by Claude Code on stdin), derives the current session state, and prints two lines — led by a stateful kaomoji face named **Bit** whose expression maps to that state.

ccbit answers one question at a glance when the user returns to a session: *is anything working, done, waiting, broken, or stopped here?*

There are **no hooks**. There are **no per-session state files**. The transcript is the source of truth; Claude Code already writes it and owns its lifecycle. ccbit only reads.

### 1.1 The problem it solves

The user runs multiple Claude Code sessions daily and context-switches between them. Returning to a session costs a re-orientation tax: read the bottom of the transcript, then scroll up to reconstruct what happened. The status line is the only output that is always visible and never scrolls away, so it is the right surface for a persistent "where is this session" indicator. ccbit turns that always-on row into a session secretary: current state at a glance, plus the last turn's result (files edited, build/test outcome) on the same line.

### 1.2 Why transcript-driven (the v1 → v2 change)

v1 used hooks as sensors writing state files. On Windows that path is fragile: it requires jq, LF line endings, a specific Git Bash binary (`usr\bin\bash.exe`, not the launcher), and a precise enable order, or hooks deadlock on stdin. A live probe (§A) confirmed the transcript JSONL contains everything the hooks were capturing — turn boundaries, edited file paths, build pass/fail, subagent activity — and that completed tool results land in the file within ~1–2s, mid-turn. So the renderer can derive all state by reading the transcript, with no hooks. This removes the entire Windows hook-deadlock surface, the enable trap, the state files, and the cleanup step.

### 1.3 Explicit non-goals

- **Not a cross-session alert layer.** ccbit only informs the user when they are looking at a given session's window. A status line cannot reach outside its own window; OS notifications are deliberately excluded.
- **Not persistent history.** ccbit stores no session state; it derives everything from the transcript per render.
- **Not real-time animation.** Motion is a 2-frame swap on a ~2s wall-clock cycle. The `refreshInterval` floor (1s) makes smooth motion impossible and undesirable.
- **Not fine-grained subagent progress.** Subagents are shown as binary running/done. (Current CC does write a per-subagent sidechain file (§6.4) used to detect *whether* an agent is running, but ccbit does not surface its internal steps.)

---

## 2. Architecture

```
Claude Code ──(stdin JSON: transcript_path, session_id, model, cost, context, rate_limits)──► ccbit binary
                                                                                                   │
                                  reads ──► ~/.claude/projects/<encoded-cwd>/<session-id>.jsonl    │
                                                                                                   ▼
                                                                          parses tail ──► derives state ──► prints 2 lines
```

- **One component:** a single compiled binary (`[DECISION: Go]`, see §11). It is the `statusLine` command. It reads stdin, reads the transcript, prints two lines, exits. Nothing else.
- **No hooks, no state files, no cleanup.** The transcript is the state.
- **Stateless across renders**, except a disposable on-disk repo-root cache (so a 1s refresh doesn't spawn `git` every render). The cache is a pure optimization, never authoritative; delete it anytime. (As built, cost is bounded by reading only the last 2 MiB of the transcript per render — see §8.1.)

### 2.1 Why Go (not bash)

A compiled binary eliminates the three bash-specific Windows failures from the v1 debug log: no `jq` dependency (parses JSON natively), no CRLF problem (it is a binary, not a `.sh`), and no stdin deadlock (invoked as `bash -c "ccbit"` it runs as a direct child that reads stdin to EOF and exits; there is no launcher grandchild holding the pipe). Compiles for windows/macos/linux. Matches the language already used for `whatcc`.

---

## 3. Functional states

Bit has eight top-level states. One face per state, fixed — never randomize. The renderer derives state from the transcript (§6) in priority order; first match wins.

| Priority | State | Derivation |
|---|---|---|
| 1 | **Stopped** | Turn is open AND last activity (max of the latest transcript entry and any live subagent's file mtime) is older than `STALL_THRESHOLD` (default **90s**, `CCBIT_STALL` env override) AND no agent is running AND not waiting. See §6.5. |
| 2 | **Failed** | Most recent build/test `tool_result` in the current turn has `is_error = true` (§6.3). |
| 3 | **Waiting on you** | Most recent tool in the current turn is `AskUserQuestion` or `ExitPlanMode` (by tool name on the assistant `tool_use`, or its result). |
| 4 | **Agents running** | A subagent is running per §6.4 (live `subagents/agent-*.jsonl`), or fallback `Task` spawns − completions > 0. |
| 5 | **Working** | Current turn is open (a user prompt with no closing boundary yet) and none of 1–4 apply. |
| 6 | **Done (redeemed)** | Turn closed, latest build/test `is_error = false`, AND an earlier build/test in the same turn had `is_error = true`. |
| 7 | **Done (normal)** | Turn closed, latest build/test `is_error = false`. |
| 8 | **Idle** | Default (turn closed, no build this turn, nothing pending). |

Priority order matters: Failed outranks Working so a red build is never hidden by an in-progress next turn; Stopped is listed first but is gated to *not* fire while an agent is running or while waiting on the user (both are legitimate "open and quiet" states, not stalls — see §6.5).

---

## 4. Face vocabulary

`()` shell throughout. One face per state. Motion only where noted.

| State | Face | Motion |
|---|---|---|
| Idle | `(•_•)` | static |
| Working | `-(๏_๏)-` ⇄ `৲(๏_๏)৲` (see §4.3) | 2-frame, ~2s, wall-clock |
| Agents running | `┏(•_•)┛` ⇄ `┗(•_•)┓` | 2-frame shimmy + live count |
| Done (normal) | `(つ•‿•)つ` | static |
| Done (redeemed) | `(→_←")` | static |
| Waiting on you | `(◕_◕)?` | static |
| Failed | `(╯°□°)╯︵ ┻━┻` | static (ASCII fallback when narrow) |
| Stopped | `(¬°-°)¬` | static |

### 4.1 Motion rule (load-bearing)

- Motion exists only in Working and Agents-running. Every other face is static.
- The animated frame is selected by wall-clock, stateless: `frame = (epoch_seconds / 2) % 2`. ~2s swap regardless of repaint frequency. Do not store/advance a frame counter.
- Liveness the user feels during a long turn comes from the numbers ticking (file count, elapsed clock) next to a calm 2s face swap, not from the face moving fast.

### 4.2 Width safety

Single-width-safe core: `(•_•)`, `(◕_◕)`, `(¬°-°)¬`. Risky glyphs needing fallback: `つ` (hug), `︵` (table flip), box arms `┏ ┛ ┗ ┓`. Read terminal width from `COLUMNS` (Claude Code sets it; requires CC ≥ 2.1.153). Below threshold (default 60), substitute:

| Full | Narrow fallback |
|---|---|
| `(つ•‿•)つ` | `(•‿•)` |
| `(╯°□°)╯︵ ┻━┻` | `(>_<) FAILED` |
| `┏(•_•)┛`⇄`┗(•_•)┓` | `(•_•)>`⇄`<(•_•)` |

If `COLUMNS` unset, assume wide; never crash.

### 4.3 Working face — RESOLVED

Final: **`-(๏_๏)-` ⇄ `৲(๏_๏)৲`** — `๏` (Thai, U+0E4F) eyes with arms flipping flat `-…-` → Bengali rupee-mark `৲…৲`, chosen by the user after live trials in the target terminal.

The original v1 `(◣_◢)` ⇄ `(◢_◣)` was rejected: the Geometric-Shapes triangles `◣ ◢` tofu/mangle on the user's Windows font. The two hard rules still hold and are satisfied: (1) both frames differ from the idle face `(•_•)`; (2) the chosen glyphs render in the user's terminal (verified live).

Both frames are selected so they animate visibly via the arms even when the eyes are static. **Glyph choice is terminal-specific** — `๏`/`৲` happen to render on this user's setup; for a portable default, the pure-ASCII fallbacks remain valid: side-scan `(>_>)`⇄`(<_<)`, angle-arms `<(•_•)>`⇄`>(•_•)<`, or `-(•_•)-`⇄`\(•.•)/`. Re-verify any change in the actual status line, since CC's tool/command output strips ANSI and (for the status-line surface) may differ from the main content font.

---

## 5. UX — two-line layout

Two rows printed to stdout.

### 5.1 Line 1 — reactive (face leads)

Face first, then state text:

```
-(๏_๏)- editing lhproduct (4 files) · lhecommservice (2 files) · 2m14s
┗(•_•)┓ 3 agents running · 2 done · 4m08s
(◕_◕)? waiting on you
(╯°□°)╯︵ ┻━┻ lhproduct build failed
(つ•‿•)つ edited 7 files · build ✓ · tests ✓
(→_←") edited 3 files · build ✓ (recovered)
(¬°-°)¬ stopped · last: editing OrderService.cs · 3m ago
(•_•) idle
```

- **Working text:** per-project segments `project (N files)` — the count is **bound to its project with a unit**, never a bare number floating between separators. From the current turn's Edit/Write/MultiEdit results (§6.2); "N" = distinct file paths per project. Elapsed clock from the turn's first entry timestamp.
- **Done text:** collapse the split to a total (`edited N files`) plus build/test result.
- **Agents text:** lead with what's happening now — `N agents running` — then `· M done` only when M > 0, then elapsed. Plain words, no `✓`/`⟳` symbols. Counts come from the subagents dir when present (§6.4), else the spawn−completion fallback; clamp at 0, never negative.
- **Whole-line color (per state):** Failed = red, Stopped = bright-red, Waiting = yellow, Done (normal + recovered) = green, Working/Agents = cyan, Idle = white. The animated face already signals the state; color reinforces it. Honors `NO_COLOR`.

### 5.2 Line 2 — ambient (plain, default color)

```
~/lhproduct · Opus · ctx 38% · 5h 23% (resets 1h12m) · 7d 41% (resets 2d3h)
```

All from stdin JSON, rendered in the terminal's default color (an earlier dim/grey treatment was removed — it was hard to read):
- `workspace.current_dir` → `~`-relative or basename
- `model.display_name`
- `context_window.used_percentage` (null → `--`); **colored only when it warrants attention** — yellow at 70–89, red ≥90, plain default below 70 (a green "all fine" badge was removed as noise).
- `rate_limits.five_hour.used_percentage` + countdown from `resets_at` (absent → omit segment)
- `rate_limits.seven_day.used_percentage` + countdown from `resets_at` (absent → omit)

### 5.3 Catch-up tail — REMOVED

Originally a third line recapping turns completed "while away." **Cut entirely.** There is no read-receipt in the transcript, so "user was away" is indistinguishable from "user is firing quick prompts back-to-back" — the line fired during normal active use and read as wrong/confusing. The current state (and the last turn's result, already on Line 1) cover the need. **ccbit always prints exactly two lines.**

---

## 6. Transcript parsing (replaces v1 sensors + state files)

Transcript is JSONL at `~/.claude/projects/<encoded-cwd>/<session-id>.jsonl`. **Do not derive the path** — read `transcript_path` (and `session_id`) directly from stdin (§7). Each line is one entry. Measured schema (§A); treat as authoritative for current CC, but see §12 on version coupling.

### 6.1 Entry shapes (measured)

Common fields on entries: `type`, `timestamp`, `uuid`, `parentUuid` (chain), `cwd`, `sessionId`, `isSidechain`, `userType`.

- **User prompt (turn start):** `type == "user"`, `message.content` is a **string**, no `toolUseResult` key. First one has `parentUuid: null`, `userType: "external"`. Carries `promptId`.
- **Assistant action:** `type == "assistant"`, `message.content` is an array that may contain `tool_use` blocks. A `tool_use` block has `name` and `input`. `message.stop_reason` is `"tool_use"` mid-turn and **`"end_turn"` on the final message that closes the turn** — the clean open/closed discriminator. Carries `requestId`, **not** `promptId`. **Trails by ~one inference** — do not depend on the latest `tool_use` being present; depend on results.
- **Tool result:** `type == "user"` carrying a `toolUseResult` sibling and a `content` array of `tool_result`. Flushes on tool completion (~1–2s). **This is the primary signal source.** Carries `promptId`.
- **Turn segmentation (measured correction):** `promptId` is present only on user-side entries (prompts + tool results), **not** shared across an entire turn (assistant messages have `requestId` instead). So turns are segmented by **linear user-prompt boundaries**, not by `promptId`. Metadata sidecar entry types (`last-prompt`, `ai-title`, `mode`, `permission-mode`, snapshots, attachments) are skipped.

### 6.2 Edited files
From Edit/Write/MultiEdit `tool_use` blocks: `.input.file_path`. Map path → project by §6.6. Dedupe by full path (same file edited twice = 1). Scope to the current turn (entries after the last user-prompt boundary — see turn segmentation in §6.1).
Note: file paths sit on the `tool_use` (which trails one inference), so a just-started edit may appear up to one inference late. Acceptable; the count catches up within ~1–2s.

### 6.3 Build / test pass-fail
From the Bash `tool_result`:
- **Pass:** `is_error == false`; `toolUseResult` is an object `{stdout, stderr, interrupted, isImage, noOutputExpected}`.
- **Fail:** `is_error == true`; `toolUseResult` is a string like `"Error: Exit code 1\n..."`; the result `content` text begins `"Exit code N"`.
- **There is no numeric exit-code field.** `is_error` is the clean binary signal. The numeric code exists only as text on failure — sufficient for tier-1, and reusable for tier-2/3 diagnosis (§10).
- Identify build/test commands by matching the originating Bash `tool_use.input.command` (e.g. `dotnet (build|test)`); since `tool_use` trails, you may match on the command recorded alongside the result, or correlate result→use via `parentUuid`. Tier-1 needs only "a Bash command in this turn returned `is_error`"; command classification refines the label.

### 6.4 Subagents (corrected — sidechain files now exist)
Current CC **does** write a per-subagent sidechain file (supersedes Appendix A-E "no separate jsonl"):

`<transcript-dir>/<session-id>/subagents/agent-<agentId>.jsonl` — full `isSidechain:true` internal turns.

This is the authoritative running-agent signal, because during a long agent run the **main transcript receives nothing** (the `Task` spawn `tool_use` trails ~one inference and often doesn't flush until the agent finishes — so neither spawn nor completion is visible mid-run). Scan the dir:

- **Running** = a subagent file whose last entry is **not** an `end_turn` assistant message, with a fresh mtime (within ~3×`STALL_THRESHOLD`; older = treated as dead/abandoned).
- **Done** = a subagent file whose last entry **is** `end_turn`. Scope "done this turn" by `mtime ≥ turnStart` (files persist whole-session); prune files untouched since before the turn without reading them.
- **Fallback** (no subagents dir, e.g. older CC): main-transcript `Task` spawns − completions, clamped ≥ 0. Completion = a `tool_result` whose `content` is `[{text:"DONE"…},{text:"agentId: …"}]`.
- Internal steps are still **not surfaced** (binary running/done), but `running > 0` both drives the Agents state and **suppresses Stopped** (§6.5).

### 6.5 Stopped / stall detection
- A turn is **open** if the last meaningful entry is not an assistant `end_turn` (i.e. it's a tool result, a pending `tool_use`, or a fresh user prompt). An `end_turn` assistant message closes the turn.
- **last activity** = max of the latest main-transcript entry `timestamp` and the freshest *running* subagent file mtime (§6.4). A running agent writes only to its sidechain, so without this fold a long agent run looks stalled.
- **Stopped** = turn open AND last activity older than `STALL_THRESHOLD` (default **90s**; override `CCBIT_STALL=<seconds>`) AND **no agent running** AND **not waiting on the user**. The agent/waiting guards matter because both are legitimate "open and quiet" states, not stalls.
- Default raised 45s → 90s: a long pure-text generation writes no transcript entries while composing, so 45s produced false "stopped" during normal work.
- A normally completed turn closes cleanly (`end_turn`), so it never trips Stopped.

### 6.6 Path → project
- Make `file_path` relative to the git repo root (`git rev-parse --show-toplevel`) or to `workspace.project_dir` (stdin) if not in git.
- `project` = first path segment of the relative path; if the file is at repo root, `project` = repo basename.
- No allowlist — unknown paths bucket under their own leading segment, so the per-project sum always equals the total. Generalizes to any repo.

---

## 7. stdin contract (from Claude Code)

The binary reads one JSON object on stdin per render (measured + documented schema):

```json
{
  "hook_event_name": "Status",
  "session_id": "1c72...",
  "transcript_path": "/abs/path/to/<session-id>.jsonl",
  "cwd": "D:\\claude\\ccbit",
  "model": { "id": "claude-opus-4-...", "display_name": "Opus" },
  "workspace": { "current_dir": "...", "project_dir": "..." },
  "version": "2.1.x",
  "output_style": { "name": "default" },
  "cost": { "total_cost_usd": 0.0, "total_duration_ms": 0, "total_lines_added": 0, "total_lines_removed": 0 },
  "context_window": { "used_percentage": 0, "context_window_size": 200000, "total_input_tokens": 0, "total_output_tokens": 0 },
  "rate_limits": { "five_hour": { "used_percentage": 0, "resets_at": 0 }, "seven_day": { "used_percentage": 0, "resets_at": 0 } }
}
```

- Use `transcript_path` for the transcript; use `session_id` as the cache key (§8.1). Do not reconstruct the encoded-cwd path.
- `cost`, `context_window`, `rate_limits` may be absent early in a session or after `/compact`; handle missing → safe defaults (`--`), never crash.
- `[VERIFY]` `rate_limits` shape/availability varies by plan (Pro/Max present; others may omit). Omit the segment when absent.

---

## 8. Renderer algorithm

1. Read stdin JSON. Extract `transcript_path`, `session_id`, `model`, `workspace`, `context_window`, `rate_limits`, `cost`. Read `COLUMNS`.
2. Read the transcript tail (§8.1) and scan the subagents dir (§6.4).
3. Parse entries; build current-turn view: open/closed (via `end_turn`), per-project edited files, build results (with `is_error`), pending question; combine with subagent running/done counts.
4. Derive state by §3 priority.
5. Pick face (§4); apply motion (§4.1) and width fallback (§4.2).
6. Build Line 1 for the state (§5.1) with whole-line color; Line 2 ambient (§5.2). Always exactly two lines.
7. Print both lines. Always exit 0. Always print something (empty/non-zero output blanks the status line).
8. Keep it fast: a slow renderer blocks updates and gets cancelled if a new update fires mid-run. Avoid unguarded `git` calls every render — repo root is resolved once and cached on disk (§8.1), with a 250ms timeout on the `git` call itself.

### 8.1 Caching (as built)
- **Bounded tail-read:** read only the last 2 MiB of the transcript (seek, drop the partial first line, parse forward). Bounds cost on long sessions while always capturing the current turn and recent context. No incremental byte-offset cache was needed.
- **Repo-root cache:** `git rev-parse --show-toplevel` is cached to `$TMPDIR/ccbit/root-<hash(cwd)>` (TTL ~6h) so a 1s refresh doesn't spawn `git` every render. This is the only thing ccbit writes — renderer cache, NOT session state, safe to delete anytime.

---

## 9. v1 → v2 deletions (do not build)

Removed entirely: all hooks (`UserPromptSubmit`, PostToolUse, `SubagentStop`, `Stop`, `SessionEnd`, `PreToolUse`); all per-session state files (`turn`, `files`, `build`, `agents`, `log`); the `~/.claude/sessions/<id>/` directory; `SessionEnd` cleanup; the `install.sh` settings.json merge for hooks; the `CLAUDE_CODE_GIT_BASH_PATH` enable-order procedure; the jq dependency; the `.sh` scripts and their CRLF/LF handling.

The only Claude Code config ccbit needs is the `statusLine` block (§11.2).

---

## 10. Error diagnosis tiers (now buildable — no longer gated)

The v1 stdout `[VERIFY]` is resolved: the failure text is in the transcript `tool_result` (§6.3). All tiers read the same entry.

- **Tier 1 (build now):** `is_error` → `build ✓` / `build failed`. No text parsing.
- **Tier 2 (signature dictionary):** on failure, grep the result `content` text against a user-maintained pattern→message map (`~/.config/ccbit/error-signatures` or in-repo). Example: text containing `401`/`codeartifact` → `renew AWS SSO for codeartifact`. Unknown → fall back to tier 1.
- **Tier 3 (model-assisted, opt-in, default off):** on failure with no tier-2 match, send the result text to a cheap model call, write a one-line diagnosis. Adds an API call + latency per failure; gate behind `CCBIT_TIER3=1`.

Tiers 2–3 are post-v1 but no longer blocked by a verification.

---

## 11. Packaging & distribution

### 11.1 Language & repo
- Single binary. `[DECISION: Go]` — confirmed direction (matches `whatcc`); cross-compiles win/mac/linux.
- Repo `livlign/ccbit`; npm name `ccbit` confirmed free (not used for distribution — kept reserved only).

### 11.2 Distribution: Claude Code plugin + marketplace
- Ship as a CC plugin so users install without hand-editing settings.json. A marketplace is a git repo with `.claude-plugin/marketplace.json`; add ccbit as an entry pointing at `livlign/ccbit` (reuse the existing `livlign` marketplace).
- User install: `/plugin marketplace add livlign/<marketplace>` then `/plugin install ccbit`.
- The plugin ships the `statusLine` config pointing at the binary via `${CLAUDE_PLUGIN_ROOT}`.
- `[VERIFY]` whether an enabled plugin's `statusLine` auto-applies or the user must select it (docs confirm plugins ship hooks/statusLine, but plugin `settings.json` defaults officially support only the `agent` key today). Worst case: user sets one `statusLine` key — far less risk than v1's hooks merge. Test by installing locally via `/plugin marketplace add ./ccbit`.

### 11.3 Per-platform binary selection `[DECISION PENDING — still open]`
Not yet decided or built. As of 2026-06-02 only the Windows binary exists, built locally and copied to `~/.claude/ccbit/ccbit.exe`, with the global `statusLine.command` pointing at it (the §11.4 manual-install path). The `statusLine.command` is a static string, but the binary differs per OS/arch. Options:
1. Commit prebuilt binaries to the plugin repo under `bin/` and point the command at a platform path (needs a selection mechanism — a tiny launcher reintroduces a shell, which we are avoiding).
2. Name a single entry and have `install`/first-run resolve the platform binary.
3. Ship one universal invocation that the binary itself dispatches.
Decide at build time. Recommendation: commit per-platform binaries (`ccbit-windows-amd64.exe`, `ccbit-darwin-arm64`, `ccbit-linux-amd64`, etc.) and select via the minimal mechanism that avoids a shell shim. Go binaries are a few MB each; three to five in-repo is acceptable.

### 11.4 Non-plugin fallback
README documents manual install: download the platform binary, add a `statusLine` block to `~/.claude/settings.json` pointing at it. One JSON key, no scripts.

### 11.5 Repo layout
```
ccbit/
  go.mod
  cmd/ccbit/main.go         # stdin parse, wiring, repo-root cache
  internal/
    transcript/   # JSONL tail-read + parse, turn segmentation, subagents scan (§6)
    state/        # state derivation (§3)
    render/       # faces, lines, color, width fallback (§4, §5)
    input/        # stdin JSON contract (§7)
  bin/            # local build output (gitignored; per-platform commit is the §11.3 decision)
  .claude-plugin/plugin.json   # NOT YET CREATED (packaging deferred)
  error-signatures   # tier-2, user-editable (post-v1)
  README.md
```
As built: `cmd/ccbit` + the four `internal/` packages exist and are unit-tested. `.claude-plugin/` and committed cross-platform binaries are deferred (§0). The repo also still contains the **unused v1** `hooks/`, `statusline.sh`, `install.sh` (slated for deletion per §9).

---

## 12. Known limitations (state in README)

1. **Single-window only.** ccbit informs you only when you are looking at the session. By design.
2. **Transcript schema is internal/undocumented.** ccbit couples to the JSONL field shapes in §6 (`is_error`, `toolUseResult`, `tool_use.input.file_path`, `stop_reason`, the `subagents/` dir, etc.). These can and do change across CC versions (the subagent-sidechain behavior already changed between the original probe and the as-built — §6.4). This is the primary fragility traded in from v1's Windows-install fragility; it breaks uniformly (fix once) rather than per-user. Pin a tested CC version range in README; degrade gracefully on unknown shapes (never crash).
3. **`tool_use` trails ~one inference.** The latest assistant action (notably `Task` spawns) appears up to one inference late; results are prompt. Derive from results; for agents, prefer the subagents dir (§6.4) over main-transcript spawn counts.
4. **Subagent visibility is boundary-only.** Running/done is derived from the sidechain file's last entry + mtime (§6.4); ccbit does not surface the agent's internal steps. Binary running/done.
5. **Build pass/fail via `is_error`, no numeric exit code.** Exact code only as failure text. Fine for tier-1; tier-2/3 parse the text.
6. **Motion is 2s/2-frame, not smooth.** `refreshInterval` floor 1s.
7. **Glyph rendering is terminal/font-specific.** The chosen faces (esp. the Working face) must be verified in the actual status line; several Unicode blocks (Geometric Shapes, Canadian Syllabics) tofu on common Windows fonts. ANSI is stripped from CC tool/command output, so previews there don't reflect the status-line surface.
8. **Width fallbacks heuristic.** `COLUMNS`-based; assumes wide if unset.
9. **Stall vs long generation.** A long pure-text generation writes no transcript entries; `STALL_THRESHOLD` (90s, tunable) trades off false "stopped" against detecting a real hang. Agent runs are covered separately via the subagents dir.

---

## 13. Build order (suggested)

Steps 1–9 are **done** (✓); 10–11 deferred.

1. ✓ stdin parse + transcript locate (read `transcript_path`); fixed `(•_•) idle` + ambient line.
2. ✓ Bounded tail-read (§8.1); parse entries into a typed model (user prompt / assistant tool_use / tool_result), exposing `timestamp`, `stop_reason`, `is_error`, `tool_use.name/input`.
3. ✓ Turn model: open/closed via `end_turn`, current-turn scoping by linear user-prompt boundaries → Working vs Idle + elapsed clock.
4. ✓ Edited-file derivation (§6.2) + per-project split (§6.6) → Working line (`project (N files)`).
5. ✓ Build/test pass-fail (§6.3) → Failed / Done / redeemed states + Done line.
6. ✓ Subagents (§6.4, subagents dir) → Agents-running state + count.
7. ✓ Waiting (`AskUserQuestion`/`ExitPlanMode`) + Stopped (§6.5, agent-aware) states.
8. ✓ Faces + motion + width fallback + per-state color (§4, §5); §4.3 working face resolved.
9. ~~Catch-up tail~~ — **removed** (§5.3); the slot is now "no third line."
10. ☐ Plugin packaging + per-platform binary selection (§11) + README + limitations.
11. ☐ Tier-2/3 error diagnosis (§10), post-v1.

Each step is independently testable with mocked stdin + a captured real transcript fixture; the as-built has unit tests in `internal/state` and `internal/render`.

---

## Appendix A — Transcript probe results (2026-06-02, CC v2.1.160, Windows 11)

Measured against a live session. Basis for §6.

- **A) In-turn flush:** completed `tool_result` entries land within ~1–2s, mid-turn, not gated on turn end. Confirmed by subagent completion visible in the next bash read ~1s later; WRITE/PASS/FAIL all present while the turn was still open.
- **A-lag) `tool_use` trails:** assistant `tool_use` (e.g. `Task` spawn) flushes ~one inference later than its result. Derive from results, not the latest `tool_use`.
- **B) Pass/fail:** `tool_result.is_error` cleanly distinguishes pass(0)/fail(≠0). No numeric exit-code field; failure text begins `"Exit code N"`. Pass `toolUseResult` is an object `{stdout, stderr, interrupted, isImage, noOutputExpected}`; fail `toolUseResult` is a string `"Error: Exit code 1\n..."`.
- **C) Turn boundaries:** user prompt = `type=="user"`, `message.content` string, no `toolUseResult` (`userType:"external"`, first `parentUuid:null`). Tool results = `type=="user"` with `toolUseResult` + `content` array of `tool_result`. Entries carry `timestamp`, `uuid`, `parentUuid`, `promptId`.
- **D) Edited files:** Write/Edit/MultiEdit `tool_use.input.file_path` (and `.input.content`) exposed directly.
- **E) Subagents:** spawn = `Task` `tool_use` (`.input.subagent_type`, `.input.prompt`); completion = `tool_result` with `content` `[{text:"DONE"},{text:"agentId: ..."}]`. ⚠️ **CORRECTED 2026-06-02 (§6.4):** this probe found no sidechain file, but current CC **does** write `<session-id>/subagents/agent-<id>.jsonl` (`isSidechain:true`). That file is now the primary running-agent signal; the "boundary-only" main-transcript view here is incomplete. Internals still not surfaced (binary running/done).
- **F) Path:** `~/.claude/projects/<encoded-cwd>/<session-id>.jsonl`; encoded-cwd `D:\claude\ccbit` → `D--claude-ccbit` (`:` and `\` → `-`). Each entry carries `cwd` + `sessionId` verbatim; resolve via stdin `transcript_path`/`session_id` rather than re-encoding.
- **Verdict:** GO. Transcript-reading status line with no hooks is viable; key off `tool_result` (immediate), treat trailing `tool_use` as soft, subagents as binary running/done.
