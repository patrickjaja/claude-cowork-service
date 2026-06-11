package process

// Event types that match the Windows cowork-svc protocol.

// StdoutEvent is emitted when a process writes to stdout.
// The client expects "id" (not "processId") per the Cowork protocol.
type StdoutEvent struct {
	Type      string `json:"type"`
	ProcessID string `json:"id"`
	Data      string `json:"data"`
}

// StderrEvent is emitted when a process writes to stderr.
type StderrEvent struct {
	Type      string `json:"type"`
	ProcessID string `json:"id"`
	Data      string `json:"data"`
}

// ExitEvent is emitted when a process exits.
// Client reads a.exitCode, a.signal, a.oomKillCount.
type ExitEvent struct {
	Type         string `json:"type"`
	ProcessID    string `json:"id"`
	ExitCode     int    `json:"exitCode"`
	Signal       string `json:"signal,omitempty"`
	OOMKillCount int    `json:"oomKillCount,omitempty"`
}

// APIReachableEvent is emitted when the API becomes reachable from inside the VM.
// Desktop reads s.status from the pipe event and passes it to onApiReachability.
// Valid values: "unknown", "reachable", "probably_unreachable", "unreachable".
type APIReachableEvent struct {
	Type   string `json:"type"`
	Status string `json:"status"`
}

// ErrorEvent is emitted when a process-level error occurs.
// Client handles case "error" events with {id, message, fatal} fields.
type ErrorEvent struct {
	Type      string `json:"type"`
	ProcessID string `json:"id"`
	Message   string `json:"message"`
	Fatal     bool   `json:"fatal"`
}

// NewStdoutEvent creates a stdout event.
func NewStdoutEvent(processID, data string) StdoutEvent {
	return StdoutEvent{Type: "stdout", ProcessID: processID, Data: data}
}

// NewStderrEvent creates a stderr event.
func NewStderrEvent(processID, data string) StderrEvent {
	return StderrEvent{Type: "stderr", ProcessID: processID, Data: data}
}

// NewExitEvent creates an exit event.
func NewExitEvent(processID string, code int) ExitEvent {
	return ExitEvent{Type: "exit", ProcessID: processID, ExitCode: code}
}

// NewExitEventWithSignal creates an exit event for signal-caused exits.
func NewExitEventWithSignal(processID string, code int, signal string) ExitEvent {
	return ExitEvent{Type: "exit", ProcessID: processID, ExitCode: code, Signal: signal}
}

// NewAPIReachableEvent creates an API reachability event.
func NewAPIReachableEvent(reachable bool) APIReachableEvent {
	status := "unreachable"
	if reachable {
		status = "reachable"
	}
	return APIReachableEvent{Type: "apiReachability", Status: status}
}

// NewAPIReachabilityStatusEvent creates an API reachability event with an
// explicit status: "reachable", "probably_unreachable", "unreachable", or
// "unknown".
func NewAPIReachabilityStatusEvent(status string) APIReachableEvent {
	return APIReachableEvent{Type: "apiReachability", Status: status}
}

// StartupStepEvent is emitted to report VM startup progress.
// Desktop guards with s.step && s.status: "started" means step began,
// any other value (e.g. "completed") triggers stepCompleted.
type StartupStepEvent struct {
	Type   string `json:"type"`
	Step   string `json:"step"`
	Status string `json:"status"`
}

// NewStartupStepEvent creates a startup progress event.
func NewStartupStepEvent(step, status string) StartupStepEvent {
	return StartupStepEvent{Type: "startupStep", Step: step, Status: status}
}

// NetworkStatusEvent is emitted to report network connectivity state.
// Desktop handles case "networkStatus" with s.status: "CONNECTED" or "NOT_CONNECTED".
type NetworkStatusEvent struct {
	Type   string `json:"type"`
	Status string `json:"status"`
}

// NewNetworkStatusEvent creates a network status event.
func NewNetworkStatusEvent(connected bool) NetworkStatusEvent {
	status := "NOT_CONNECTED"
	if connected {
		status = "CONNECTED"
	}
	return NetworkStatusEvent{Type: "networkStatus", Status: status}
}

// NewErrorEvent creates a process error event.
func NewErrorEvent(processID string, message string, fatal bool) ErrorEvent {
	return ErrorEvent{Type: "error", ProcessID: processID, Message: message, Fatal: fatal}
}
