package pullrequest

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/somewhere-tech/sessions/runtime/internal/ledger"
)

type ghPullRequest struct {
	Number      *int   `json:"number"`
	URL         string `json:"url"`
	State       string `json:"state"`
	IsDraft     bool   `json:"isDraft"`
	MergedAt    string `json:"mergedAt"`
	MergeCommit *struct {
		OID string `json:"oid"`
	} `json:"mergeCommit"`
	HeadRefName       string    `json:"headRefName"`
	HeadRefOID        string    `json:"headRefOid"`
	BaseRefName       string    `json:"baseRefName"`
	Mergeable         string    `json:"mergeable"`
	StatusCheckRollup []ghCheck `json:"statusCheckRollup"`
}

type ghCheck struct {
	TypeName   string `json:"__typename"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
}

// decodeGH turns gh's JSON into a content-free observation. Titles, bodies,
// check names, and URLs of checks are never kept.
func decodeGH(ref ledger.PullRequestRef, encoded []byte) (ledger.PullRequestObservation, error) {
	var value ghPullRequest
	if err := json.Unmarshal(encoded, &value); err != nil {
		return ledger.PullRequestObservation{}, &FetchError{Kind: ErrorMalformedResponse, Message: fmt.Sprintf(
			"gh returned output for %s that is not the expected JSON (%v); update gh and refresh again", ref.URL, err)}
	}
	if value.Number == nil {
		return ledger.PullRequestObservation{}, &FetchError{Kind: ErrorMalformedResponse, Message: fmt.Sprintf(
			"gh returned no pull request number for %s; update gh and refresh again", ref.URL)}
	}
	if *value.Number != ref.Number {
		return ledger.PullRequestObservation{}, &FetchError{Kind: ErrorMismatch, Message: fmt.Sprintf(
			"gh answered for pull request #%d, not #%d at %s; nothing was recorded", *value.Number, ref.Number, ref.URL)}
	}
	state, ok := map[string]string{"OPEN": StateOpen, "CLOSED": StateClosed, "MERGED": StateMerged}[strings.ToUpper(value.State)]
	if !ok {
		return ledger.PullRequestObservation{}, &FetchError{Kind: ErrorMalformedResponse, Message: fmt.Sprintf(
			"gh reported an unrecognized state %q for %s; nothing was recorded", value.State, ref.URL)}
	}
	observation := ledger.PullRequestObservation{
		State: state, Draft: value.IsDraft, MergedAt: value.MergedAt,
		HeadRef: value.HeadRefName, HeadSHA: value.HeadRefOID, BaseRef: value.BaseRefName,
		Mergeable: mergeable(value.Mergeable),
	}
	if value.MergeCommit != nil {
		observation.MergeCommit = value.MergeCommit.OID
	}
	observation.Checks, observation.ChecksTotal, observation.ChecksFailing, observation.ChecksPending =
		summarizeChecks(value.StatusCheckRollup)
	return observation, nil
}

func mergeable(value string) string {
	switch strings.ToUpper(value) {
	case "MERGEABLE":
		return MergeableYes
	case "CONFLICTING":
		return MergeableConflicted
	default:
		return MergeableUnknown
	}
}

// summarizeChecks folds check runs and commit statuses into one word. Failing
// outranks pending, pending outranks unknown, and only a set whose every
// member succeeded (or was neutral or skipped) is passing.
func summarizeChecks(checks []ghCheck) (string, int, int, int) {
	if len(checks) == 0 {
		return ChecksNone, 0, 0, 0
	}
	failing, pending, unknown := 0, 0, 0
	for _, check := range checks {
		switch checkOutcome(check) {
		case ChecksFailing:
			failing++
		case ChecksPending:
			pending++
		case ChecksUnknown:
			unknown++
		}
	}
	summary := ChecksPassing
	switch {
	case failing > 0:
		summary = ChecksFailing
	case pending > 0:
		summary = ChecksPending
	case unknown > 0:
		summary = ChecksUnknown
	}
	return summary, len(checks), failing, pending
}

func checkOutcome(check ghCheck) string {
	if check.TypeName == "StatusContext" || (check.TypeName == "" && check.State != "") {
		switch strings.ToUpper(check.State) {
		case "SUCCESS":
			return ChecksPassing
		case "PENDING", "EXPECTED":
			return ChecksPending
		case "FAILURE", "ERROR":
			return ChecksFailing
		default:
			return ChecksUnknown
		}
	}
	if strings.ToUpper(check.Status) != "COMPLETED" {
		if check.Status == "" {
			return ChecksUnknown
		}
		return ChecksPending
	}
	switch strings.ToUpper(check.Conclusion) {
	case "SUCCESS", "NEUTRAL", "SKIPPED":
		return ChecksPassing
	case "FAILURE", "TIMED_OUT", "CANCELLED", "ACTION_REQUIRED", "STARTUP_FAILURE", "STALE":
		return ChecksFailing
	default:
		return ChecksUnknown
	}
}
