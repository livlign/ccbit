package render

import (
	"strings"
	"time"

	"github.com/livlign/ccbit/internal/sessions"
	"github.com/livlign/ccbit/internal/state"
)

// Roster renders the full live-session list for the `ccbit sessions` subcommand:
// one row per sibling heartbeat as an aligned table, actionable states first.
// beats is taken as-is from sessions.Active, which already filters to live
// sessions and sorts them (failed, stopped, waiting, then the rest).
func Roster(beats []sessions.Beat, now time.Time, colorOn bool) []string {
	if len(beats) == 0 {
		return []string{"No live Claude Code sessions."}
	}

	type row struct{ status, age, project, session string }
	header := row{"STATE", "AGE", "PROJECT", "SESSION"}
	rows := make([]row, len(beats))
	for i, b := range beats {
		status := rosterStatus(b.State)
		if sessions.Stale(b, now) {
			// The heartbeat has stopped advancing — the session's terminal almost
			// certainly closed. Don't let a frozen "idle" pose as a live one; say so.
			status = "stale"
		}
		rows[i] = row{
			status:  status,
			age:     fmtAge(now.Sub(time.Unix(b.UpdatedAt, 0))),
			project: dash(b.Project),
			session: dash(b.Title),
		}
	}

	// Column widths from the plain (uncolored) text; the trailing SESSION column
	// is never padded.
	w := []int{len(header.status), len(header.age), len(header.project)}
	for _, r := range rows {
		w[0] = max(w[0], len(r.status))
		w[1] = max(w[1], len(r.age))
		w[2] = max(w[2], len(r.project))
	}

	out := []string{rosterLine(header, w, "")}
	for i, r := range rows {
		col := ""
		if colorOn {
			if r.status == "stale" {
				col = dim
			} else {
				col = line1Color(stateFromString(beats[i].State))
			}
		}
		out = append(out, rosterLine(r, w, col))
	}
	return out
}

// rosterLine lays out one table row. The STATE cell is colored (when statusColor
// is set) but padded by its plain width so columns still align under the color
// codes.
func rosterLine(r struct{ status, age, project, session string }, w []int, statusColor string) string {
	status := r.status
	if statusColor != "" {
		status = colorize(status, statusColor)
	}
	var b strings.Builder
	b.WriteString(status)
	b.WriteString(strings.Repeat(" ", w[0]-len(r.status)+2))
	b.WriteString(r.age)
	b.WriteString(strings.Repeat(" ", w[1]-len(r.age)+2))
	b.WriteString(r.project)
	b.WriteString(strings.Repeat(" ", w[2]-len(r.project)+2))
	b.WriteString(r.session)
	return b.String()
}

// rosterStatus is the human word for a heartbeat state in the roster — the same
// vocabulary line 1 uses for siblings ("stalled", "needs you"), extended to the
// benign states a full roster also lists.
func rosterStatus(s string) string {
	switch s {
	case "failed":
		return "failed"
	case "stopped":
		return "stalled"
	case "waiting":
		return "needs you"
	case "working":
		return "working"
	case "agents":
		return "agents"
	case "done", "redeemed":
		return "done"
	default:
		return "idle"
	}
}

// stateFromString maps a heartbeat's state string back to a state.State so the
// roster can reuse line1Color across every state (siblingState only covers the
// actionable ones).
func stateFromString(s string) state.State {
	switch s {
	case "working":
		return state.Working
	case "agents":
		return state.Agents
	case "done":
		return state.DoneNormal
	case "redeemed":
		return state.DoneRedeemed
	case "waiting":
		return state.Waiting
	case "failed":
		return state.Failed
	case "stopped":
		return state.Stopped
	default:
		return state.Idle
	}
}

func dash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
