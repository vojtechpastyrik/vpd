package gitlab

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"
)

// Exit codes of `vpd gitlab upgrade`. They tell the caller what a failure
// means for rollback without reading the log.
const (
	ExitOK = 0
	// ExitAbortBefore: stopped before the package switched; nothing to roll back.
	ExitAbortBefore = 1
	// ExitAbortAfter: stopped after the package switched; needs attention,
	// rollback means restoring the backup taken at the start of the stop.
	ExitAbortAfter = 2
	// ExitWarnings: finished, with warnings worth a look.
	ExitWarnings = 3
	// ExitGate: a stop finished and --gate asks for manual checks before the next.
	ExitGate = 4
)

// Abort ends a command with one of the exit codes above.
type Abort struct {
	Code int
	Msg  string
}

func (a *Abort) Error() string { return a.Msg }

func abortBefore(format string, args ...any) *Abort {
	return &Abort{Code: ExitAbortBefore, Msg: fmt.Sprintf(format, args...)}
}

func abortAfter(format string, args ...any) *Abort {
	return &Abort{Code: ExitAbortAfter, Msg: fmt.Sprintf(format, args...)}
}

// Reporter prints progress: one line per phase change, details indented under
// it. In JSON mode every line is one event object.
type Reporter struct {
	W    io.Writer
	JSON bool

	mu       sync.Mutex
	warnings []string
}

type event struct {
	Time  string `json:"time"`
	Level string `json:"level"`
	Msg   string `json:"msg"`
}

func (r *Reporter) Phase(format string, args ...any) { r.emit("phase", fmt.Sprintf(format, args...)) }
func (r *Reporter) Info(format string, args ...any)  { r.emit("info", fmt.Sprintf(format, args...)) }

func (r *Reporter) Warn(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	r.mu.Lock()
	r.warnings = append(r.warnings, msg)
	r.mu.Unlock()
	r.emit("warn", msg)
}

func (r *Reporter) Error(msg string) { r.emit("error", msg) }

func (r *Reporter) Warnings() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.warnings...)
}

func (r *Reporter) emit(level, msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	if r.JSON {
		b, _ := json.Marshal(event{Time: now.Format(time.RFC3339), Level: level, Msg: msg})
		fmt.Fprintln(r.W, string(b))
		return
	}
	prefix := "  "
	switch level {
	case "phase":
		prefix = ""
	case "warn":
		prefix = "  WARN: "
	case "error":
		prefix = "  ABORT: "
	}
	fmt.Fprintf(r.W, "[%s] %s%s\n", now.Format("15:04:05"), prefix, msg)
}
