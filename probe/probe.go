// Package probe implements API reachability probing for the apiReachability
// event stream. Desktop passes an apiProbeURL in startVM and renders the
// resulting status (reachable / probably_unreachable / unreachable) as its
// online/offline indicator.
package probe

import (
	"net/http"
	"sync"
	"time"
)

// Prober periodically checks an HTTP endpoint and reports status
// transitions. Any HTTP response (regardless of status code) counts as
// reachable — the probe answers "can we reach the API host", not "is the
// API healthy". One failed probe is probably_unreachable; consecutive
// failures are unreachable.
type Prober struct {
	url      string
	interval time.Duration
	onStatus func(status string)

	stop     chan struct{}
	stopOnce sync.Once
}

// New creates a prober that calls onStatus with the initial status and on
// every change thereafter. Start it with Start; it probes immediately.
func New(url string, interval time.Duration, onStatus func(status string)) *Prober {
	return &Prober{
		url:      url,
		interval: interval,
		onStatus: onStatus,
		stop:     make(chan struct{}),
	}
}

// Start launches the probe loop in a goroutine.
func (p *Prober) Start() {
	go p.loop()
}

// Stop terminates the probe loop. Safe to call multiple times and on a
// never-started prober.
func (p *Prober) Stop() {
	p.stopOnce.Do(func() { close(p.stop) })
}

func (p *Prober) loop() {
	client := &http.Client{Timeout: 10 * time.Second}
	last := ""
	failures := 0
	for {
		status := "reachable"
		resp, err := client.Head(p.url)
		if err != nil {
			failures++
			status = "probably_unreachable"
			if failures >= 2 {
				status = "unreachable"
			}
		} else {
			_ = resp.Body.Close()
			failures = 0
		}
		if status != last {
			p.onStatus(status)
			last = status
		}
		select {
		case <-p.stop:
			return
		case <-time.After(p.interval):
		}
	}
}
