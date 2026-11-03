package localnetwork

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestExplainAnnotatesDarwinLocalNetworkFailures(t *testing.T) {
	tests := []struct {
		name        string
		endpoint    string
		err         error
		annotated   bool
		originalErr string
	}{
		{"private-ip", "http://10.0.0.32:8787", &fixtureError{text: "dial tcp 10.0.0.32:8787: connect: no route to host", cause: syscall.EHOSTUNREACH}, true, "dial tcp 10.0.0.32:8787: connect: no route to host"},
		{"private-wrapped-errno", "http://192.168.1.20:8787", syscall.EHOSTUNREACH, true, "no route to host"},
		{"link-local", "http://169.254.2.4:8787", syscall.EHOSTUNREACH, true, "no route to host"},
		{"tailnet", "http://100.92.1.2:8787", syscall.EHOSTUNREACH, false, "no route to host"},
		{"public", "https://example.com", syscall.EHOSTUNREACH, false, "no route to host"},
		{"other-error", "http://10.0.0.2:8787", syscall.ECONNREFUSED, false, "connection refused"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := Explain(test.endpoint, test.err)
			wantAnnotated := test.annotated && runtime.GOOS == "darwin"
			if IsLocalUnreachable(got) != wantAnnotated {
				t.Fatalf("Explain(%q, %q) = %q, annotated=%v want %v", test.endpoint, test.err, got, !wantAnnotated, wantAnnotated)
			}
			if !strings.Contains(got.Error(), test.originalErr) {
				t.Fatalf("Explain(%q, %q) = %q, want it to preserve %q", test.endpoint, test.err, got, test.originalErr)
			}
			if errors.Is(test.err, syscall.EHOSTUNREACH) != errors.Is(got, syscall.EHOSTUNREACH) {
				t.Fatalf("Explain(%q, %q) = %q lost the underlying errno", test.endpoint, test.err, got)
			}
			if !wantAnnotated {
				if got.Error() != test.err.Error() {
					t.Fatalf("Explain(%q, %q) = %q, want the original error unchanged", test.endpoint, test.err, got)
				}
				return
			}
			// Without evidence the wording is calm and never names the setting.
			if !strings.Contains(got.Error(), UnreachableCause) || strings.Contains(got.Error(), "System Settings") {
				t.Fatalf("undiagnosed failure %q must use the calm wording only", got)
			}
			if ReasonFor(got) != ReasonUnreachable || IsBlocked(got) {
				t.Fatalf("ReasonFor(%q) = %q", got, ReasonFor(got))
			}
		})
	}
}

func TestExplainWithEvidenceNamesTheSettingOnlyWhenBlocked(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Local Network privacy is a macOS behaviour")
	}
	got := ExplainWithEvidence("http://10.0.0.32:8787", syscall.EHOSTUNREACH, true)
	if !IsBlocked(got) || ReasonFor(got) != ReasonBlocked || !strings.Contains(got.Error(), SettingsPath) {
		t.Fatalf("blocked explanation = %q (reason %q)", got, ReasonFor(got))
	}
	// Explaining an already explained error must not stack causes.
	if again := Explain("http://10.0.0.32:8787", got); again.Error() != got.Error() {
		t.Fatalf("re-explained = %q, want %q", again, got)
	}
}

// Classification must come from Explain, never from message text.
func TestClassificationIgnoresCopiedText(t *testing.T) {
	for _, text := range []string{UnreachableCause, BlockedCause} {
		err := errors.New(text + " (copied text)")
		if IsLocalUnreachable(err) || IsBlocked(err) || ReasonFor(err) != "" {
			t.Fatalf("copied text %q was classified", text)
		}
	}
	if IsLocalUnreachable(nil) || ReasonFor(nil) != "" {
		t.Fatal("nil was classified")
	}
}

func TestClassifyDistinguishesBlockedFromAbsentAndMomentary(t *testing.T) {
	const endpoint = "http://10.0.0.32:8787"
	failure := errors.New("dial tcp: no route to host")
	tests := []struct {
		name              string
		caller, daemon    error
		reason            string
		mentionsSetting   bool
		mentionsAbsentees bool
	}{
		{"terminal-also-fails", failure, failure, ReasonUnreachable, false, true},
		{"terminal-fails-daemon-unknown", failure, nil, ReasonUnreachable, false, true},
		{"terminal-reaches-daemon-still-fails", nil, failure, ReasonBlocked, true, false},
		{"both-reach-now", nil, nil, ReasonReachableNow, false, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := Classify(endpoint, test.caller, test.daemon)
			if got.Reason != test.reason || got.Endpoint != endpoint {
				t.Fatalf("Classify = %+v, want reason %q", got, test.reason)
			}
			if strings.Contains(got.Message, "System Settings") != test.mentionsSetting {
				t.Fatalf("message %q: settings mention want %v", got.Message, test.mentionsSetting)
			}
			if strings.Contains(got.Message, "asleep") != test.mentionsAbsentees {
				t.Fatalf("message %q: off/asleep mention want %v", got.Message, test.mentionsAbsentees)
			}
		})
	}
}

func TestProbeHealthCountsAnyHTTPAnswer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/health" {
			t.Errorf("probe path = %q", request.URL.Path)
		}
		http.Error(response, "unauthorized", http.StatusUnauthorized)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := ProbeHealth(ctx, server.Client(), server.URL+"/"); err != nil {
		t.Fatalf("probe of an answering server = %v", err)
	}
	closed := httptest.NewServer(http.NotFoundHandler())
	address := closed.URL
	closed.Close()
	if err := ProbeHealth(ctx, http.DefaultClient, address); err == nil {
		t.Fatal("probe of a closed server succeeded")
	}
}

func TestRetryDialRecoversFromMomentaryFailures(t *testing.T) {
	attempts := 0
	dial := RetryDial(func(context.Context, string, string) (net.Conn, error) {
		attempts++
		if attempts < 3 {
			return nil, &net.OpError{Op: "dial", Net: "tcp", Err: syscall.EHOSTUNREACH}
		}
		client, server := net.Pipe()
		server.Close()
		return client, nil
	}, []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond})
	conn, err := dial(context.Background(), "tcp", "10.0.0.32:8787")
	if err != nil || conn == nil {
		t.Fatalf("dial = %v, %v", conn, err)
	}
	conn.Close()
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
}

func TestRetryDialIsBoundedAndSkipsNonTransientErrors(t *testing.T) {
	for _, test := range []struct {
		name     string
		err      error
		attempts int
	}{
		{"no-route", syscall.EHOSTUNREACH, 4},
		{"network-unreachable", syscall.ENETUNREACH, 4},
		{"refused", syscall.ECONNREFUSED, 4},
		{"timeout", context.DeadlineExceeded, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			attempts := 0
			dial := RetryDial(func(context.Context, string, string) (net.Conn, error) {
				attempts++
				return nil, &net.OpError{Op: "dial", Net: "tcp", Err: test.err}
			}, []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond})
			if _, err := dial(context.Background(), "tcp", "10.0.0.32:8787"); !errors.Is(err, test.err) {
				t.Fatalf("dial error = %v, want %v", err, test.err)
			}
			if attempts != test.attempts {
				t.Fatalf("attempts = %d, want %d", attempts, test.attempts)
			}
		})
	}
}

func TestRetryDialStopsWhenTheContextEnds(t *testing.T) {
	attempts := 0
	dial := RetryDial(func(context.Context, string, string) (net.Conn, error) {
		attempts++
		return nil, syscall.EHOSTUNREACH
	}, []time.Duration{time.Hour})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := dial(ctx, "tcp", "10.0.0.32:8787"); !errors.Is(err, syscall.EHOSTUNREACH) {
		t.Fatalf("dial error = %v", err)
	}
	if attempts != 1 || time.Since(started) > time.Second {
		t.Fatalf("attempts=%d elapsed=%s, want one attempt cut short by the context", attempts, time.Since(started))
	}
}

func TestWithoutRetryFailsFast(t *testing.T) {
	attempts := 0
	dial := RetryDial(func(context.Context, string, string) (net.Conn, error) {
		attempts++
		return nil, syscall.EHOSTUNREACH
	}, []time.Duration{time.Millisecond, time.Millisecond})
	if _, err := dial(WithoutRetry(context.Background()), "tcp", "10.0.0.32:8787"); err == nil || attempts != 1 {
		t.Fatalf("attempts = %d err = %v, want one failed attempt", attempts, err)
	}
}

func TestTransientRetryScheduleStaysInsideTheFleetBudget(t *testing.T) {
	var total time.Duration
	for _, delay := range TransientRetryDelays {
		total += delay
	}
	if total < 4*time.Second || total > 8*time.Second {
		t.Fatalf("retry schedule totals %s, want a brief bounded window", total)
	}
}

type fixtureError struct {
	text  string
	cause error
}

func (e *fixtureError) Error() string { return e.text }
func (e *fixtureError) Unwrap() error { return e.cause }
