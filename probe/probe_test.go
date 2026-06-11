package probe

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestProberReportsTransitions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	statuses := make(chan string, 16)
	p := New(srv.URL, 10*time.Millisecond, func(s string) { statuses <- s })
	p.Start()
	defer p.Stop()

	expect := func(want string) {
		t.Helper()
		select {
		case got := <-statuses:
			if got != want {
				t.Fatalf("status = %q, want %q", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for %q", want)
		}
	}

	expect("reachable")
	srv.Close()
	expect("probably_unreachable")
	expect("unreachable")
}

func TestProberStopIsIdempotent(t *testing.T) {
	p := New("http://127.0.0.1:0", time.Hour, func(string) {})
	p.Stop()
	p.Stop() // must not panic on double stop or never-started prober
}
