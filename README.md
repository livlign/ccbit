# ccbit

[![ci](https://github.com/livlign/ccbit/actions/workflows/ci.yml/badge.svg)](https://github.com/livlign/ccbit/actions/workflows/ci.yml)

A session-awareness status line for [Claude Code](https://claude.com/claude-code), led by a stateful kaomoji face named **Bit**. On every render Bit reads the session transcript, works out what's happening, and narrates it in a line or two — like a small secretary sitting on the bottom row of your terminal.

![ccbit — a status line that reads your session. One coding turn narrated by Bit: working in cyan, a build failure with a table-flip face, recovery with "Build green again.", a done summary with files edited and tests passed, idle with alerts from other sessions — ending on all 8 states: working, agents running, waiting on you, failed, done, done-recovered, stopped, idle.](docs/hero.gif)

ccbit answers, at a glance: *is anything working, done, waiting, broken, or stopped here — what just changed, and what's happening in my other sessions?*

```
(つ•‿•)つ 4 files edited, line changes: +885/-99. Build succeeded. Tests succeeded.
~/ccbit · main +1 ~2 -1 ↑1 · Opus 4.8 (high) · ctx 38% ↑ · 5h 3% (4h37m) · 7d 0% (6d20h)
```

It is a single Go binary. **No hooks, no daemons.** The transcript is the source of truth; Claude Code already writes it and owns its lifecycle. ccbit only reads it (plus two small, disposable state dirs of its own — see [How it works](#how-it-works)).

## Install

Requires Claude Code ≥ v2.1.153 (for the `COLUMNS` width hint). No Go toolchain needed — this fetches a prebuilt binary (macOS/Linux, amd64/arm64) and configures the status line:

```sh
curl -fsSL https://raw.githubusercontent.com/livlign/ccbit/main/install.sh | bash
```

The installer verifies the downloaded binary's SHA-256 against the release's `checksums.txt` before installing, and refuses to install on a mismatch.

Open a new Claude Code session to see Bit. A settings file may only define **one** `statusLine`; if you already have one, the installer replaces it (your previous `settings.json` is backed up first).

On Windows, grab `ccbit_windows_amd64.zip` (or `arm64`) from the [latest release](https://github.com/livlign/ccbit/releases/latest) and point your status line at the extracted `ccbit.exe`.

<details>
<summary>Manual setup / build from source</summary>

Building from source requires Go 1.26+:

```sh
go build -o ~/.claude/ccbit/ccbit ./cmd/ccbit
```

Then point your status line at it in `~/.claude/settings.json`:

```json
{
  "statusLine": {
    "type": "command",
    "command": "~/.claude/ccbit/ccbit",
    "refreshInterval": 1
  }
}
```

</details>

## What Bit says

### Line 1 — the reactive line

Bit's face maps to the session state (first match wins, in priority order), and the text recaps what's going on. The whole line is colored by state.

| Priority | State | Face | When |
|---|---|---|---|
| 1 | Stopped | `(¬°-°)¬` | a quiet open turn with a dispatched tool that never answered (hang or unnoticed permission prompt), or silence past any plausible thinking stretch (~15m) |
| 2 | Failed | <code>(╯°□°)╯︵&nbsp;┻━┻</code> | the latest build/test errored — Bit appends the concrete reason when the output gives one up (a compiler location, a named failing test, a known signature) |
| 3 | Waiting on you | `(◕_◕)?` | a question or plan is awaiting your answer |
| 4 | Agents running | <code>┏(•&#95;•)┛&nbsp;⇄&nbsp;┗(•&#95;•)┓</code> | subagents are in flight |
| 5 | Working | <code>\(•&#95;•)/&nbsp;⇄&nbsp;/(•&#95;•)\</code> … | a turn is in progress; the working face is a hand gesture picked per turn (raise-the-roof, pumping, flexing, …) that animates between two frames. Long silent stretches with nothing pending show as `thinking (2m31s)` rather than decaying into Stopped |
| 6 | Done (recovered) | `(→_←")` | idle, and a build/test passed this turn after an earlier failure |
| 7 | Done | `(つ•‿•)つ` | the turn finished having edited, committed/pushed/deployed, or passed a build/test |
| 8 | Idle | `(•_•)` `(°.°)` `(◕_◕)` … | nothing else applies (e.g. a turn that only read or answered). The idle face rotates per turn through a small set of resting moods — picked deterministically from the turn, so it's steady through a turn's repaints and just varies turn to turn |

Bit recaps in plain sentences rather than a row of glyphs:

```
(つ•‿•)つ 4 files edited, line changes: +885/-99. Build succeeded. Tests succeeded.
(→_←")  2 files edited. Build green again.
-(๏_๏)- editing ccbit (3 files) · 1/4 todos · 2m14s
(╯°□°)╯︵ ┻━┻ ccbit build failed · internal/render/render.go:42:3: undefined: foo
(¬°-°)¬ stopped · last: editing render.go · 2m ago
```

Motion exists only in Working and Agents — a 2-frame swap on a ~2s wall-clock cycle, time-derived so concurrent repaints never jitter. The Working and Idle faces are also assembled per turn from shared parts — a pool of eyes and a per-state pool of hands — so which gesture (or resting pose) you see is picked once per turn and held steady through that turn's repaints, varying turn to turn. The felt liveness during a long turn comes from the **numbers** ticking and the gesture animating, not from the face changing under you.

### Other sessions

When you run several sessions at once, Bit speaks up about the others on line 1 — by their session title, so you know which window to switch to:

```
(•_•) idle · The session "Read and review project" has new updates — take a look
(•_•) idle · Elsewhere: api crashed, web needs you
(•_•) idle · 3 other sessions running
```

- A session that **just finished** a turn is flagged with a nudge (it clears when you switch over and prompt it, and stops nagging after a while).
- A session that **crashed**, **needs you**, or **stalled** is named with its state.
- Several merely-busy sessions become a light count; a single idle one isn't mentioned at all.

Line 1 can only afford a fragment. For the full picture, run `ccbit sessions` in any shell — it prints every live session as a table, the actionable ones first:

```
$ ccbit sessions
STATE      AGE  PROJECT  SESSION
failed     2m   api      Fix login bug
needs you  5s   web      Redesign nav
working    1s   ccbit    Refactor parser
idle       30s  docs     —
```

Same data, no new source — just a full read of the heartbeats line 1 hints at. Add `--json` to pipe it.

To see every face before a real session happens to hit each state, run `ccbit demo` — it prints Bit in all eight states from synthetic data (reads nothing). `ccbit version` and `ccbit help` round out the CLI.

### Line 2 — the ambient line

```
~/ccbit · main +1 ~2 -1 ↑1 · Opus 4.8 (high) · ctx 38% ↑ · 5h 3% (4h37m) · 7d 0% (6d20h)
```

Current directory, git branch, model (with its reasoning effort), context-window usage, and rate-limit windows with their reset countdowns. The git segment breaks the worktree down as `+new ~modified -deleted` (each shown only when nonzero), followed by `↑ahead ↓behind`. `ctx%` colors only when it warrants attention (≥70 yellow, ≥90 red) and carries a velocity arrow — `↑` while context is climbing, `↓` after a `/compact`. Several segments have optional color/icon upgrades — see [Configuration](#configuration).

## Bit gets smarter over time

ccbit keeps a small, **numbers-only** memory per project (no prompt text is ever stored). It folds each completed turn into a couple of moving averages and uses them to move past one-size-fits-all rules:

- **Learned stall threshold.** "Stopped" is no longer a fixed timer — it adapts to how long *this* project's turns normally pause. A repo with slow builds stops falsely reading as stalled; a snappy one flags a hang sooner. (Still overridable with `CCBIT_STALL`.)
- **Subtle personality.** A red→green recovery reads `Build green again.` rather than a flat status.

Everything stays silent until there's enough history to be trustworthy — a wrong insight costs more than a missing one. Memory is disposable: delete `~/.claude/ccbit/memory/` and ccbit falls back to its fixed defaults.

## Configuration

| Var | Default | Meaning |
|---|---|---|
| `CCBIT_STALL` | learned (≈45–300s) | seconds of inactivity before an open turn reads as Stopped; an explicit value overrides the learned one |
| `NO_COLOR` | unset | set to disable all ANSI color |
| `COLUMNS` | — | width hint; below ~60 columns, risky wide glyphs fall back to ASCII-safe faces |

### Visual features (optional, opt-in)

Off by default and set independently — none can be safely auto-detected (Nerd Font glyphs show as tofu without a patched font; color and gauges are a matter of taste). Set any to `1` to enable.

| Var | Effect |
|---|---|
| `CCBIT_NERD_FONT` | Nerd Font glyphs for git change marks — plus / pencil / trash instead of `+ ~ -` (requires a patched Nerd Font) |
| `CCBIT_ICONS` | a leading Nerd Font icon on each ambient segment — folder, branch, chip, gauge, clock (requires a patched Nerd Font) |
| `CCBIT_GIT_COLOR` | color the git change marks: green new, yellow modified, red deleted |
| `CCBIT_CTX_GAUGE` | a small fill bar beside the context percentage (`ctx ▆▆▁▁▁ 38%`) |
| `CCBIT_RATE_COLOR` | escalate the 5h / 7d rate-limit meters to yellow (≥70%) then red (≥90%), like `ctx%` |
| `CCBIT_AMBIENT_COLOR` | wash the ambient line in a smooth cyan→violet→pink gradient instead of leaving it plain grey (`1`/`gradient`); use `segments` for a flat accent hue per segment instead. Either way the pressure colors on `ctx%` and rate still punch through |

You don't have to memorize these: while idle and with nothing else to say, Bit quietly rotates a one-line hint for whichever features are still off, then goes quiet again — so they surface on their own.

```
(•_•)旦 idle · tip: set CCBIT_GIT_COLOR=1 to color git changes
```

### Custom error signatures (optional)

When a build fails, Bit shows the first concrete reason it can extract from the output. To map a recurring failure to your own message, create `~/.config/ccbit/error-signatures` (or `$XDG_CONFIG_HOME/ccbit/error-signatures`) with one `pattern⇥message` per line — a case-insensitive regular expression, a **tab**, then the message. A matching signature wins over the built-in extraction; `#` lines are comments.

```
# pattern<TAB>message
401|codeartifact	renew AWS SSO for codeartifact
no space left on device	disk full — clear build cache
```

## How it works

```
Claude Code ──(stdin JSON: transcript_path, model, context, rate_limits, cost)──► ccbit (Go binary)
                                                                                       │
                                          reads ──► session transcript (.jsonl, last 2 MiB)
                                          r/w   ──► ~/.claude/ccbit/sessions/  (heartbeats: cross-session awareness)
                                          r/w   ──► ~/.claude/ccbit/memory/    (per-project learned aggregates)
                                                                                       ▼
                                                                       derive state ──► print 2 lines ──► exit
```

Each invocation reads stdin, tail-reads the transcript (bounded at 2 MiB so cost stays flat on long sessions), derives the state, and prints. Two small on-disk stores let it do what a single transcript can't:

- **Heartbeats** are ephemeral per-session files. Each session writes its own state every render and reads its siblings' — that's how Bit knows another window crashed or finished. They carry the session's title and a rolling window of `ctx%` samples (for the velocity arrow), and self-expire when a session goes quiet.
- **Memory** is durable per-project aggregates (typical turn duration and in-turn gaps), updated once per completed turn via a per-session high-water mark so the per-second renders never double-count.

Both are disposable optimizations, never authoritative: delete either dir and ccbit keeps working on fixed defaults.

## Layout

```
ccbit/
  cmd/ccbit/main.go            # entry: parse stdin → read transcript → derive → record → render
  internal/
    input/      stdin.go       # parse the status-line JSON (model, ctx%, rate limits, cost)
    transcript/ transcript.go  # tail-read + parse the JSONL; ai-title capture
                turns.go       # segment entries into turns; builds/edits/agents/gaps
                subagents.go   # running-agent detection from the subagents sidechain
    state/      state.go       # derive one of 8 states by fixed priority
    sessions/   sessions.go    # heartbeats: siblings, completion nudges, ctx velocity, titles
    memory/     memory.go      # durable per-project learning (numbers only)
    render/     render.go      # state + signals → the two printed lines
  docs/                        # PRD v1 / v2, debugging notes
```

## Known limitations

1. **Single-window only by design.** ccbit informs you about other sessions *within the window you're looking at*; it never reaches outside it (no OS notifications).
2. **Build/test signal is exit-code only.** The line says succeeded/failed, never counts or reasons. Detection matches a fixed list of known tools: build/test subcommands of common toolchains (`go`, `cargo`, `dotnet`, `npm`/`pnpm`/`yarn`/`bun`, `mvn`, `gradle`, `bazel`, `make`/`just`, …) plus standalone runners (`pytest`, `jest`, `vitest`, `eslint`, `tsc`, …). Dev-loop commands (`dotnet run`, `npm start`, `make serve`, `--watch`) and anything unrecognized never count — fail-safe to Idle.
3. **Learned values need history.** Per-project insights stay silent until they have enough samples; brand-new projects get the fixed defaults.
4. **Running-agent count is inferred**, not measured.
5. **Width fallbacks are heuristic** (`COLUMNS`-based); terminals that don't set `COLUMNS` are assumed wide.
```
