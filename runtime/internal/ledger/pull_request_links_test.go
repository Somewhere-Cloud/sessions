package ledger

import (
	"fmt"
	"testing"
)

func prURL(number int) string { return fmt.Sprintf("https://github.com/acme/app/pull/%d", number) }

func prLinkedEvent(seq int64, kind EventType, number int) Event {
	return ledgerEvent(seq, "lane", kind, seq*100, pullRequestRef(number, prURL(number)))
}

func foldedURLs(lane LaneState) []string {
	urls := make([]string, 0, len(lane.PullRequests))
	for _, link := range lane.PullRequests {
		urls = append(urls, link.URL)
	}
	return urls
}

// A ledger written when a lane held one link folds exactly as it did: each
// pull_request_linked replaced the previous link, so no replaced link is
// invented as still linked, and facts about it stay history.
func TestLegacyPullRequestFactsDoNotInventRetainedLinks(t *testing.T) {
	events := []Event{
		createdLedgerEvent(1, "lane", "terminal", ""),
		prLinkedEvent(2, EventPullRequestLinked, 1),
		ledgerEvent(3, "lane", EventPullRequestObserved, 300, pullRequestObservedPayload{URL: prURL(1), PullRequestObservation: PullRequestObservation{State: "open"}}),
		prLinkedEvent(4, EventPullRequestLinked, 2),
		ledgerEvent(5, "lane", EventPullRequestObserved, 500, pullRequestObservedPayload{URL: prURL(1), PullRequestObservation: PullRequestObservation{State: "merged"}}),
		ledgerEvent(6, "lane", EventPullRequestUnlinked, 600, pullRequestUnlinkedPayload{URL: prURL(1)}),
		prLinkedEvent(7, EventPullRequestLinked, 2),
	}
	lane := Fold(events)[0]
	if urls := foldedURLs(lane); len(urls) != 1 || urls[0] != prURL(2) || lane.PullRequest.URL != prURL(2) || lane.PullRequest.LinkedAtMS != 400 {
		t.Fatalf("legacy fold = %v primary=%#v", urls, lane.PullRequest)
	}
	if lane.PullRequest.Observed {
		t.Fatal("an observation of the replaced link was applied to the current one")
	}
}

// Added links coexist; merged is per link; exact unlink and observation touch
// only the named link; the primary copy always mirrors its list entry.
func TestAddedPullRequestLinksFoldPerLink(t *testing.T) {
	events := []Event{
		createdLedgerEvent(1, "lane", "terminal", ""),
		prLinkedEvent(2, EventPullRequestAdded, 1),
		ledgerEvent(3, "lane", EventPullRequestAdded, 300, pullRequestAddedPayload{
			PullRequestRef: pullRequestRef(2, prURL(2)), TaskLinks: []TaskLink{{Ref: "gh:acme/app#9"}},
		}),
		prLinkedEvent(4, EventPullRequestAdded, 2),
		ledgerEvent(5, "lane", EventPullRequestObserved, 500, pullRequestObservedPayload{URL: prURL(2), PullRequestObservation: PullRequestObservation{State: "merged"}}),
		ledgerEvent(6, "lane", EventPullRequestObserved, 600, pullRequestObservedPayload{URL: prURL(2), PullRequestObservation: PullRequestObservation{State: "open"}}),
		ledgerEvent(7, "lane", EventPullRequestObserved, 700, pullRequestObservedPayload{URL: prURL(1), PullRequestObservation: PullRequestObservation{State: "open"}}),
		ledgerEvent(8, "lane", EventTaskLinked, 800, taskLinksPayload{Links: []TaskLink{{Ref: "sw:tsk_0123abcd", PullRequestURL: prURL(1)}, {Ref: "gh:acme/app#5", PullRequestURL: prURL(7)}}}),
	}
	lane := Fold(events)[0]
	if urls := foldedURLs(lane); len(urls) != 2 || lane.PullRequest.URL != prURL(1) {
		t.Fatalf("added links = %v primary=%s", urls, lane.PullRequest.URL)
	}
	first, second := lane.PullRequests[0], lane.PullRequests[1]
	if first.Merged || !second.Merged || second.Observation.State != "merged" || first.Observation.State != "open" {
		t.Fatalf("per-link observations = %#v / %#v", first, second)
	}
	if len(second.Tasks) != 1 || second.Tasks[0].Ref != "gh:acme/app#9" || len(first.Tasks) != 1 || first.Tasks[0].PullRequestURL != "" ||
		len(lane.PullRequest.Tasks) != 1 || len(lane.Tasks) != 0 {
		t.Fatalf("task links = %+v / %+v / session %+v", first.Tasks, second.Tasks, lane.Tasks)
	}
	events = append(events, ledgerEvent(9, "lane", EventPullRequestUnlinked, 900, pullRequestUnlinkedPayload{URL: prURL(1)}))
	lane = Fold(events)[0]
	if urls := foldedURLs(lane); len(urls) != 1 || lane.PullRequest.URL != prURL(2) || !lane.PullRequest.Merged {
		t.Fatalf("after exact unlink = %v primary=%#v", urls, lane.PullRequest)
	}
}

func TestPullRequestLinksAreBounded(t *testing.T) {
	events := []Event{createdLedgerEvent(1, "lane", "terminal", "")}
	for number := 1; number <= maxPullRequestLinks+3; number++ {
		events = append(events, prLinkedEvent(int64(number+1), EventPullRequestAdded, number))
	}
	if got := len(Fold(events)[0].PullRequests); got != maxPullRequestLinks {
		t.Fatalf("links = %d, want %d", got, maxPullRequestLinks)
	}
}
