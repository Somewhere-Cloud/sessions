package state

import (
	"encoding/json"

	"github.com/somewhere-tech/sessions/runtime/internal/proto"
)

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneInt64Pointer(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneUint64Pointer(value *uint64) *uint64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneFloat64Pointer(value *float64) *float64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneIntPointer(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

type SessionTool string

const (
	ToolClaude              SessionTool = "claude-code"
	ToolCodex               SessionTool = "codex"
	ToolTerminal            SessionTool = "terminal"
	ToolLane                SessionTool = "lane"
	KindLane                            = "lane"
	KindCodexAppServer                  = "codex-app-server"
	KindClaudeStructured                = "claude-structured"
	DescriptionExplicit                 = "explicit"
	DescriptionFirstMessage             = "first-message"
)

// Where a session's stored name came from, and therefore who may change it.
//
// A session card used to keep whatever name it was launched with while Claude
// titled the same conversation something else everywhere the provider itself
// showed it, so searching Sessions for the name the user could see in Claude
// found nothing. The name now follows the provider's own conversation title
// unless a person has said otherwise.
//
// NameSourceLaunch is also the value an older session with no recorded source
// takes, which makes the change migration-free: an untouched session simply
// becomes adoptable. That default cannot distinguish a session whose name was
// set by an earlier `sessions rename` from one that still carries its launch
// name, so such a session can be adopted once by the provider title. Renaming
// it again pins it permanently, and that is the whole cost of not writing a
// migration for a field that did not exist.
const (
	NameSourceLaunch   = "launch"
	NameSourceProvider = "provider"
	NameSourceExplicit = "explicit"
	// NameSourceGenerated is a short title Sessions wrote itself, once, from
	// the first exchange of a session nobody named. It is the weakest claim
	// on the card: the provider's own title replaces it and a rename pins
	// over it. See internal/autotitle.
	NameSourceGenerated = "generated"
)

// AutoTitleStatus reports the daemon's automatic title attempt for one
// session. It is the only place a failure appears: generation never blocks or
// interrupts the session, so an unavailable CLI or a timeout is recorded here
// and nowhere else. It lives in memory only; a restarted daemon may try once
// more after the session's next completed turn.
type AutoTitleStatus struct {
	// State is generating, generated, or failed.
	State    string `json:"state"`
	Provider string `json:"provider,omitempty"`
	Error    string `json:"error,omitempty"`
	At       int64  `json:"at"`
}

// WorkspaceGit is a bounded cached observation of the session cwd on this host.
// Branch remains creation provenance; this observation never replaces it.
type WorkspaceGit struct {
	IconDataURL string `json:"iconDataUrl,omitempty"`
	State       string `json:"state"`
	Branch      string `json:"branch,omitempty"`
	Revision    string `json:"revision,omitempty"`
	ObservedAt  int64  `json:"observedAt"`
}

type SessionInfo struct {
	WorkspaceGit      *WorkspaceGit     `json:"workspaceGit,omitempty"`
	ID                string            `json:"id"`
	Name              string            `json:"name,omitempty"`
	NameSource        string            `json:"name_source,omitempty"`
	Description       string            `json:"description"`
	DescriptionSource string            `json:"description_source,omitempty"`
	Tags              map[string]string `json:"tags,omitempty"`
	Kind              string            `json:"kind,omitempty"`
	SpecPath          string            `json:"specPath,omitempty"`
	Cmd               string            `json:"cmd"`
	Args              []string          `json:"args"`
	Cwd               string            `json:"cwd"`
	Profile           string            `json:"profile,omitempty"`
	ConfigDir         string            `json:"config_dir,omitempty"`
	WorktreePath      string            `json:"worktree_path,omitempty"`
	Branch            string            `json:"branch,omitempty"`
	Base              string            `json:"base,omitempty"`
	SourceRepo        string            `json:"source_repo,omitempty"`
	Cols              int               `json:"cols"`
	Rows              int               `json:"rows"`
	CreatedAt         int64             `json:"createdAt"`
	PID               int               `json:"pid"`
	RunnerProtocol    int               `json:"runnerProtocol"`
	RunnerVersion     string            `json:"runnerVersion,omitempty"`
	MessageSubmit     bool              `json:"messageSubmit,omitempty"`
	MessageQueue      bool              `json:"messageQueue,omitempty"`
	Tool              SessionTool       `json:"tool"`
	// Launching is a process-local Create call with a recorded identity but
	// no registered runner yet. It does not claim the command started.
	Launching  bool  `json:"launching,omitempty"`
	Working    bool  `json:"working"`
	LastDataAt int64 `json:"lastDataAt"`
	// LastDataUnknown marks a record restored from the ledger with no
	// recorded human or provider activity. LastDataAt then holds CreatedAt
	// as a floor, not an observation of output.
	LastDataUnknown bool `json:"lastDataUnknown,omitempty"`
	// LastUserMessageAt is transcript-derived and says nothing about who wrote
	// the message. It moves whenever a user-role record appears in the
	// provider's own conversation file, and a provider writes those records for
	// its own internal injections too: a scheduled prompt, a cron tick, a
	// reminder it decided to inject. None of those cross Sessions' input path,
	// and in the transcript they are indistinguishable from a person typing.
	//
	// So this answers "when did this conversation last take a user turn", which
	// is not the question anything asking "when did the user last touch this"
	// wants. For that read LastHumanMessageAt, and do not treat this field as a
	// proxy for human contact.
	LastUserMessageAt *int64 `json:"lastUserMessageAt"`
	// LastHumanMessageAt and LastAgentMessageAt are daemon-owned, and stamped at
	// Sessions' own input boundary rather than read back out of the provider's
	// transcript. That boundary is what makes them trustworthy: a provider's
	// internal injection is written straight into the transcript and never
	// crosses it, so it can move LastUserMessageAt and neither of these.
	//
	// Everything a person sends does cross it -- the HTTP input and submit
	// routes, the WebSocket mux, an attached terminal, `sessions send` -- and
	// arrives with no source-session attribution. An agent relaying to another
	// session arrives through the same routes carrying one. Unattributed is
	// therefore a person, attributed is another session, and a transcript-only
	// user record is neither.
	LastHumanMessageAt *int64 `json:"lastHumanMessageAt"`
	LastAgentMessageAt *int64 `json:"lastAgentMessageAt"`
	IdleReason         string `json:"idleReason,omitempty"`
	IdleDetail         string `json:"idleDetail,omitempty"`
	IdleSince          *int64 `json:"idleSince,omitempty"`
	LastSummary        string `json:"lastSummary,omitempty"`
	FailureKind        string `json:"failureKind,omitempty"`
	FailureDetail      string `json:"failureDetail,omitempty"`
	FailureProvider    string `json:"failureProvider,omitempty"`
	// FailureEvidence is the provider's own line this failure was read from.
	// Surfaces show it, because a claim about somebody's provider they cannot
	// trace to their own screen is one Sessions should not be making.
	FailureEvidence string               `json:"failureEvidence,omitempty"`
	FailureAt       int64                `json:"failureAt,omitempty"`
	Retry           *proto.ProviderRetry `json:"retry,omitempty"`
	// LimitRecovery is the daemon's plan to resume this session when its
	// usage limit resets, and the latest outcome. It is derived from the
	// limit recovery store in the state root and never written to metadata.
	LimitRecovery *LimitRecovery `json:"limitRecovery,omitempty"`
	// RestartContinuation is the daemon's record of carrying this session on
	// after a reboot, update, or crash cut its turn short. It is derived from
	// the restart continuation store and never written to metadata.
	RestartContinuation *RestartContinuation `json:"restartContinuation,omitempty"`
	// UsageCap says this session's account is at or past the usage cap, so
	// its next turn waits, or that Sessions moved its work to another account.
	// It is derived by the daemon and never written to metadata.
	UsageCap *UsageCapHold `json:"usageCap,omitempty"`
	// PendingApproval is the permission a Rich lane is waiting on right now.
	// It is derived from the lane's structured stream, so it survives a
	// daemon restart as long as the runner is still holding the request.
	PendingApproval *ApprovalPrompt `json:"pendingApproval,omitempty"`
	// Start is the delegated-start receipt for a session created with an
	// operation id: created, first request delivered, working, completed or
	// blocked, each with its evidence and one safe recovery. See start.go.
	Start *StartReceipt `json:"start,omitempty"`
	// Exited means Sessions reaped a real status for this session's process:
	// an exit code, a signal, or a user-requested end that completed. It is
	// never set because the daemon lost contact. Losing a socket says nothing
	// about whether the process on the other end is still working.
	Exited     bool    `json:"exited"`
	ExitCode   *int    `json:"exitCode"`
	ExitSignal *string `json:"exitSignal"`
	ExitReason string  `json:"exitReason,omitempty"`
	ExitedAt   *int64  `json:"exitedAt"`
	// Unreachable means the daemon cannot currently talk to this session's
	// runner: the socket read failed, the read deadline elapsed, or the daemon
	// restarted out from under a healthy runner. It is a statement about the
	// connection, not about the work. An unreachable session is still a
	// session: it is listed, readable, and attachable, and reconnect or the
	// next discovery pass may reattach it. It is never presented as ended.
	Unreachable       bool   `json:"unreachable,omitempty"`
	UnreachableReason string `json:"unreachableReason,omitempty"`
	UnreachableSince  *int64 `json:"unreachableSince,omitempty"`
	// RunnerGone is stronger than Unreachable: the daemon's identity-aware
	// process probe found no process belonging to this session. It is derived
	// for listings and never turns Exited true, because a missing process still
	// supplies no exit status.
	RunnerGone bool `json:"runnerGone,omitempty"`
	// LostReason says why a runner is gone, and LostAtMS when. A session shown
	// as "lost" with nothing else is a dead end: after the owner's MacBook
	// rebooted, seven sessions read that way and the daemon knew perfectly well
	// that the machine had restarted under them. One of:
	// "machine rebooted", "runner exited", "daemon lost contact".
	LostReason        string `json:"lostReason,omitempty"`
	LostAtMS          int64  `json:"lostAt,omitempty"`
	ClaudeCustomTitle string `json:"claudeCustomTitle,omitempty"`
	ClaudeAITitle     string `json:"claudeAiTitle,omitempty"`
	// AutoTitle is the last automatic title attempt, absent when Sessions
	// has not tried to title this session.
	AutoTitle              *AutoTitleStatus `json:"autoTitle,omitempty"`
	OnIdle                 string           `json:"onIdle,omitempty"`
	Model                  string           `json:"model,omitempty"`
	Effort                 string           `json:"effort,omitempty"`
	Fast                   bool             `json:"fast,omitempty"`
	ConversationID         string           `json:"conversationId,omitempty"`
	RemoteEndpoint         string           `json:"remoteEndpoint,omitempty"`
	ClaudeSessionID        string           `json:"claudeSessionId,omitempty"`
	ContinuedFromHistoryID string           `json:"continuedFromHistoryId,omitempty"`
	ContinuedFromProvider  string           `json:"continuedFromProvider,omitempty"`
	ContinuationMode       string           `json:"continuationMode,omitempty"`
	ImportedMessageCount   int              `json:"importedMessageCount,omitempty"`
	CreatorKind            string           `json:"creator_kind,omitempty"`
	CreatorID              string           `json:"creator_id,omitempty"`
	ParentSessionID        string           `json:"parent_session_id,omitempty"`
	// ReportsToParent means the daemon delivers this child's idle outcomes to
	// ParentSessionID (`sessions new --report`).
	ReportsToParent bool   `json:"reports_to_parent,omitempty"`
	DelegationKind  string `json:"delegation_kind,omitempty"`
	Permissions     string `json:"permissions,omitempty"`
	Lifecycle       string `json:"lifecycle,omitempty"`
	// DisplayParentSessionID is a user-controlled organizational override.
	// nil preserves the creator-ledger hierarchy, a pointer to "" makes the
	// session a visual root, and any other value groups it under that session.
	// It never rewrites trusted creator provenance.
	DisplayParentSessionID *string `json:"display_parent_session_id,omitempty"`
	// SetAsideAt removes a live session from the default working set without
	// changing runner lifecycle, attention, search, or notification truth.
	SetAsideAt *int64 `json:"setAsideAt,omitempty"`
	// SetAsideUntil is when the daemon brings the session back on its own.
	// Absent means it stays aside until someone brings it back.
	SetAsideUntil *int64 `json:"setAsideUntil,omitempty"`
	// Pinned is the user marking a workbench: the session sorts first in every
	// listing and automatic termination keeps its hands off it. It is never
	// omitempty, matching working and exited rather than fast, because an agent
	// choosing what to end has to tell "this session is not pinned" from "this
	// daemon never told me", and an omitted field reads as both.
	Pinned bool `json:"pinned"`
	// SeenAt is when a person last opened this session through a Sessions
	// client or `sessions seen`. AttentionAt is when the daemon last reported
	// that the agent finished a turn, failed, or needs input -- the same
	// moments a desktop notification is queued for. MarkedUnread is an
	// explicit "Mark as unread".
	SeenAt       *int64 `json:"seenAt,omitempty"`
	AttentionAt  *int64 `json:"attentionAt,omitempty"`
	MarkedUnread bool   `json:"markedUnread,omitempty"`
	// Unread is derived from the three fields above by the daemon, so every
	// client and the CLI agree on it. Always present, like pinned, so a caller
	// can tell "read" from a daemon that predates the field.
	Unread bool `json:"unread"`
	// MemoryBytes, CPUPercent and ResourceProcesses are what this session costs
	// the machine: resident memory across its whole process tree, percent of
	// one core burned since the previous sample, and how many processes those
	// two numbers cover.
	//
	// All three are pointers, and that is the point of them. A session with no
	// live process, a process the OS refused to describe, and a first sample
	// with nothing to subtract from are all genuinely unknown, and a reader
	// that cannot tell unknown from zero will conclude a machine is idle when
	// it is paging. nil is the only honest encoding of "Sessions does not
	// know"; omitempty keeps it off the wire entirely rather than sending a
	// null that a lenient client would coerce to 0.
	//
	// CPUPercent is a rate measured between two samples, never a lifetime
	// average. 100 is one core saturated; a tree spanning cores reads above
	// 100. See internal/resource.
	MemoryBytes       *uint64  `json:"memoryBytes,omitempty"`
	CPUPercent        *float64 `json:"cpuPercent,omitempty"`
	ResourceProcesses *int     `json:"resourceProcesses,omitempty"`
	// ResourceSampledAt is when the three fields above were measured, in unix
	// milliseconds. It travels with them because a sample is only ever as
	// current as the last tick, and a listing that presents a stale number as
	// live is the same category of lie as presenting unknown as zero.
	ResourceSampledAt  *int64   `json:"resourceSampledAt,omitempty"`
	CreatorAncestry    []string `json:"creator_ancestry,omitempty"`
	RootCreatorKind    string   `json:"root_creator_kind,omitempty"`
	RootCreatorID      string   `json:"root_creator_id,omitempty"`
	ProvenanceStatus   string   `json:"provenance_status,omitempty"`
	ReopenedAs         string   `json:"reopened_as,omitempty"`
	ResumedFrom        string   `json:"resumed_from,omitempty"`
	MovedToEndpoint    string   `json:"moved_to_endpoint,omitempty"`
	MovedToSessionID   string   `json:"moved_to_session_id,omitempty"`
	MovedFromEndpoint  string   `json:"moved_from_endpoint,omitempty"`
	MovedFromSessionID string   `json:"moved_from_session_id,omitempty"`
	EndedByKind        string   `json:"ended_by_kind,omitempty"`
	EndedByID          string   `json:"ended_by_id,omitempty"`
	EndedByName        string   `json:"ended_by_name,omitempty"`
	EndedByClient      string   `json:"ended_by_client,omitempty"`
	EndReason          string   `json:"end_reason,omitempty"`
	EndOperationID     string   `json:"end_operation_id,omitempty"`

	// TaskRefs are the task references recorded in the ledger for the session
	// and its pull request links. They are stated links, not activity, and
	// never move lastDataAt.
	TaskRefs []TaskRef `json:"taskRefs,omitempty"`
	// PullRequest summarizes the session's primary linked pull request as last
	// recorded in the ledger, so a listing can show it without a call per
	// session. Absent when no pull request is linked.
	PullRequest *PullRequestSummary `json:"pullRequest,omitempty"`

	// Observations is the factual, runtime-aware view of what Sessions has
	// observed about this session, with sources. See observations.go.
	Observations *Observations `json:"observations,omitempty"`
}

// ApprovalPrompt is one permission request a structured provider is holding
// open until Sessions answers it.
type ApprovalPrompt struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Summary string `json:"summary"`
	Command string `json:"command,omitempty"`
	Cwd     string `json:"cwd,omitempty"`
	Reason  string `json:"reason,omitempty"`
	At      int64  `json:"at"`
}

const (
	IdleReasonNeverStarted = "never-started"
	IdleReasonCompleted    = "completed"
	IdleReasonNeedsInput   = "needs-input"
	IdleReasonFailed       = "failed"
	PermissionsInherit     = "inherit"
	PermissionsConstrained = "constrained"
	PermissionsFull        = "full"
	LifecycleTask          = "task"
	LifecycleSession       = "session"
)

// TaskRef is one recorded task reference as listings carry it. PullRequest is
// set when the reference belongs to that linked pull request rather than the
// session itself. Scope is github.com/OWNER/REPO, somewhere:<project>, or
// unresolved; Ambiguous marks a task ID linked on the same target under more
// than one scope.
type TaskRef struct {
	Ref         string `json:"ref"`
	Scope       string `json:"scope"`
	Project     string `json:"project,omitempty"`
	PullRequest string `json:"pull_request,omitempty"`
	Ambiguous   bool   `json:"ambiguous,omitempty"`
	// InheritedFrom names the earlier session this one continues the same
	// conversation of, when the reference was linked there.
	InheritedFrom string `json:"inherited_from,omitempty"`
}

type CreateSessionRequest struct {
	Cmd  string   `json:"cmd,omitempty"`
	Args []string `json:"args,omitempty"`
	// InitialInput is a provider-authored first request already present in Args.
	// It is a watcher binding hint, not a second input to deliver, and is never
	// persisted separately from the provider transcript.
	InitialInput string `json:"initialInput,omitempty"`
	// OperationID makes creation idempotent: a second request with the same id
	// returns the session the first one created instead of starting another.
	// PromptOperationID is the delivery operation id the caller will use for
	// the first request, recorded so the start receipt can follow it.
	OperationID       string `json:"operation_id,omitempty"`
	PromptOperationID string `json:"prompt_operation_id,omitempty"`
	// RequestFingerprint is CreateRequestFingerprint of the request as the
	// caller sent it, computed by the daemon and recorded with OperationID so
	// a repeated id asking for different work is refused, not replayed.
	RequestFingerprint string            `json:"-"`
	Cwd                string            `json:"cwd,omitempty"`
	Cols               int               `json:"cols,omitempty"`
	Rows               int               `json:"rows,omitempty"`
	Env                map[string]string `json:"env,omitempty"`
	Name               string            `json:"name,omitempty"`
	Description        string            `json:"description,omitempty"`
	Tags               map[string]string `json:"tags,omitempty"`
	Profile            string            `json:"profile,omitempty"`
	// TaskRefs are task references stated for the session (sw:tsk_<id> or
	// gh:OWNER/REPO#NUMBER), recorded in the ledger with its creation.
	// TaskProject is the Somewhere project the sw: references belong to;
	// without it their scope is reported as unresolved.
	TaskRefs    []string `json:"taskRefs,omitempty"`
	TaskProject string   `json:"taskProject,omitempty"`
	// DefaultProfile explicitly chooses the provider's default login. Without
	// it, an omitted profile lets a same-provider child start on its parent's
	// recorded account; with it, the child uses the default login instead.
	DefaultProfile bool `json:"defaultProfile,omitempty"`
	// UsageCapNote is set by the daemon when the usage cap moved this start
	// to another account; it is shown on the new session, never read from a
	// caller.
	UsageCapNote *UsageCapHold `json:"-"`
	Worktree     bool          `json:"worktree,omitempty"`
	Base         string        `json:"base,omitempty"`
	// NoWorktree declines the worktree an agent-created child would otherwise
	// get by default, so a lane can deliberately share its manager's checkout.
	NoWorktree bool   `json:"noWorktree,omitempty"`
	Kind       string `json:"kind,omitempty"`
	SpecPath   string `json:"specPath,omitempty"`
	OnIdle     string `json:"onIdle,omitempty"`
	// ReportToParent asks the daemon to deliver each of this child's idle
	// outcomes to the session that created it. It is honored only for a
	// child created from inside another session.
	ReportToParent bool `json:"reportToParent,omitempty"`
	WaitReady      bool `json:"waitReady,omitempty"`
	Force          bool `json:"force,omitempty"`
	// ProviderTerminal is a request-only escape hatch for an agent-created
	// Claude child that genuinely needs the provider's interactive terminal.
	// It is never persisted as a session kind: the normal PTY kind remains
	// empty for compatibility with existing runners and clients.
	ProviderTerminal bool                  `json:"providerTerminal,omitempty"`
	Claude           *ClaudeSessionOptions `json:"claude,omitempty"`
	// CreatorSessionID and CreatorOwnerID are populated from trusted HTTP
	// headers at the daemon boundary. They are deliberately not JSON fields.
	CreatorSessionID string `json:"-"`
	CreatorOwnerID   string `json:"-"`
	// DelegationKind records whether a child was explicitly requested by a
	// person or created by its parent agent. It is presentation provenance,
	// not an authorization principal.
	DelegationKind string `json:"delegationKind,omitempty"`
	// Permissions is resolved by the daemon. "inherit" is accepted only for a
	// child and can never give that child more access than its parent unless the
	// user explicitly enabled autonomous delegated work.
	Permissions string `json:"permissions,omitempty"`
	// Lifecycle records caller intent. Provider completion is never permission
	// to end a runtime; only an explicit End closes it.
	Lifecycle string `json:"lifecycle,omitempty"`
	// DisplayParentSessionID is copied only by trusted recovery code. It is
	// never accepted from the public create-session JSON body.
	DisplayParentSessionID *string `json:"-"`
	ConversationID         string  `json:"-"`
	ConfigDir              string  `json:"-"`
	WorktreePath           string  `json:"-"`
	WorktreeBranch         string  `json:"-"`
	WorktreeBase           string  `json:"-"`
	// WorktreeDefaulted marks a worktree the daemon chose for a delegated lane
	// rather than one the caller asked for. A defaulted worktree that cannot be
	// created (folder is not a usable Git checkout) degrades to the shared
	// folder; an explicitly requested one still fails loudly.
	WorktreeDefaulted bool                 `json:"-"`
	SourceRepo        string               `json:"-"`
	Continuation      *ContinuationContext `json:"-"`
}

// EndSessionRequest is durable audit context for an explicit termination.
// Initiator fields are derived from the authenticated transport, not accepted
// from the public JSON body.
type EndSessionRequest struct {
	InitiatorKind string
	InitiatorID   string
	InitiatorName string
	Client        string
	Reason        string
	OperationID   string
}

// InputAttribution is trusted transport provenance for one text submission.
// It is never accepted from the JSON body.
type InputAttribution struct {
	SourceSessionID string
	Client          string
	// Automation names a daemon-owned sender, such as idle compaction. Its
	// input is neither a person's nor another lane's: it stamps no message
	// principal and records provider activity rather than human input.
	Automation string
}

// ClaudeSessionOptions are explicit per-session overrides. Empty fields use
// the persisted Sessions default; "inherit" asks Sessions to defer to Claude
// itself for that setting.
type ClaudeSessionOptions struct {
	RemoteControl           string `json:"remoteControl,omitempty"`
	PermissionMode          string `json:"permissionMode,omitempty"`
	Model                   string `json:"model,omitempty"`
	Effort                  string `json:"effort,omitempty"`
	Chrome                  string `json:"chrome,omitempty"`
	SomewhereMCP            string `json:"somewhereMcp,omitempty"`
	RemoteControlNamePrefix string `json:"remoteControlNamePrefix,omitempty"`
}

type ClaudeEventsWindow struct {
	Events     []json.RawMessage
	NextIndex  int64
	TotalCount int64
	StartIndex int64
	EndIndex   int64
}

// LimitRecovery is one session's resume-after-usage-limit plan as session
// reads show it. State is scheduled, sending, sent, unconfirmed, failed,
// cancelled, or stopped.
type LimitRecovery struct {
	State     string `json:"state"`
	ResumeAt  int64  `json:"resumeAt,omitempty"`
	ResetAt   int64  `json:"resetAt,omitempty"`
	Source    string `json:"source,omitempty"`
	Attempt   int    `json:"attempt,omitempty"`
	Detail    string `json:"detail,omitempty"`
	UpdatedAt int64  `json:"updatedAt,omitempty"`
}

// RestartContinuation is one session's continue-after-restart record as
// session reads show it. Reason is reboot, update, crash, or unknown; State
// is pending, reopening, sending, sent, unconfirmed, failed, skipped, or
// stopped. SourceID and TargetID link a lost session to the one it was
// reopened as.
type RestartContinuation struct {
	Reason        string `json:"reason"`
	Evidence      string `json:"evidence,omitempty"`
	Kind          string `json:"kind,omitempty"`
	State         string `json:"state"`
	SourceID      string `json:"sourceId,omitempty"`
	TargetID      string `json:"targetId,omitempty"`
	Attempt       int    `json:"attempt,omitempty"`
	Detail        string `json:"detail,omitempty"`
	InterruptedAt int64  `json:"interruptedAt,omitempty"`
	SentAt        int64  `json:"sentAt,omitempty"`
	UpdatedAt     int64  `json:"updatedAt,omitempty"`
}

// Usage cap hold states.
const (
	// UsageCapWaiting: the account is at or past the cap; the next turn waits
	// for its window to reset or for a switch to another account.
	UsageCapWaiting = "waiting"
	// UsageCapSwitched: Sessions started or continued this work on another
	// account because of the cap or a provider limit.
	UsageCapSwitched = "switched"
)

// UsageCapHold is one session's usage cap state as session reads show it.
type UsageCapHold struct {
	State string `json:"state"`
	// Tool and Account name the account the verdict is about ("" is the
	// provider's default login); AccountLabel is how people know it.
	Tool         string `json:"tool,omitempty"`
	Account      string `json:"account"`
	AccountLabel string `json:"accountLabel,omitempty"`
	UsedPercent  int    `json:"usedPercent,omitempty"`
	Cap          int    `json:"cap,omitempty"`
	Window       string `json:"window,omitempty"`
	ResetsAt     int64  `json:"resetsAt,omitempty"`
	Detail       string `json:"detail,omitempty"`
	// SwitchAccount is an account with headroom Sessions would move to
	// ("" is the default login), present when SwitchLabel is.
	SwitchAccount string `json:"switchAccount,omitempty"`
	SwitchLabel   string `json:"switchLabel,omitempty"`
	// SwitchedTo is the session continuing this work on another account;
	// SwitchedFrom is the account a switched start or continuation left.
	SwitchedTo   string `json:"switchedTo,omitempty"`
	SwitchedFrom string `json:"switchedFrom,omitempty"`
	UpdatedAt    int64  `json:"updatedAt,omitempty"`
}

// PullRequestSummary is the last recorded state of a session's primary linked
// pull request. State and Checks use the values of `/api/sessions/:id/pr`;
// "unknown" means it has not been read yet. AllMerged is true only when every
// linked pull request is known to be merged.
type PullRequestSummary struct {
	URL        string `json:"url"`
	Number     int    `json:"number"`
	State      string `json:"state"`
	Draft      bool   `json:"draft,omitempty"`
	Checks     string `json:"checks"`
	Others     int    `json:"others,omitempty"`
	AllMerged  bool   `json:"allMerged"`
	ObservedAt int64  `json:"observedAt,omitempty"`
}
