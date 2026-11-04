package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

// Child questions tell a manager when a lane it started stops to ask a
// question or for permission. The question is delivered through the parent
// report outbox, attributed to the child, once per waiting episode: a new
// question or approval request is a new episode, and an answer ends one. A
// question is never typed into a parent that has ended, been archived, or been
// set aside, and one the child stopped asking before delivery is dropped.
// `sessions team questions off` turns this off for the machine.

const (
	childQuestionsRoute    = "/api/team/questions"
	childQuestionsInterval = 5 * time.Second
	childQuestionKind      = "question"
)

// childWaitingEpisode names one waiting episode of a live child: an approval
// request by its id, a question by the moment it was published.
func childWaitingEpisode(info state.SessionInfo) (episode, text string, at int64, ok bool) {
	text, ok = state.WaitingFor(info)
	if !ok {
		return "", "", 0, false
	}
	if approval := info.PendingApproval; approval != nil {
		return "approval:" + approval.ID, text, approval.At, true
	}
	if info.IdleSince != nil {
		at = *info.IdleSince
	}
	return "input:" + strconv.FormatInt(at, 10), text, at, true
}

func formatChildQuestion(info state.SessionInfo, waiting string) string {
	title := firstNonEmpty(strings.TrimSpace(info.Name), "untitled")
	if utf8.RuneCountInString(waiting) > parentReportTextLimit {
		waiting = string([]rune(waiting)[:parentReportTextLimit]) + "…"
	}
	return fmt.Sprintf("[sessions] Child %s (%s) is waiting: %s\n\nReply with `sessions send %s ...` or answer in the app.",
		shortID(info.ID), title, waiting, info.ID)
}

func childQuestionOperationID(child, episode string) string {
	return parentReportOperationID(child+"\x00question\x00"+episode, 0)
}

// startChildQuestions scans for waiting children until done closes. A child
// report that a lane needs input triggers a scan sooner.
func (s *Server) startChildQuestions(done <-chan struct{}, logf func(string, ...any)) {
	go func() {
		ticker := time.NewTicker(childQuestionsInterval)
		defer ticker.Stop()
		for {
			s.scanChildQuestions("", logf)
			select {
			case <-done:
				return
			case <-ticker.C:
			}
		}
	}()
}

func (s *Server) childQuestionsEnabled() bool {
	settings, err := state.LoadSettings(s.lan.settingsPath)
	return err != nil || settings.ChildQuestionsEnabled()
}

// scanChildQuestions enqueues one question for every child whose waiting
// episode its parent has not heard about, and forgets children that are no
// longer waiting so their next question is a new episode. fresh names a child
// whose turn just ended on a question, which always starts a new episode; a
// child that stays waiting on input otherwise stays in the same one, even when
// its blocked screen is classified again.
func (s *Server) scanChildQuestions(fresh string, logf func(string, ...any)) {
	if s.registry.IsDiscovering() {
		return
	}
	p := s.parentReports
	p.scanMu.Lock()
	defer p.scanMu.Unlock()
	enabled := s.childQuestionsEnabled()
	waiting := make(map[string]bool)
	for _, info := range s.registry.List(false) {
		episode, text, at, ok := childWaitingEpisode(info)
		if info.ParentSessionID == "" || info.ParentSessionID == info.ID || !ok {
			continue
		}
		waiting[info.ID] = true
		if !newQuestionEpisode(p.questionEpisode(info.ID), episode, info.ID == fresh) {
			continue
		}
		if err := p.markQuestion(info.ID, episode); err != nil {
			logf("[child-question] could not record %s's question: %v; it stays in Waiting for you", info.ID, err)
			continue
		}
		if enabled && s.questionParentAbsent(info.ParentSessionID) == "" {
			s.enqueueChildQuestion(info, episode, text, at, logf)
		}
	}
	for _, child := range p.questionChildren() {
		if !waiting[child] {
			p.forgetQuestion(child)
		}
	}
}

// newQuestionEpisode says whether episode is one the parent has not heard.
func newQuestionEpisode(previous, episode string, fresh bool) bool {
	switch {
	case previous == episode:
		return false
	case fresh || previous == "":
		return true
	}
	return !(questionEpisodeKind(previous) == "input" && questionEpisodeKind(episode) == "input")
}

func questionEpisodeKind(episode string) string {
	kind, _, _ := strings.Cut(episode, ":")
	return kind
}

func (s *Server) enqueueChildQuestion(info state.SessionInfo, episode, text string, at int64, logf func(string, ...any)) {
	p := s.parentReports
	err := p.enqueue(parentReport{
		OperationID: childQuestionOperationID(info.ID, episode), Kind: childQuestionKind, Episode: episode,
		Child: info.ID, Parent: info.ParentSessionID, Text: formatChildQuestion(info, text),
		CreatedAtMS: p.now().UnixMilli(), OutcomeAtMS: at,
	})
	if err != nil {
		logf("[child-question] could not tell %s that %s is waiting: %v; it stays in Waiting for you", info.ParentSessionID, info.ID, err)
	}
}

// questionParentAbsent says why a parent must not hear a question at all: it
// is gone, ended, archived, or set aside. Busy parents hear it later.
func (s *Server) questionParentAbsent(parent string) string {
	current, ok := s.registry.Get(parent)
	if !ok {
		return "parent session " + parent + " is not known to this daemon"
	}
	info := current.Info()
	switch {
	case info.Exited:
		return "parent session " + parent + " has ended"
	case info.SetAsideAt != nil:
		return "parent session " + parent + " is set aside"
	}
	return ""
}

// staleChildQuestion says why a queued question should no longer be
// delivered: the child answered it, or its parent went away.
func (s *Server) staleChildQuestion(report parentReport) string {
	if report.Kind != childQuestionKind {
		return ""
	}
	if reason := s.questionParentAbsent(report.Parent); reason != "" && !s.registry.IsDiscovering() {
		return reason
	}
	current, ok := s.registry.Get(report.Child)
	if !ok {
		return "child " + report.Child + " is gone"
	}
	episode, _, _, waiting := childWaitingEpisode(current.Info())
	if !waiting || !sameQuestion(report.Episode, episode) {
		return "child " + report.Child + " is no longer waiting on that question"
	}
	return ""
}

// sameQuestion says whether the child is still waiting on the episode a
// queued question describes: the same approval, or still on input, since a
// blocked screen classified again is still the same question.
func sameQuestion(queued, current string) bool {
	if questionEpisodeKind(queued) == "input" {
		return questionEpisodeKind(current) == "input"
	}
	return queued == current
}

func (p *parentReports) questionDir() string { return filepath.Join(p.root, "questions") }

func (p *parentReports) loadQuestionsLocked() {
	if p.questionsLoaded {
		return
	}
	p.questionsLoaded = true
	p.questions = make(map[string]string)
	entries, _ := os.ReadDir(p.questionDir())
	for _, entry := range entries {
		var record struct {
			Child   string `json:"child"`
			Episode string `json:"episode"`
		}
		if readJSONFile(filepath.Join(p.questionDir(), entry.Name()), &record) == nil && record.Child != "" {
			p.questions[record.Child] = record.Episode
		}
	}
}

func (p *parentReports) questionEpisode(child string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.loadQuestionsLocked()
	return p.questions[child]
}

// markQuestion durably records that the parent was told about one episode,
// so a daemon restart does not ask the same question twice.
func (p *parentReports) markQuestion(child, episode string) error {
	if !safeReportName(child) {
		return fmt.Errorf("session id %q cannot name a question record", child)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.loadQuestionsLocked()
	record := map[string]string{"child": child, "episode": episode}
	if err := writeJSONFileAtomic(filepath.Join(p.questionDir(), child+".json"), record); err != nil {
		return err
	}
	p.questions[child] = episode
	return nil
}

func (p *parentReports) questionChildren() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.loadQuestionsLocked()
	children := make([]string, 0, len(p.questions))
	for child := range p.questions {
		children = append(children, child)
	}
	return children
}

func (p *parentReports) forgetQuestion(child string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.loadQuestionsLocked()
	delete(p.questions, child)
	_ = os.Remove(filepath.Join(p.questionDir(), child+".json"))
}

// handleChildQuestions serves GET and PUT /api/team/questions, the machine
// switch for telling parents about waiting children.
func (s *Server) handleChildQuestions(w http.ResponseWriter, r *http.Request, origin string) {
	switch r.Method {
	case http.MethodGet:
	case http.MethodPut:
		var body struct {
			Enabled *bool `json:"enabled"`
		}
		if err := readJSON(r, &body); err != nil || body.Enabled == nil {
			s.sendJSON(w, 400, map[string]any{"error": `send {"enabled": true} or {"enabled": false}`}, origin)
			return
		}
		if err := state.UpdateSettings(s.lan.settingsPath, func(settings *state.Settings) error {
			settings.ChildQuestions = &state.EnabledSetting{Enabled: *body.Enabled}
			return nil
		}); err != nil {
			s.sendJSON(w, 500, map[string]any{"error": "save child questions: " + err.Error()}, origin)
			return
		}
	default:
		s.sendJSON(w, 405, map[string]any{"error": "method not allowed"}, origin)
		return
	}
	settings, err := state.LoadSettings(s.lan.settingsPath)
	if err != nil {
		s.sendJSON(w, 500, map[string]any{"error": "read child questions: " + err.Error()}, origin)
		return
	}
	s.sendJSON(w, 200, map[string]any{"enabled": settings.ChildQuestionsEnabled(), "scope": "machine"}, origin)
}
