// Package logx centralizes logging for cowork-svc-linux: one truncation
// policy, one opt-out flag (-log-full-lines), one debug gate. Thin wrapper
// over the stdlib log package so the file:line prefix and systemd-journal
// stream stay exactly the same — this is not a structured logger.
package logx

import (
	"fmt"
	"log"
	"strconv"
	"sync/atomic"
)

var (
	debug     atomic.Bool
	fullLines atomic.Bool
	maxLen    atomic.Int32
)

func init() { maxLen.Store(160) }

// Configure is called once from main after flag.Parse.
func Configure(debugOn, fullLinesOn bool, lineLen int) {
	debug.Store(debugOn)
	fullLines.Store(fullLinesOn)
	if lineLen > 0 {
		maxLen.Store(int32(lineLen))
	}
	applyDebugFlags(debugOn)
}

// SetDebug flips the debug gate at runtime (used by the setDebugLogging RPC).
func SetDebug(on bool) {
	debug.Store(on)
	applyDebugFlags(on)
}

// DebugEnabled reports whether Debug calls will emit output.
func DebugEnabled() bool { return debug.Load() }

func applyDebugFlags(on bool) {
	if on {
		log.SetFlags(log.LstdFlags | log.Lshortfile)
	} else {
		log.SetFlags(log.LstdFlags)
	}
}

// Trunc cuts s to the configured line budget, appending a hint at how much
// was dropped. Pass-through when -log-full-lines is set.
func Trunc(s string) string {
	if fullLines.Load() {
		return s
	}
	n := int(maxLen.Load())
	if len(s) <= n {
		return s
	}
	return s[:n] + "…(+" + strconv.Itoa(len(s)-n) + " more)"
}

// Info always logs. Use for startup, shutdown, errors, and warnings.
func Info(format string, args ...any) {
	_ = log.Output(2, fmt.Sprintf(format, args...))
}

// Debug logs only when the debug gate is on.
func Debug(format string, args ...any) {
	if !debug.Load() {
		return
	}
	_ = log.Output(2, fmt.Sprintf(format, args...))
}

// Logger carries a subsystem prefix (e.g. "kvm", "native") so call sites
// don't hand-roll "[kvm] " in every format string.
type Logger struct{ prefix string }

// Subsystem returns a logger that prefixes every line with "[name] ".
func Subsystem(name string) *Logger { return &Logger{prefix: "[" + name + "] "} }

func (l *Logger) Info(format string, args ...any) {
	_ = log.Output(2, l.prefix+fmt.Sprintf(format, args...))
}

func (l *Logger) Debug(format string, args ...any) {
	if !debug.Load() {
		return
	}
	_ = log.Output(2, l.prefix+fmt.Sprintf(format, args...))
}
