// Package proto defines the wire protocol shared by the ai-ssh client and
// daemon. The client (what an AI drives) is deliberately dumb: it can only
// describe an *action*, never a credential.
package proto

// Operations understood by the daemon. There is intentionally no "get
// credential" operation, and adding one would defeat the point of ai-ssh.
const (
	OpExec     = "exec"
	OpUpload   = "upload"
	OpDownload = "download"
	OpHosts    = "hosts"
	OpPing     = "ping"
	OpLock     = "lock"
	OpShutdown = "shutdown"
	// OpAttach turns the connection into an event stream for a front end (the
	// GUI): the daemon pushes approvals and reads decisions back.
	OpAttach = "attach"
	// OpDecide answers a pending approval on an attached connection.
	OpDecide = "decide"
)

// Event types pushed to attached front ends.
const (
	EventApprovalRequest = "approval_request"
	EventApprovalClosed  = "approval_closed"
)

// Request is a single client -> daemon call.
type Request struct {
	Op       string   `json:"op"`
	Host     string   `json:"host,omitempty"`
	Command  []string `json:"command,omitempty"`
	Local    string   `json:"local,omitempty"`
	Remote   string   `json:"remote,omitempty"`
	Workdir  string   `json:"workdir,omitempty"`
	Timeout  int      `json:"timeout,omitempty"` // seconds, 0 = host/default
	MaxBytes int64    `json:"max_bytes,omitempty"`
	Tail     bool     `json:"tail,omitempty"`
	Mkdir    bool     `json:"mkdir,omitempty"`
	Mode     string   `json:"mode,omitempty"`
	Verify   bool     `json:"verify,omitempty"`

	// Elevate asks the daemon to run the command through the host's
	// configured sudo/su policy. The credential stays inside the daemon.
	Elevate bool `json:"elevate,omitempty"`
	// PTY forces a pseudo terminal (needed for su, or sudo with requiretty).
	PTY bool `json:"pty,omitempty"`

	// Role marks an OpAttach front end. "gui" is the desktop window; "agent"
	// is the on-demand agent, which shows approval dialogs itself when no
	// desktop window is running. Empty is treated as "gui", so older
	// attachments keep working.
	Role string `json:"role,omitempty"`
}

// Attachment roles.
const (
	RoleGUI   = "gui"
	RoleAgent = "agent"
)

// Error codes. The client maps these onto process exit codes so an AI can
// branch without parsing prose.
const (
	ErrUsage       = "usage"
	ErrConfig      = "config"
	ErrDaemonDown  = "daemon_unavailable"
	ErrConnect     = "connect_failed"
	ErrAuth        = "auth_failed"
	ErrHostKey     = "host_key_mismatch"
	ErrUnknownHost = "unknown_host"
	ErrBlocked     = "blocked"
	ErrRemote      = "remote_failed"
	ErrTransfer    = "transfer_failed"
	ErrTimeout     = "timeout"
	ErrPathDenied  = "path_denied"
	ErrUnsupported = "unsupported"
	ErrElevation   = "elevation_failed"
	ErrElevDenied  = "elevation_denied"
	// ErrApprovalDenied: a human said no.
	ErrApprovalDenied = "approval_denied"
	// ErrApprovalTimeout: nobody answered in time.
	ErrApprovalTimeout = "approval_timeout"
	// ErrApprovalUnavailable: approval is required but no front end is attached.
	ErrApprovalUnavailable = "approval_unavailable"
	ErrInternal            = "internal"
	ErrLocked              = "locked"
	// ErrUnlockRequired: the vault is locked and nobody could be asked for
	// the master password (no window, no terminal, no askpass helper).
	ErrUnlockRequired = "unlock_required"
)

// Error is the machine readable failure payload.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Host    string `json:"host,omitempty"`
}

// Approval is a request for a human decision, pushed to attached front ends.
type Approval struct {
	ID       string `json:"id"`
	Op       string `json:"op"`
	Host     string `json:"host"`
	Command  string `json:"command,omitempty"`
	Local    string `json:"local,omitempty"`
	Remote   string `json:"remote,omitempty"`
	Elevated bool   `json:"elevated,omitempty"`
	Reason   string `json:"reason,omitempty"`
	TimeoutS int    `json:"timeout_s,omitempty"`
}

// Event is one message on an attached connection.
type Event struct {
	Type     string    `json:"type"`
	ID       string    `json:"id,omitempty"`
	Approval *Approval `json:"approval,omitempty"`
}

// Decision answers an approval from a front end.
type Decision struct {
	Op    string `json:"op"`
	ID    string `json:"id"`
	Allow bool   `json:"allow"`
}

// HostInfo is the non-secret view of a configured host.
type HostInfo struct {
	Alias   string `json:"alias"`
	Addr    string `json:"addr"`
	User    string `json:"user"`
	Auth    string `json:"auth"`
	HostKey string `json:"host_key,omitempty"`
	Workdir string `json:"workdir,omitempty"`
	// Elevate is the configured privilege mode ("sudo", "su") or empty.
	Elevate string `json:"elevate,omitempty"`
	// Group is the display group, empty when ungrouped.
	Group    string `json:"group,omitempty"`
	Favorite bool   `json:"favorite,omitempty"`
}

// Response is a single daemon -> client reply.
type Response struct {
	OK       bool   `json:"ok"`
	Action   string `json:"action,omitempty"`
	Host     string `json:"host,omitempty"`
	ExitCode *int   `json:"exit_code,omitempty"`

	Stdout      string `json:"stdout,omitempty"`
	Stderr      string `json:"stderr,omitempty"`
	StdoutBytes int64  `json:"stdout_bytes,omitempty"`
	StderrBytes int64  `json:"stderr_bytes,omitempty"`
	Truncated   bool   `json:"truncated,omitempty"`

	Local  string `json:"local,omitempty"`
	Remote string `json:"remote,omitempty"`
	Bytes  int64  `json:"bytes,omitempty"`
	Sha256 string `json:"sha256,omitempty"`

	// Elevated is true when the command ran through sudo/su, Merged when a PTY
	// combined stderr into stdout.
	Elevated    bool   `json:"elevated,omitempty"`
	ElevateMode string `json:"elevate_mode,omitempty"`
	Merged      bool   `json:"merged,omitempty"`
	// Approval is "" when the policy did not ask, else approved, denied,
	// timeout or unavailable.
	Approval string `json:"approval,omitempty"`

	Hosts []HostInfo `json:"hosts,omitempty"`

	DurationMs int64  `json:"duration_ms"`
	Error      *Error `json:"error,omitempty"`
}

// Process exit codes. Stable contract for agents.
const (
	ExitOK       = 0
	ExitUsage    = 1
	ExitConnect  = 2
	ExitRemote   = 3
	ExitTransfer = 4
	ExitBlocked  = 5
	ExitDaemon   = 6
	ExitTimeout  = 124
)

// ExitCodeFor maps a response onto a process exit code.
func (r Response) ExitCodeFor() int {
	if r.Error == nil {
		if r.ExitCode != nil && *r.ExitCode != 0 {
			return ExitRemote
		}
		return ExitOK
	}
	switch r.Error.Code {
	case ErrUsage, ErrConfig, ErrUnknownHost, ErrUnsupported:
		return ExitUsage
	case ErrDaemonDown:
		return ExitDaemon
	case ErrLocked, ErrHostKey, ErrAuth, ErrConnect, ErrUnlockRequired:
		return ExitConnect
	case ErrBlocked, ErrPathDenied, ErrElevDenied,
		ErrApprovalDenied, ErrApprovalUnavailable:
		return ExitBlocked
	case ErrElevation:
		return ExitConnect
	case ErrTransfer:
		return ExitTransfer
	case ErrTimeout, ErrApprovalTimeout:
		return ExitTimeout
	default:
		return ExitUsage
	}
}
