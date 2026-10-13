package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/somewhere-tech/sessions/runtime/internal/ledger"
	"github.com/somewhere-tech/sessions/runtime/internal/recovery"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

// These are provider-owned saved observations, not a network connection check.
// Never read or expose credentials, or rewrite Claude's reconnection records.
//
// State, Restartable, Action and Command say what the destructive restart
// would do with this record right now. A saved record that restart cannot end
// is still a record: the preview names the command that does bring it back
// rather than reading as if the runtime did not exist.
type restartPreview struct {
	SourceSessionID string          `json:"sourceSessionId"`
	State           string          `json:"state"`
	Restartable     bool            `json:"restartable"`
	Action          string          `json:"action"`
	Command         string          `json:"command,omitempty"`
	Reason          string          `json:"reason,omitempty"`
	RecordedRestart *restartRequest `json:"recordedRestart,omitempty"`
	Profile         string          `json:"profile,omitempty"`
	RemoteURL       string          `json:"remoteUrl,omitempty"`
	SavedLoginEmail string          `json:"savedLoginEmail,omitempty"`
	AccountChanged  bool            `json:"accountChanged"`
	Warning         string          `json:"warning,omitempty"`
}

func (s *Server) handleRestartPreview(w http.ResponseWriter, r *http.Request, origin string) {
	var body struct {
		SourceSessionID string `json:"sourceSessionId"`
	}
	if err := readJSON(r, &body); err != nil || body.SourceSessionID == "" {
		s.sendJSON(w, 400, map[string]any{"error": "sourceSessionId is required"}, origin)
		return
	}
	info, live, listing, found := s.restartPreviewSource(body.SourceSessionID)
	if !found {
		s.sendJSON(w, 404, map[string]any{"error": "This computer has no session record " + body.SourceSessionID +
			". Nothing was restarted. If it runs on another machine, preview it there with `sessions --machine NAME restart " +
			body.SourceSessionID + " --preview`."}, origin)
		return
	}
	result := restartPreview{SourceSessionID: info.ID, Profile: info.Profile}
	if info.Tool == state.ToolClaude {
		result = claudeRestartPreview(info)
	}
	s.describeRestartOptions(&result, info, live, listing)
	s.sendJSON(w, 200, result, origin)
}

// restartPreviewSource finds the record a preview describes. live is the test
// the destructive restart applies to its target; any other record comes from
// the same listing `sessions resume` and `sessions recover` read, including a
// session that stayed paused after a reboot. Nothing here wakes or attaches it.
func (s *Server) restartPreviewSource(id string) (state.SessionInfo, bool, []state.SessionInfo, bool) {
	source, attached := s.registry.Get(id)
	if attached && !source.HasExited() {
		return source.Info(), true, nil, true
	}
	listing := s.registry.List(true)
	for _, candidate := range listing {
		if candidate.ID == id {
			return candidate, false, listing, true
		}
	}
	if attached {
		return source.Info(), false, listing, true
	}
	if _, paused := s.pendingRestore(id); paused {
		info := state.SessionInfo{ID: id, Unreachable: true, UnreachableReason: "restart-restore-pending"}
		return info, false, listing, true
	}
	return state.SessionInfo{}, false, nil, false
}

func (s *Server) describeRestartOptions(result *restartPreview, info state.SessionInfo, live bool, listing []state.SessionInfo) {
	paused := info.UnreachableReason == "restart-restore-pending"
	result.State = restartPreviewState(info, live, paused)
	result.Action = "none"
	if s.describeRecordedRestart(result, info.ID, live) {
		return
	}
	switch {
	case live:
		describeLiveRestart(result, info)
	case info.MovedToEndpoint != "":
		result.Reason = "This conversation moved to " + info.MovedToEndpoint + restartMovedLane(info) +
			". Open it there; resuming or restarting it here would fork it."
	case !paused && info.Tool != state.ToolClaude && info.Tool != state.ToolCodex:
		result.Reason = "Restart and resume reopen a Claude or Codex conversation; this record has none. Inspect it with `sessions status " + info.ID + "`."
	default:
		describeSavedResume(result, info, listing, paused)
	}
}

func restartPreviewState(info state.SessionInfo, live, paused bool) string {
	switch {
	case live && info.Unreachable:
		return "unreachable"
	case live:
		return "running"
	case paused:
		return "needs-recovery"
	case info.MovedToEndpoint != "":
		return "moved"
	case info.Exited:
		return "exited"
	default:
		return "lost"
	}
}

func restartMovedLane(info state.SessionInfo) string {
	if info.MovedToSessionID == "" {
		return ""
	}
	return " as " + info.MovedToSessionID
}

func describeLiveRestart(result *restartPreview, info state.SessionInfo) {
	if info.Tool != state.ToolClaude && info.Tool != state.ToolCodex {
		result.Reason = "Restart requires a Claude or Codex conversation."
		return
	}
	if _, err := resolveRestartConversation(info); err != nil {
		result.Reason = "The conversation cannot be reopened, so restart would refuse and leave this runtime running: " + err.Error()
		return
	}
	result.Restartable, result.Action = true, "restart"
}

// describeSavedResume answers with the decision `sessions resume` itself makes,
// including its refusal when a successor of this record is already running.
// A reboot-paused record is answered the same way: `sessions resume` is the
// command the paused-read contract already names for it.
func describeSavedResume(result *restartPreview, info state.SessionInfo, listing []state.SessionInfo, paused bool) {
	target := resolveResumeTarget(listing, info, "")
	if target.Refusal != "" {
		result.Reason = target.Refusal
		if sessionStillRunning(target.Session) && target.Session.ID != info.ID {
			result.Action, result.Command = "open", "sessions attach "+target.Session.ID
		}
		return
	}
	result.Action, result.Command = "resume", "sessions resume "+info.ID
	result.Reason = "This runtime is no longer running, so restart has nothing to end. Resume reopens the same saved conversation with its recorded profile."
	if paused {
		result.Reason = "This session stayed paused after a reboot, so there is no running runtime to end. Resume reopens the same saved conversation with its recorded profile."
	}
}

// describeRecordedRestart reports a restart already recorded for this source.
// Repeating its exact choices is the idempotent way to finish it; resuming
// instead would start a second runtime beside the recorded replacement. The
// receipt is only read here.
func (s *Server) describeRecordedRestart(result *restartPreview, id string, live bool) bool {
	encoded, err := os.ReadFile(s.restartReceiptPath(restartOperationID(id)))
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	var receipt restartReceipt
	if err == nil {
		err = json.Unmarshal(encoded, &receipt)
	}
	if err != nil {
		result.Reason = "The restart recorded for this runtime cannot be read: " + err.Error() + ". Restore state storage before retrying."
		return true
	}
	request := receipt.Request
	result.RecordedRestart = &request
	if receipt.SourceEnded && live {
		result.Reason = "A recorded restart already ended this runtime and it is live again, so a retry would refuse to end it. Inspect `sessions status " + id + "` before choosing a new operation."
		return true
	}
	result.Restartable, result.Action = true, "retry-restart"
	result.Command = restartRetryCommand(request)
	result.Reason = "A restart of this runtime is already recorded. Repeating exactly these choices finishes it or returns its replacement without starting another runtime; different choices are refused."
	return true
}

func restartRetryCommand(request restartRequest) string {
	parts := []string{"sessions", "restart", request.SourceSessionID, "--confirm", request.ConfirmSessionID, "--permissions", request.Permissions}
	switch request.RuntimeMode {
	case "terminal":
		parts = append(parts, "--terminal")
	case "rich":
		parts = append(parts, "--structured")
	}
	if request.RemoteControl {
		parts = append(parts, "--remote-control")
	}
	return strings.Join(parts, " ")
}

func claudeRestartPreview(info state.SessionInfo) restartPreview {
	result := restartPreview{SourceSessionID: info.ID, Profile: info.Profile}
	uuid, _ := ledger.ExistingProviderResume(info.Cmd, info.Args)
	if uuid == "" {
		uuid = info.ClaudeSessionID
	}
	if uuid == "" {
		uuid = info.ConversationID
	}
	options := recovery.AdoptionOptions{}
	if info.ConfigDir != "" {
		options.ClaudeProjectsDir = filepath.Join(info.ConfigDir, "projects")
	}
	adoption, err := recovery.ResolveAdoption(uuid, options)
	if err != nil {
		result.Warning = "Could not read the recorded Remote Control account. Reopening the conversation uses this computer's current login; the previous Claude link is not guaranteed."
		return result
	}
	owner, url := recordedClaudeBridge(adoption.Path)
	result.RemoteURL = url
	home, _ := os.UserHomeDir()
	loginPath := filepath.Join(home, ".claude.json")
	if info.ConfigDir != "" {
		loginPath = filepath.Join(info.ConfigDir, ".claude.json")
	}
	account, email := savedClaudeLogin(loginPath)
	result.SavedLoginEmail = email
	result.AccountChanged = owner != "" && account != "" && owner != account
	if result.AccountChanged {
		result.Warning = "Claude's saved login differs from the recorded Remote Control owner. To keep the previous Claude-app connection, sign into its original account on this computer before reopening it. Otherwise Claude may create a different link under the current login. Local history stays intact."
	} else if owner == "" || account == "" {
		result.Warning = "The Remote Control account could not be compared. Reopening the conversation uses this computer's current login; the previous Claude link is not guaranteed."
	}
	return result
}

func savedClaudeLogin(path string) (string, string) {
	file, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer file.Close()
	var data struct {
		Account struct {
			UUID  string `json:"accountUuid"`
			Email string `json:"emailAddress"`
		} `json:"oauthAccount"`
	}
	if json.NewDecoder(io.LimitReader(file, 2<<20)).Decode(&data) != nil {
		return "", ""
	}
	return data.Account.UUID, data.Account.Email
}

// Bound work even for a 500 MB transcript. Skip a partial first line; an absent
// record is unknown, never proof that Remote Control is off or disconnected.
func recordedClaudeBridge(path string) (string, string) {
	file, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return "", ""
	}
	start := stat.Size() - (1 << 20)
	if start < 0 {
		start = 0
	}
	if _, err = file.Seek(start, io.SeekStart); err != nil {
		return "", ""
	}
	data, err := io.ReadAll(io.LimitReader(file, 1<<20))
	if err != nil {
		return "", ""
	}
	lines := bytes.Split(data, []byte{'\n'})
	if start > 0 {
		lines = lines[1:]
	}
	owner, url := "", ""
	for _, line := range lines {
		if !bytes.Contains(line, []byte(`"bridge-session"`)) && !bytes.Contains(line, []byte(`"bridge_status"`)) {
			continue
		}
		var record struct {
			Type    string `json:"type"`
			Subtype string `json:"subtype"`
			Owner   string `json:"ownerAccountUuid"`
			Bridge  string `json:"bridgeSessionId"`
			URL     string `json:"url"`
		}
		if json.Unmarshal(line, &record) != nil {
			continue
		}
		if record.Type == "bridge-session" {
			owner = record.Owner
			url = ""
			if strings.HasPrefix(record.Bridge, "cse_") && !strings.ContainsAny(record.Bridge, "/?# \t\r\n") {
				url = "https://claude.ai/code/session_" + strings.TrimPrefix(record.Bridge, "cse_")
			}
		}
		if record.Type == "system" && record.Subtype == "bridge_status" && strings.HasPrefix(record.URL, "https://claude.ai/code/") {
			url = record.URL
		}
	}
	return owner, url
}
