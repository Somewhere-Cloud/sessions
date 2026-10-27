package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/delivery"
	sessionruntime "github.com/somewhere-tech/sessions/runtime/internal/session"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

type childReportSource interface {
	SetChildReportHandler(func(sessionruntime.ChildReport))
}

// StartParentReports connects the session manager's child outcomes to the
// parent-report outbox and delivers pending reports until ctx ends. Reports
// written before a restart are delivered by the next daemon.
func (s *Server) StartParentReports(ctx context.Context, logf func(string, ...any)) {
	if source, ok := s.registry.(childReportSource); ok {
		source.SetChildReportHandler(func(report sessionruntime.ChildReport) {
			s.offerParentReport(report, logf)
		})
		go func() {
			<-ctx.Done()
			source.SetChildReportHandler(nil)
		}()
	}
	go func() {
		ticker := time.NewTicker(parentReportInterval)
		defer ticker.Stop()
		for {
			s.deliverParentReports(ctx, logf)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			case <-s.parentReports.wake:
			}
		}
	}()
}

// subscribeParentReport records that a newly created child reports to its
// parent. A session created from outside any session has no parent to report
// to, so the request changes nothing and the record says so.
func (s *Server) subscribeParentReport(info state.SessionInfo) state.SessionInfo {
	if info.ParentSessionID == "" {
		return info
	}
	if parent, ok := s.registry.Get(info.ParentSessionID); !ok || !reportableParent(parent.Info()) {
		// A report is typed into its parent; into a shell it would run as a command.
		return info
	}
	if err := s.parentReports.subscribe(info.ID, info.ParentSessionID); err != nil {
		log.Printf("[parent-report] %s will not report to %s: %v; use `sessions wait %s` instead", info.ID, info.ParentSessionID, err, info.ID)
		return info
	}
	info.ReportsToParent = true
	return info
}

func (s *Server) offerParentReport(report sessionruntime.ChildReport, logf func(string, ...any)) {
	parent, ok := s.parentReports.parentOf(report.SessionID)
	if !ok || (report.ParentSessionID != "" && parent != report.ParentSessionID) {
		return
	}
	err := s.parentReports.enqueue(parentReport{
		OperationID: parentReportOperationID(report.SessionID, report.At),
		Child:       report.SessionID, Parent: parent,
		Text: formatParentReport(report), CreatedAtMS: s.parentReports.now().UnixMilli(),
		OutcomeAtMS: report.At,
	})
	if err != nil {
		logf("[parent-report] could not record %s's outcome for %s: %v; the result stays in the lane, read it with `sessions last %s`", report.SessionID, parent, err, report.SessionID)
	}
}

// deliverParentReports sends at most one report to each idle parent per pass:
// a delivered report starts the parent's next turn, and the next report waits
// for that turn to end.
func (s *Server) deliverParentReports(ctx context.Context, logf func(string, ...any)) {
	s.pruneParentSubscriptions()
	served := make(map[string]bool)
	for _, report := range s.parentReports.pending() {
		if ctx.Err() != nil {
			return
		}
		if served[report.Parent] || report.NextAttemptMS > s.parentReports.now().UnixMilli() {
			continue
		}
		ready, drop := s.parentReady(report.Parent)
		if drop != "" {
			logf("[parent-report] dropped report from %s: %s; read it with `sessions last %s`", report.Child, drop, report.Child)
			s.parentReports.remove(report)
			continue
		}
		if !ready {
			continue
		}
		served[report.Parent] = true
		s.attemptParentReport(ctx, report, logf)
	}
}

// parentReady says whether the parent can take a report now, or why the
// report can never be delivered.
func (s *Server) parentReady(parent string) (ready bool, drop string) {
	current, ok := s.registry.Get(parent)
	if !ok {
		if s.registry.IsDiscovering() {
			return false, ""
		}
		return false, "parent session " + parent + " is not known to this daemon"
	}
	info := current.Info()
	if !reportableParent(info) {
		return false, "parent session " + parent + " is not a Claude or Codex session"
	}
	if info.Exited {
		return false, "parent session " + parent + " has ended"
	}
	if info.Working || info.Unreachable || info.IdleReason == state.IdleReasonNeedsInput {
		return false, ""
	}
	return true, ""
}

func (s *Server) attemptParentReport(ctx context.Context, report parentReport, logf func(string, ...any)) {
	status, retry, reason := s.submitParentReport(ctx, report)
	switch {
	case status == delivery.StatusAccepted:
		s.parentReports.remove(report)
	case status == delivery.StatusNotDelivered && retry:
		report.Attempts++
		report.LastReason = reason
		if report.Attempts >= parentReportMaxAttempts {
			logf("[parent-report] gave up delivering %s's report to %s after %d attempts: %s; read it with `sessions last %s`", report.Child, report.Parent, report.Attempts, reason, report.Child)
			s.parentReports.remove(report)
			return
		}
		backoff := time.Duration(1<<min(report.Attempts, 6)) * 5 * time.Second
		report.NextAttemptMS = s.parentReports.now().Add(backoff).UnixMilli()
		if err := s.parentReports.save(report); err != nil {
			logf("[parent-report] could not record a retry for %s: %v", report.OperationID, err)
		}
	default:
		// Uncertain or refused for good: never resend. The child keeps its result.
		if status != delivery.StatusTextOnly && status != "" {
			logf("[parent-report] report from %s to %s ended %s: %s", report.Child, report.Parent, status, reason)
		}
		s.parentReports.remove(report)
	}
}

// submitParentReport sends the report through this daemon's own submit route,
// attributed to the child, so it gets the same receipts, attribution and
// per-session ordering as `sessions send` from that lane.
func (s *Server) submitParentReport(ctx context.Context, report parentReport) (delivery.Status, bool, string) {
	body, err := json.Marshal(map[string]string{"data": report.Text, "operation_id": report.OperationID})
	if err != nil {
		return delivery.StatusNotDelivered, true, err.Error()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "/api/sessions/"+report.Parent+"/submit", bytes.NewReader(body))
	if err != nil {
		return delivery.StatusNotDelivered, true, err.Error()
	}
	request.Host = net.JoinHostPort(s.config.Host, strconv.Itoa(s.config.Port))
	request.RemoteAddr = "127.0.0.1:0"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(creatorSessionHeader, report.Child)
	request.Header.Set(endClientHeader, parentReportClient)
	response := &internalResponse{header: make(http.Header), code: http.StatusOK}
	s.ServeHTTP(response, request)
	var receipt struct {
		Status string `json:"status"`
		Retry  bool   `json:"retry"`
		Reason string `json:"reason"`
		Error  string `json:"error"`
	}
	if json.Unmarshal(response.body.Bytes(), &receipt) != nil || receipt.Status == "" {
		return delivery.StatusNotDelivered, response.code >= 500, firstNonEmpty(receipt.Error, http.StatusText(response.code))
	}
	return delivery.Status(receipt.Status), receipt.Retry, firstNonEmpty(receipt.Reason, receipt.Error)
}

// pruneParentSubscriptions forgets children that have ended or are gone, once
// their last outcome had its chance to enter the outbox.
func (s *Server) pruneParentSubscriptions() {
	if s.registry.IsDiscovering() {
		return
	}
	for _, sub := range s.parentReports.subscriptions() {
		current, ok := s.registry.Get(sub.Child)
		if !ok || current.Info().Exited {
			s.parentReports.unsubscribe(sub.Child)
		}
	}
}

// internalResponse captures one in-process route answer.
type internalResponse struct {
	header http.Header
	code   int
	body   bytes.Buffer
}

func (r *internalResponse) Header() http.Header         { return r.header }
func (r *internalResponse) Write(p []byte) (int, error) { return r.body.Write(p) }
func (r *internalResponse) WriteHeader(code int)        { r.code = code }

// reportableParent is a provider conversation that reads a report as a message.
func reportableParent(info state.SessionInfo) bool {
	return info.Tool == state.ToolClaude || info.Tool == state.ToolCodex
}
