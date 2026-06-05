// Package transcript reads and parses a Claude Code session transcript (JSONL)
// into a typed model: user prompts, assistant actions, and tool results.
//
// The transcript is the source of truth (there are no hooks). Only the tail is
// read per render — see ReadTail — which bounds cost on long sessions while
// always capturing the current turn and the last few completed turns.
package transcript

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"time"
)

// TailBytes bounds how much of the transcript is read per render. It is large
// enough to capture the current turn plus the last several turns (catch-up caps
// at 5 turns), and small enough to parse in a few milliseconds.
const TailBytes = 2 << 20 // 2 MiB

type Kind int

const (
	KindOther Kind = iota
	KindUserPrompt
	KindAssistant
	KindToolResult
	KindTitle // ai-title sidecar: the session's human-readable name
)

// ToolUse is a single tool invocation recorded on an assistant entry.
// Assistant entries (and thus ToolUses) trail their results by ~one inference,
// so derive state from results and treat tool_use presence as soft.
type ToolUse struct {
	ID           string
	Name         string
	FilePath     string // Edit/Write/MultiEdit/NotebookEdit
	Command      string // Bash
	SubagentType string // Task
	Subject      string // TaskCreate
	ActiveForm   string // TaskCreate
	TaskID       string // TaskUpdate
	Status       string // TaskUpdate / TaskCreate
}

// Entry is one parsed transcript line reduced to the fields ccbit needs.
type Entry struct {
	Kind       Kind
	Time       time.Time
	HasTime    bool
	StopReason string // assistant: "end_turn" closes a turn; "tool_use" means more is coming
	Model      string
	PromptID   string
	Title      string // KindTitle: the session's ai-title

	// assistant
	ToolUses []ToolUse

	// tool result
	ToolUseID      string
	IsError        bool
	ResultText     string
	ResultCommand  string // command of the originating Bash tool_use, if correlated
	ResultToolName string // name of the originating tool_use, if correlated
	IsBash         bool
}

type rawEntry struct {
	Type          string          `json:"type"`
	Timestamp     string          `json:"timestamp"`
	PromptID      string          `json:"promptId"`
	AiTitle       string          `json:"aiTitle"`
	Message       json.RawMessage `json:"message"`
	ToolUseResult json.RawMessage `json:"toolUseResult"`
}

type rawMessage struct {
	Role       string          `json:"role"`
	Model      string          `json:"model"`
	StopReason *string         `json:"stop_reason"`
	Content    json.RawMessage `json:"content"`
}

type rawBlock struct {
	Type      string          `json:"type"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ID        string          `json:"id"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   *bool           `json:"is_error"`
	Text      string          `json:"text"`
	Content   json.RawMessage `json:"content"`
}

type rawInput struct {
	FilePath     string `json:"file_path"`
	Command      string `json:"command"`
	SubagentType string `json:"subagent_type"`
	// TaskCreate / TaskUpdate (the session's plan, replayed by Tasks)
	Subject    string `json:"subject"`
	ActiveForm string `json:"activeForm"`
	TaskID     string `json:"taskId"`
	Status     string `json:"status"`
}

// ReadTail opens path, seeks to the last TailBytes, and parses each JSONL line
// into Entry values in file order. Malformed or non-essential lines are skipped.
// Returns the entries; on open/stat error returns the error so the caller can
// fall back to a neutral render.
func ReadTail(path string) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	var start int64
	if fi.Size() > TailBytes {
		start = fi.Size() - TailBytes
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return nil, err
	}

	r := bufio.NewReaderSize(f, 1<<20)
	if start > 0 {
		// Drop the partial line at the seek point; the next full line is the start.
		_, _ = r.ReadBytes('\n')
	}
	return Parse(r), nil
}

// Parse reads JSONL entries from r in file order, skipping malformed and
// non-essential lines. Exposed for testing; ReadTail wraps it with tail-seeking.
func Parse(r io.Reader) []Entry {
	br := bufio.NewReaderSize(r, 1<<20)
	var entries []Entry
	uses := map[string]ToolUse{} // tool_use id -> use, for correlating results
	for {
		line, rerr := br.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			if e, ok := parseLine(line, uses); ok {
				entries = append(entries, e)
			}
		}
		if rerr != nil {
			break
		}
	}
	return entries
}

func parseLine(line []byte, uses map[string]ToolUse) (Entry, bool) {
	var re rawEntry
	if err := json.Unmarshal(line, &re); err != nil {
		return Entry{}, false
	}

	e := Entry{PromptID: re.PromptID}
	if t, err := time.Parse(time.RFC3339, re.Timestamp); err == nil {
		e.Time = t
		e.HasTime = true
	}

	switch re.Type {
	case "user":
		if hasToolUseResult(re.ToolUseResult) {
			e.Kind = KindToolResult
			fillToolResult(&e, re, uses)
			return e, true
		}
		if isStringContent(re.Message) {
			e.Kind = KindUserPrompt
			return e, true
		}
		return Entry{}, false
	case "assistant":
		e.Kind = KindAssistant
		fillAssistant(&e, re, uses)
		return e, true
	case "ai-title":
		if re.AiTitle == "" {
			return Entry{}, false
		}
		e.Kind = KindTitle
		e.Title = re.AiTitle
		return e, true
	default:
		// Metadata sidecar entries (last-prompt, ai-title, mode, permission-mode,
		// snapshot, attachments, ...) carry no turn signal.
		return Entry{}, false
	}
}

func fillAssistant(e *Entry, re rawEntry, uses map[string]ToolUse) {
	var m rawMessage
	if err := json.Unmarshal(re.Message, &m); err != nil {
		return
	}
	if m.StopReason != nil {
		e.StopReason = *m.StopReason
	}
	e.Model = m.Model
	for _, b := range decodeBlocks(m.Content) {
		if b.Type != "tool_use" {
			continue
		}
		var in rawInput
		_ = json.Unmarshal(b.Input, &in)
		tu := ToolUse{
			ID:           b.ID,
			Name:         b.Name,
			FilePath:     in.FilePath,
			Command:      in.Command,
			SubagentType: in.SubagentType,
			Subject:      in.Subject,
			ActiveForm:   in.ActiveForm,
			TaskID:       in.TaskID,
			Status:       in.Status,
		}
		e.ToolUses = append(e.ToolUses, tu)
		if b.ID != "" {
			uses[b.ID] = tu
		}
	}
}

func fillToolResult(e *Entry, re rawEntry, uses map[string]ToolUse) {
	for _, b := range decodeBlocks(messageContent(re.Message)) {
		if b.Type != "tool_result" {
			continue
		}
		e.ToolUseID = b.ToolUseID
		if b.IsError != nil && *b.IsError {
			e.IsError = true
		}
		e.ResultText = blockText(b.Content)
		break
	}

	// Correlate to the originating tool_use (present in file order once the
	// trailing assistant message has flushed). Gives us command + tool name.
	if tu, ok := uses[e.ToolUseID]; ok {
		e.ResultCommand = tu.Command
		e.ResultToolName = tu.Name
		if tu.Name == "Bash" || tu.Command != "" {
			e.IsBash = true
		}
	}
	// Shape-based fallback: a Bash result's toolUseResult is an object with
	// stdout/stderr; failures serialize it as a plain string.
	if !e.IsBash && looksLikeBashResult(re.ToolUseResult) {
		e.IsBash = true
	}
	if e.ResultText == "" {
		var s string
		if json.Unmarshal(re.ToolUseResult, &s) == nil {
			e.ResultText = s
		}
	}
}

func decodeBlocks(content json.RawMessage) []rawBlock {
	c := bytes.TrimSpace(content)
	if len(c) == 0 || c[0] != '[' {
		return nil
	}
	var blocks []rawBlock
	if json.Unmarshal(c, &blocks) != nil {
		return nil
	}
	return blocks
}

func messageContent(msg json.RawMessage) json.RawMessage {
	var m rawMessage
	if json.Unmarshal(msg, &m) != nil {
		return nil
	}
	return m.Content
}

// blockText extracts text from a tool_result block's content, which is either a
// JSON string or an array of {type:"text", text:"..."} blocks.
func blockText(content json.RawMessage) string {
	c := bytes.TrimSpace(content)
	if len(c) == 0 {
		return ""
	}
	if c[0] == '"' {
		var s string
		if json.Unmarshal(c, &s) == nil {
			return s
		}
		return ""
	}
	if c[0] == '[' {
		var sb strings.Builder
		for _, b := range decodeBlocks(c) {
			if b.Text != "" {
				if sb.Len() > 0 {
					sb.WriteByte('\n')
				}
				sb.WriteString(b.Text)
			}
		}
		return sb.String()
	}
	return ""
}

func looksLikeBashResult(raw json.RawMessage) bool {
	c := bytes.TrimSpace(raw)
	if len(c) == 0 || c[0] != '{' {
		return false
	}
	var probe struct {
		Stdout *json.RawMessage `json:"stdout"`
		Stderr *json.RawMessage `json:"stderr"`
	}
	if json.Unmarshal(c, &probe) != nil {
		return false
	}
	return probe.Stdout != nil || probe.Stderr != nil
}

// LatestTitle returns the most recent ai-title in the parsed entries, or "" if
// none appear in the tail. Callers persist it so it survives scrolling out of
// the tail on long sessions.
func LatestTitle(entries []Entry) string {
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Kind == KindTitle {
			return entries[i].Title
		}
	}
	return ""
}

func hasToolUseResult(raw json.RawMessage) bool {
	c := bytes.TrimSpace(raw)
	return len(c) > 0 && string(c) != "null"
}

func isStringContent(msg json.RawMessage) bool {
	c := bytes.TrimSpace(messageContent(msg))
	return len(c) > 0 && c[0] == '"'
}
