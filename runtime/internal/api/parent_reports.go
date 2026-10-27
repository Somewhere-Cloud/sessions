package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	sessionruntime "github.com/somewhere-tech/sessions/runtime/internal/session"
)

// Parent reports carry a delegated lane's outcome back to the session that
// created it, so a manager can delegate and keep working instead of holding a
// turn open in `sessions wait`. A child opts in when it is created
// (`sessions new --report`). Every idle outcome it publishes is written to a
// durable outbox first, then delivered to the parent through the same
// attributed submit path `sessions send` uses, once the parent is idle.
//
// Delivery is at most once per outcome: each report has a fixed operation id,
// and a report whose delivery was uncertain (unknown, text-only, or a daemon
// that stopped mid-send) is never sent again. The child's own history still
// holds the result; `sessions last <id>` reads it.

const (
	parentReportInterval    = 3 * time.Second
	parentReportMaxAttempts = 12
	parentReportTextLimit   = 1200
	parentReportClient      = "sessions-report"
)

type parentSubscription struct {
	Child       string `json:"child"`
	Parent      string `json:"parent"`
	CreatedAtMS int64  `json:"created_at_ms"`
}

type parentReport struct {
	OperationID   string `json:"operation_id"`
	Child         string `json:"child"`
	Parent        string `json:"parent"`
	Text          string `json:"text"`
	CreatedAtMS   int64  `json:"created_at_ms"`
	OutcomeAtMS   int64  `json:"outcome_at_ms"`
	Attempts      int    `json:"attempts,omitempty"`
	NextAttemptMS int64  `json:"next_attempt_ms,omitempty"`
	LastReason    string `json:"last_reason,omitempty"`
}

type parentReports struct {
	root string
	now  func() time.Time
	wake chan struct{}

	mu     sync.Mutex
	subs   map[string]parentSubscription
	loaded bool
}

func newParentReports(root string) *parentReports {
	return &parentReports{
		root: filepath.Join(root, "parent-reports"), now: time.Now,
		wake: make(chan struct{}, 1), subs: make(map[string]parentSubscription),
	}
}

func (p *parentReports) subscriptionDir() string { return filepath.Join(p.root, "subscriptions") }
func (p *parentReports) outboxDir() string       { return filepath.Join(p.root, "outbox") }

func (p *parentReports) loadLocked() {
	if p.loaded {
		return
	}
	p.loaded = true
	entries, _ := os.ReadDir(p.subscriptionDir())
	for _, entry := range entries {
		var sub parentSubscription
		if readJSONFile(filepath.Join(p.subscriptionDir(), entry.Name()), &sub) == nil && sub.Child != "" && sub.Parent != "" {
			p.subs[sub.Child] = sub
		}
	}
}

func (p *parentReports) subscribe(child, parent string) error {
	if child == "" || parent == "" || child == parent || !safeReportName(child) {
		return errors.New("a parent report needs a distinct child and parent session")
	}
	sub := parentSubscription{Child: child, Parent: parent, CreatedAtMS: p.now().UnixMilli()}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.loadLocked()
	if err := writeJSONFileAtomic(filepath.Join(p.subscriptionDir(), child+".json"), sub); err != nil {
		return fmt.Errorf("record parent report subscription: %w", err)
	}
	p.subs[child] = sub
	return nil
}

func (p *parentReports) parentOf(child string) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.loadLocked()
	sub, ok := p.subs[child]
	return sub.Parent, ok
}

func (p *parentReports) subscriptions() []parentSubscription {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.loadLocked()
	result := make([]parentSubscription, 0, len(p.subs))
	for _, sub := range p.subs {
		result = append(result, sub)
	}
	return result
}

func (p *parentReports) unsubscribe(child string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.loadLocked()
	delete(p.subs, child)
	_ = os.Remove(filepath.Join(p.subscriptionDir(), child+".json"))
}

// enqueue writes a report once. A second offer of the same outcome (same
// operation id) is a no-op, so a republished idle edge cannot report twice.
func (p *parentReports) enqueue(report parentReport) error {
	if err := os.MkdirAll(p.outboxDir(), 0o700); err != nil {
		return err
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		return err
	}
	path := filepath.Join(p.outboxDir(), report.OperationID+".json")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	_, writeErr := file.Write(encoded)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(path)
		return errors.Join(writeErr, closeErr)
	}
	select {
	case p.wake <- struct{}{}:
	default:
	}
	return nil
}

func (p *parentReports) pending() []parentReport {
	entries, _ := os.ReadDir(p.outboxDir())
	reports := make([]parentReport, 0, len(entries))
	for _, entry := range entries {
		var report parentReport
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		if readJSONFile(filepath.Join(p.outboxDir(), entry.Name()), &report) == nil && report.OperationID != "" {
			reports = append(reports, report)
		}
	}
	// Oldest outcome first, so one parent hears a child's turns in order.
	sort.Slice(reports, func(i, j int) bool {
		if reports[i].OutcomeAtMS != reports[j].OutcomeAtMS {
			return reports[i].OutcomeAtMS < reports[j].OutcomeAtMS
		}
		return reports[i].CreatedAtMS < reports[j].CreatedAtMS
	})
	return reports
}

func (p *parentReports) save(report parentReport) error {
	return writeJSONFileAtomic(filepath.Join(p.outboxDir(), report.OperationID+".json"), report)
}

func (p *parentReports) remove(report parentReport) {
	_ = os.Remove(filepath.Join(p.outboxDir(), report.OperationID+".json"))
}

// parentReportOperationID derives a stable UUID v4-shaped delivery id from the
// outcome, so the delivery store recognizes a repeat of the same report.
func parentReportOperationID(child string, at int64) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("sessions-parent-report\x00%s\x00%d", child, at)))
	value := sum[:16]
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value)
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:]
}

func formatParentReport(report sessionruntime.ChildReport) string {
	verb := "finished"
	body := strings.TrimSpace(report.Summary)
	switch report.IdleReason {
	case "needs-input":
		verb, body = "needs input", firstNonEmpty(strings.TrimSpace(report.Detail), body)
	case "failed":
		verb, body = "failed", firstNonEmpty(strings.TrimSpace(report.Detail), body)
	}
	if body == "" {
		body = "(no summary)"
	}
	if utf8.RuneCountInString(body) > parentReportTextLimit {
		body = string([]rune(body)[:parentReportTextLimit]) + "…"
	}
	return fmt.Sprintf("[sessions] Delegated lane %q (%s) %s:\n%s\n\nFull result: sessions last %s · Link for the user: sessions://lane/%s",
		report.Name, report.SessionID, verb, body, report.SessionID, report.SessionID)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func safeReportName(id string) bool {
	return id != "" && !strings.ContainsAny(id, `/\.`) && len(id) <= 128
}

func readJSONFile(path string, target any) error {
	encoded, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, target)
}

func writeJSONFileAtomic(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, encoded, 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}
