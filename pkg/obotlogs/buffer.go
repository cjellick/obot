// Package obotlogs keeps Obot's recent log output in memory, so administrators (and Obot AI)
// can read what the server has been saying without access to the host or the cluster.
package obotlogs

import (
	"encoding/json"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultCapacity is how many lines the process-wide buffer keeps.
	DefaultCapacity = 5000
	// maxLineBytes caps one stored line. Longer lines are cut.
	maxLineBytes = 8 * 1024
)

// Line is one line of output.
type Line struct {
	Seq     uint64    `json:"seq"`
	Time    time.Time `json:"time"`
	Stream  string    `json:"stream"`
	Level   string    `json:"level,omitempty"`
	Message string    `json:"message,omitempty"`
	Text    string    `json:"text"`
}

// Buffer is a bounded, concurrency-safe ring of lines.
type Buffer struct {
	mu    sync.Mutex
	lines []Line
	next  int
	full  bool
	seq   uint64
}

// NewBuffer returns a buffer that keeps the last capacity lines.
func NewBuffer(capacity int) *Buffer {
	if capacity <= 0 {
		capacity = DefaultCapacity
	}
	return &Buffer{lines: make([]Line, capacity)}
}

var std = NewBuffer(DefaultCapacity)

// Default is the process-wide buffer Capture fills.
func Default() *Buffer { return std }

// Add records one line written to stream at t.
func (b *Buffer) Add(stream, text string, t time.Time) {
	text = strings.TrimRight(text, "\r\n")
	if text == "" {
		return
	}
	if len(text) > maxLineBytes {
		text = text[:maxLineBytes] + "…"
	}
	level, message, when := parse(text)
	if !when.IsZero() {
		t = when
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.seq++
	b.lines[b.next] = Line{Seq: b.seq, Time: t, Stream: stream, Level: level, Message: message, Text: text}
	b.next++
	if b.next == len(b.lines) {
		b.next = 0
		b.full = true
	}
}

// Query selects lines.
type Query struct {
	// Text is matched case-insensitively against the whole line. Every word must appear.
	Text string
	// MinLevel keeps lines at this level or above: debug, info, warn or error. Lines without a
	// level are kept only when MinLevel is empty or debug.
	MinLevel string
	// Since drops lines older than this.
	Since time.Time
	// Limit is how many of the newest matching lines to return.
	Limit int
}

// Result is a query's answer, oldest line first.
type Result struct {
	Lines     []Line    `json:"lines"`
	Matched   int       `json:"matched"`
	Buffered  int       `json:"buffered"`
	Capacity  int       `json:"capacity"`
	OldestAt  time.Time `json:"oldestAt,omitzero"`
	Truncated bool      `json:"truncated,omitempty"`
}

// Search returns the newest lines matching q, oldest first.
func (b *Buffer) Search(q Query) Result {
	b.mu.Lock()
	all := b.snapshot()
	capacity := len(b.lines)
	b.mu.Unlock()

	if q.Limit <= 0 {
		q.Limit = 200
	}
	terms := strings.Fields(strings.ToLower(q.Text))
	minRank := levelRank(q.MinLevel)

	result := Result{Buffered: len(all), Capacity: capacity, Lines: []Line{}}
	if len(all) > 0 {
		result.OldestAt = all[0].Time
	}
	var matched []Line
next:
	for _, line := range all {
		if !q.Since.IsZero() && line.Time.Before(q.Since) {
			continue
		}
		if minRank > 0 && levelRank(line.Level) < minRank {
			continue
		}
		if len(terms) > 0 {
			hay := strings.ToLower(line.Text)
			for _, t := range terms {
				if !strings.Contains(hay, t) {
					continue next
				}
			}
		}
		matched = append(matched, line)
	}
	result.Matched = len(matched)
	if len(matched) > q.Limit {
		matched = matched[len(matched)-q.Limit:]
		result.Truncated = true
	}
	result.Lines = append(result.Lines, matched...)
	return result
}

func (b *Buffer) snapshot() []Line {
	if !b.full {
		return append([]Line(nil), b.lines[:b.next]...)
	}
	out := make([]Line, 0, len(b.lines))
	out = append(out, b.lines[b.next:]...)
	return append(out, b.lines[:b.next]...)
}

// levelRank orders levels; 0 is "no level", which only a debug or empty filter keeps.
func levelRank(level string) int {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug", "trace":
		return 1
	case "info", "notice":
		return 2
	case "warn", "warning":
		return 3
	case "error", "err", "fatal", "panic", "critical":
		return 4
	}
	return 0
}

// NormalizeLevel maps the level names log lines use onto debug, info, warn and error.
func NormalizeLevel(level string) string {
	switch levelRank(level) {
	case 1:
		return "debug"
	case 2:
		return "info"
	case 3:
		return "warn"
	case 4:
		return "error"
	}
	return ""
}

// parse pulls the level, message and time out of a structured log line: Obot's JSON lines
// (severity/message/time), other JSON loggers (level/msg) and slog's text format.
func parse(text string) (level, message string, when time.Time) {
	if strings.HasPrefix(text, "{") {
		var fields map[string]any
		if json.Unmarshal([]byte(text), &fields) == nil {
			level = NormalizeLevel(str(fields, "severity", "level", "lvl"))
			message = str(fields, "message", "msg")
			if t, err := time.Parse(time.RFC3339Nano, str(fields, "time", "ts", "timestamp")); err == nil {
				when = t
			}
			return level, message, when
		}
	}
	// slog text: time=... level=INFO msg="..."
	if i := strings.Index(text, "level="); i >= 0 {
		rest := text[i+len("level="):]
		if j := strings.IndexByte(rest, ' '); j >= 0 {
			rest = rest[:j]
		}
		level = NormalizeLevel(rest)
	}
	if level == "" {
		upper := strings.ToUpper(text)
		for _, candidate := range []string{"ERROR", "WARN", "INFO", "DEBUG"} {
			if strings.Contains(upper, "["+candidate+"]") || strings.HasPrefix(upper, candidate+" ") || strings.Contains(upper, " "+candidate+" ") {
				level = NormalizeLevel(candidate)
				break
			}
		}
	}
	if strings.HasPrefix(text, "panic:") || strings.HasPrefix(text, "fatal error:") {
		level = "error"
	}
	return level, message, when
}

func str(fields map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := fields[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}
