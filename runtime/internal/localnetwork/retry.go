package localnetwork

import (
	"context"
	"errors"
	"net"
	"syscall"
	"time"
)

// DialFunc is the shape of net.Dialer.DialContext.
type DialFunc func(ctx context.Context, network, address string) (net.Conn, error)

// TransientRetryDelays is how long a fleet dial waits between attempts after a
// failure that is routinely momentary: a daemon that just restarted (an
// update) may see no route for a few seconds while the interface, ARP, or the
// peer's own listener settles. The sum is the most a dead machine costs extra.
var TransientRetryDelays = []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second, 2 * time.Second}

// IsTransientDialError reports the dial failures worth trying again: no route
// to the host or network, and a refused connection (a peer daemon mid-restart).
// A timeout is not retried; it has already spent its time.
func IsTransientDialError(err error) bool {
	return errors.Is(err, syscall.EHOSTUNREACH) || errors.Is(err, syscall.ENETUNREACH) ||
		errors.Is(err, syscall.ECONNREFUSED)
}

// RetryDial wraps dial so a transient failure is tried again on the given
// schedule before it is reported. The first successful attempt returns at
// once, so a healthy path costs nothing; the context still bounds the whole.
func RetryDial(dial DialFunc, delays []time.Duration) DialFunc {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := dial(ctx, network, address)
		if ctx.Value(noRetryKey{}) != nil {
			return conn, err
		}
		for _, delay := range delays {
			if err == nil || !IsTransientDialError(err) {
				return conn, err
			}
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, err
			case <-timer.C:
			}
			conn, err = dial(ctx, network, address)
		}
		return conn, err
	}
}

type noRetryKey struct{}

// WithoutRetry marks a request whose dial should fail fast: a sweep over many
// candidates (Bonjour verification, picking a pairing route) learns more from
// a quick answer per candidate than from waiting out each absent one.
func WithoutRetry(ctx context.Context) context.Context {
	return context.WithValue(ctx, noRetryKey{}, true)
}
