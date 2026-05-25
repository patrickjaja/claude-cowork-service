package native

import (
	"strings"
	"testing"

	"github.com/patrickjaja/claude-cowork-service/process"
)

func TestStreamOutputFiltersSandboxRuntimeDebugLines(t *testing.T) {
	var events []interface{}
	pt := newProcessTracker(func(event interface{}) {
		events = append(events, event)
	}, true)

	pt.streamOutput("proc-1", strings.NewReader("[SandboxDebug] initializing sandbox\ninner output\n"), "stderr")

	if len(events) != 1 {
		t.Fatalf("events = %#v, want one inner command output event", events)
	}
	stdout, ok := events[0].(process.StdoutEvent)
	if !ok {
		t.Fatalf("event = %#v, want process.StdoutEvent", events[0])
	}
	if stdout.Data != "inner output\n" {
		t.Fatalf("stdout data = %q, want inner output", stdout.Data)
	}
}

func TestStreamOutputFiltersMultilineSandboxRuntimeDebugJSON(t *testing.T) {
	var events []interface{}
	pt := newProcessTracker(func(event interface{}) {
		events = append(events, event)
	}, true)

	pt.streamOutput("proc-1", strings.NewReader("[SandboxDebug] {\n  \"allowedHosts\": [\n    \"example.com\"\n  ]\n}\ninner output\n"), "stderr")

	if len(events) != 1 {
		t.Fatalf("events = %#v, want one inner command output event", events)
	}
	stdout, ok := events[0].(process.StdoutEvent)
	if !ok {
		t.Fatalf("event = %#v, want process.StdoutEvent", events[0])
	}
	if stdout.Data != "inner output\n" {
		t.Fatalf("stdout data = %q, want inner output", stdout.Data)
	}
}

func TestStreamOutputKeepsInnerCommandSandboxDebugLikeStdout(t *testing.T) {
	var events []interface{}
	pt := newProcessTracker(func(event interface{}) {
		events = append(events, event)
	}, true)

	pt.streamOutput("proc-1", strings.NewReader("[SandboxDebug] user stdout\n"), "stdout")

	if len(events) != 1 {
		t.Fatalf("events = %#v, want stdout event", events)
	}
	stdout, ok := events[0].(process.StdoutEvent)
	if !ok {
		t.Fatalf("event = %#v, want process.StdoutEvent", events[0])
	}
	if stdout.Data != "[SandboxDebug] user stdout\n" {
		t.Fatalf("stdout data = %q, want original stdout", stdout.Data)
	}
}
