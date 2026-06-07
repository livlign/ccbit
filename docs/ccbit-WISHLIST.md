## `ccbit sessions` subcommand

Standalone command that prints the full roster of live sessions, not just the
width-squeezed inline hint on line 1.

**Why:** cross-session awareness is ccbit's differentiator vs. other statuslines.
Line 1 can only afford a terse fragment ("3 other sessions running"); the heartbeat
dir already holds every live session's state + title, so a full roster is near-free.

**Shape (draft):** `ccbit sessions` reads ~/.claude/ccbit/sessions/, prints one row
per live heartbeat — state, session title, last-activity age. Sorted by state priority
(crashed/needs-you first). No new data source: pure read of existing heartbeats.

**Open questions:**
- output format — plain table vs. machine-readable (--json) for piping
- does it filter to live-only, or show recently-expired with a dim style
- invoked manually, or also bindable to a Claude Code slash command / shell alias

**On-strategy:** no hooks, no daemons, transcript/heartbeat read only. Deepens the moat
rather than widening surface area.
