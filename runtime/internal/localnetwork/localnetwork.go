package localnetwork

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

const (
	// ReasonUnreachable marks a private-address dial that failed with the errno
	// macOS returns both for a machine that is off, asleep, or elsewhere and for
	// a process the Local Network privacy switch refuses. The daemon alone cannot
	// tell those apart, so this reason claims only that the machine did not
	// answer; a caller exempt from the privacy check can diagnose further.
	ReasonUnreachable = "machine-unreachable"
	// ReasonBlocked is reported only on evidence: a process exempt from Local
	// Network privacy (the CLI run from Terminal or SSH) reached the same
	// address moments ago while the daemon, re-dialling it, still could not.
	ReasonBlocked = "local-network-blocked"
	// LegacyReason is what older daemons sent for the same ambiguous failure.
	LegacyReason = "local-network-permission"

	// SettingsPath is where the switch lives on macOS.
	SettingsPath = "System Settings › Privacy & Security › Local Network › Sessions"

	// UnreachableCause is the calm wording for an undiagnosed failure.
	UnreachableCause = "the machine looks off, asleep, or on another network"
	// BlockedCause is the definitive wording, used only with evidence.
	BlockedCause = "macOS is blocking the Sessions background service from the local network: a command run from Terminal reached this address, but sessionsd could not. Turn on Sessions in " + SettingsPath + ", then try again"
)

var (
	errUnreachable = errors.New(UnreachableCause)
	errBlocked     = errors.New(BlockedCause)
)

// Explain annotates Darwin's terse EHOSTUNREACH on a private or link-local
// destination with the calm, undiagnosed cause. The original error is preserved
// and reported first, so callers keep both the transport detail and errno
// matching. Tailnet and public-network failures are returned unchanged.
func Explain(endpoint string, err error) error {
	return ExplainWithEvidence(endpoint, err, false)
}

// ExplainWithEvidence is Explain for a caller that holds a verified Local
// Network block (see ReasonBlocked). Without that evidence it is Explain.
func ExplainWithEvidence(endpoint string, err error, blocked bool) error {
	if err == nil || !platformDenied(err) || !IsLocalEndpoint(endpoint) {
		return err
	}
	if errors.Is(err, errUnreachable) || errors.Is(err, errBlocked) {
		return err
	}
	if blocked {
		return fmt.Errorf("%w (%w)", err, errBlocked)
	}
	return fmt.Errorf("%w (%w)", err, errUnreachable)
}

// IsLocalUnreachable reports whether Explain classified err as a private-address
// failure that could be either an absent machine or a Local Network block.
func IsLocalUnreachable(err error) bool {
	return errors.Is(err, errUnreachable) || errors.Is(err, errBlocked)
}

// IsBlocked reports whether err carries verified evidence of a Local Network
// block. Copied message text never qualifies.
func IsBlocked(err error) bool { return errors.Is(err, errBlocked) }

// ReasonFor is the machine-readable reason for err, or "" when Explain did not
// classify it.
func ReasonFor(err error) string {
	switch {
	case IsBlocked(err):
		return ReasonBlocked
	case IsLocalUnreachable(err):
		return ReasonUnreachable
	}
	return ""
}

func IsLocalEndpoint(endpoint string) bool {
	host := strings.TrimSpace(endpoint)
	if parsed, err := url.Parse(host); err == nil && parsed.Hostname() != "" {
		host = parsed.Hostname()
	} else if split, _, err := net.SplitHostPort(host); err == nil {
		host = split
	}
	host = strings.Trim(host, "[]")
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast())
}
