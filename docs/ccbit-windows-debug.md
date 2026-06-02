# ccbit on Windows — debugging log & install guide

A full account of getting ccbit (status line + hooks) working on Windows 11, the
dead-ends hit, the root causes found, and the final known-good configuration.

Date: 2026-06-02. Machine: Windows 11 Pro, Claude Code v2.1.160.

---

## TL;DR — what actually works

1. **Install jq** (every ccbit script needs it): `winget install jqlang.jq`. It lands on
   the persistent user PATH, so freshly-started CC sessions find it.
2. **Convert all scripts to LF.** A Windows `git` checkout with `autocrlf` gives the `.sh`
   files CRLF line endings, which break bash. `sed -i 's/\r$//' *.sh hooks/*.sh`.
3. **Pin Claude Code to the *direct* Git Bash** in `~/.claude/settings.json`:
   ```json
   "env": { "CLAUDE_CODE_GIT_BASH_PATH": "C:\\Program Files\\Git\\usr\\bin\\bash.exe" }
   ```
   Use `usr\bin\bash.exe` (the real 2.4 MB bash), **NOT** `bin\bash.exe` (a 47 KB launcher).
   This is the single most important finding — see [The hook hang](#the-hook-hang).
4. **Use plain tilde commands** for statusLine and hooks (`~/.claude/ccbit/statusline.sh`),
   never a wrapped `"C:\...\bash.exe" "...sh"` string.
5. **Enabling hooks must be done from a *freshly restarted* session** (see
   [The enable trap](#the-enable-trap)).

Final settings.json shape is in [Known-good configuration](#known-good-configuration).

---

## How Claude Code runs statusLine/hook commands on Windows

- CC executes the `command` string **through Git Bash** (effectively `bash -c "<command>"`),
  picking the first git-for-windows bash it finds on PATH unless overridden by
  `CLAUDE_CODE_GIT_BASH_PATH` (read once at session startup — changing it requires a restart).
- The session JSON is piped to the command on **stdin**; the command prints to **stdout**.
- Settings hot-reload: the status line refreshes on the next interaction, and hook config is
  re-read into already-running sessions (this matters a lot — see the enable trap).

---

## The dead-ends (in order), and what each taught us

### 1. `jq` missing
Every script (`statusline.sh` + all hooks) parses the stdin JSON with `jq`. Without it,
scripts ran but produced degraded/empty output. Fix: `winget install jqlang.jq`.

### 2. The "wrap it in bash.exe" mistake
First attempt set the command to:
```
"C:\Program Files\Git\bin\bash.exe" "C:/Users/.../statusline.sh"
```
This **fails silently**. CC already runs the command *inside* Git Bash, so this double-wraps.
Worse, Git Bash treats the backslashes in `C:\Program Files\...` as escape characters; the
path collapses to `C:Program: command not found` (exit 127) and the status line goes blank.

Verified directly:
```
$ bash -c '"C:\Program Files\Git\bin\bash.exe" "C:/Users/.../statusline.sh"'
FilesGitbinbash.exe ...: line 1: C:Program: command not found   # exit 127
$ bash -c '~/.claude/ccbit/statusline.sh'
(•_•) idle                                                       # exit 0  ✓
```
**Lesson:** use the plain tilde/forward-slash command. Don't wrap it.

### 3. CRLF line endings
`file` reported `CRLF line terminators` on every script. Under bash, `exit 0\r` is not a
valid exit code and every line carries a trailing `\r`, so hooks failed non-zero with
"No stderr output". Fix: `sed -i 's/\r$//'` on all `.sh` files. (Add a `.gitattributes`
forcing `*.sh eol=lf` to prevent regression on re-checkout/re-copy.)

### 4. Wrong bash auto-picked (cmder)
`claude --debug` showed:
```
[DEBUG] Using bash path: "C:\Users\...\Downloads\cmder\vendor\git-for-windows\usr\bin\bash.exe"
```
CC was using **cmder's bundled bash** because cmder's vendor dir sits earlier on PATH.
Solution is to override it with `CLAUDE_CODE_GIT_BASH_PATH` (next section also covers *which*
bash to point at).

### 5. The hook hang
<a name="the-hook-hang"></a>
After overriding to `C:\Program Files\Git\bin\bash.exe`, the **status line worked but hooks
HUNG** — CC showed `running stop hook · 54s`, then up to ~10 min (CC's 600s command-hook
timeout). It froze every session, because hook config hot-reloads into live sessions too.

A first guide consult suggested this was an unfixable architectural CC bug (Windows hooks
get stdin as a TTY, never EOF; subprocess stdio deadlock; per-hook `timeout` ignored).
A **bounded diagnostic** Stop hook using `timeout 2 cat` (caps stdin read at 2s) was added —
and it *still* hung ~10 min and **never even created its log file**, i.e. the script body
never ran. That seemed to confirm "below the script, unfixable."

**That conclusion was wrong.** The real cause was found by comparing the two bash binaries:

| Binary | Size | What it is |
|---|---|---|
| `C:\Program Files\Git\bin\bash.exe` | **47 KB** | a thin **launcher** that spawns the real bash as a child |
| `C:\Program Files\Git\usr\bin\bash.exe` | **2.4 MB** | the **real** bash |

The `bin\bash.exe` launcher spawns `usr\bin\bash.exe` as a **grandchild** of CC. CC pipes the
hook's stdin into the launcher and waits for the pipe to close; the grandchild inherits and
holds the pipe handle, the launcher exits, and CC waits **forever** for an EOF that never
comes → the hang. The timeline fit exactly: hooks only began *hanging* (vs merely erroring)
the moment the override switched to `bin\bash.exe`. The earlier cmder runs used a *direct*
`usr\bin\bash.exe` and never hung (they only errored on CRLF).

**Fix:** point the override at the **direct** bash:
```json
"env": { "CLAUDE_CODE_GIT_BASH_PATH": "C:\\Program Files\\Git\\usr\\bin\\bash.exe" }
```

### Proof the fix works
A bounded Stop diagnostic, run in a **freshly restarted** session on `usr\bin\bash.exe`,
logged:
```
=== start epoch=1780371278 bash=/usr/bin/bash ===
stdin_is_tty=no(pipe)        <- proper pipe, NOT a TTY
stdin_bytes=1565             <- full JSON arrived intact
stdin_raw<<{"session_id":"...","hook_event_name":"Stop", ...}>>
=== end epoch=1780371278 === <- same second: instant, NO hang
```
So on Windows, with the direct bash: stdin is a real pipe, the full JSON is delivered, and
the hook completes instantly. **Hooks are fully viable.** The "TTY / unfixable" theory did not
hold up against direct measurement — the launcher wrapper was the whole problem.

Bonus discovered in that payload: CC also exposes hook context as env vars
(`CLAUDE_CODE_SESSION_ID`, `CLAUDE_PROJECT_DIR`, `CLAUDE_EFFORT`, `CLAUDE_CODE_ENTRYPOINT`),
a stdin-free fallback if ever needed.

### 6. The enable trap
<a name="the-enable-trap"></a>
Even with the correct direct-bash override in settings, enabling hooks from an
**already-open** session hangs it. Reason: `CLAUDE_CODE_GIT_BASH_PATH` is read only at session
startup, but hook *config* hot-reloads into live sessions. A session opened **before** the
override is still running on the old launcher bash; the instant hooks activate (including its
own turn-end `Stop` hook), it deadlocks.

**Enable procedure that avoids the hang:**
1. Set the `usr\bin` override in settings.json.
2. **Fully close every Claude Code window.**
3. Open **one fresh session** (now on the direct bash).
4. **Only then** add the `hooks` block — ideally from within that fresh session.

Rule of thumb going forward: *if a hook ever hangs again, it's a stale session opened before
the override — just close it.* New sessions are fine.

---

## Known-good configuration
<a name="known-good-configuration"></a>

`~/.claude/settings.json` (relevant keys):

```json
{
  "env": {
    "CLAUDE_CODE_GIT_BASH_PATH": "C:\\Program Files\\Git\\usr\\bin\\bash.exe"
  },
  "statusLine": {
    "type": "command",
    "command": "~/.claude/ccbit/statusline.sh",
    "refreshInterval": 1,
    "padding": 1
  },
  "hooks": {
    "UserPromptSubmit": [
      { "hooks": [ { "type": "command", "command": "~/.claude/ccbit/hooks/turn-start.sh" } ] }
    ],
    "PostToolUse": [
      { "matcher": "Edit|Write|MultiEdit", "hooks": [ { "type": "command", "command": "~/.claude/ccbit/hooks/files.sh" } ] },
      { "matcher": "Bash", "hooks": [ { "type": "command", "command": "~/.claude/ccbit/hooks/bash.sh" } ] },
      { "matcher": "Task", "hooks": [ { "type": "command", "command": "~/.claude/ccbit/hooks/agent-spawn.sh" } ] },
      { "matcher": "AskUserQuestion|ExitPlanMode", "hooks": [ { "type": "command", "command": "~/.claude/ccbit/hooks/wait.sh" } ] }
    ],
    "PreToolUse": [
      { "hooks": [ { "type": "command", "command": "~/.claude/ccbit/hooks/activity.sh" } ] },
      { "matcher": "AskUserQuestion|ExitPlanMode", "hooks": [ { "type": "command", "command": "~/.claude/ccbit/hooks/wait.sh" } ] }
    ],
    "SubagentStop": [
      { "hooks": [ { "type": "command", "command": "~/.claude/ccbit/hooks/agent-done.sh" } ] }
    ],
    "Stop": [
      { "hooks": [ { "type": "command", "command": "~/.claude/ccbit/hooks/turn-end.sh" } ] }
    ],
    "SessionEnd": [
      { "hooks": [ { "type": "command", "command": "~/.claude/ccbit/hooks/cleanup.sh" } ] }
    ]
  }
}
```

Notes:
- The `command` strings are plain tilde paths (no `bash.exe` wrapper, no backslashes).
- jq must be installed and on PATH.
- All `~/.claude/ccbit/*.sh` files must be LF.

---

## Recommended installer changes (for ccbit itself)

The repo's `install.sh` is POSIX/Unix-oriented and was applied manually here. To make ccbit
Windows-friendly out of the box, consider:

1. **Ship `.sh` files as LF and enforce it** — add `.gitattributes` with `*.sh text eol=lf`.
2. **Document the jq requirement** with the `winget install jqlang.jq` one-liner.
3. **Document `CLAUDE_CODE_GIT_BASH_PATH`** must point at `usr\bin\bash.exe` (the direct bash),
   and warn explicitly against `bin\bash.exe` (the launcher causes the hook-stdin deadlock).
4. **Document the enable order** (override → full restart → then add hooks), since hot-reload
   into a stale session is a footgun.

---

## Quick verification commands (run in Git Bash, jq on PATH)

```bash
# 1. line endings (must be LF)
for f in ~/.claude/ccbit/statusline.sh ~/.claude/ccbit/hooks/*.sh; do
  grep -lU $'\r' "$f" >/dev/null 2>&1 && echo "CRLF: $f" || echo "LF:   $f"
done

# 2. settings.json is valid JSON and points at the direct bash
jq -r '.env.CLAUDE_CODE_GIT_BASH_PATH, .statusLine.command' ~/.claude/settings.json

# 3. render the status line exactly as CC does (direct bash, bash -c, JSON on stdin)
JSON='{"session_id":"t","workspace":{"current_dir":"D:/x"},"model":{"display_name":"Opus"},"context_window":{"used_percentage":12}}'
echo "$JSON" | COLUMNS=120 "C:/Program Files/Git/usr/bin/bash.exe" -c '~/.claude/ccbit/statusline.sh'
# expect:  (•_•) idle  /  D:/x · Opus · ctx 12%   (exit 0)
```
