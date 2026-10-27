// Package docker is Echowa's view of the Docker Engine: pulling images,
// running apps as Swarm services, reading their status and logs, and pruning
// images. Deploy logic depends on the Client interface. Engine implements it
// against the daemon, and Fake implements it in memory for tests.
package docker

import (
	"context"
	"errors"
	"io"
	"iter"
	"time"
)

// Client is everything Echowa needs from Docker.
type Client interface {
	// Check reports whether the daemon is reachable and is a Swarm manager.
	// The error wraps ErrDaemonUnreachable, ErrNotSwarm or ErrNotManager.
	Check(ctx context.Context) error

	// PullImage pulls ref (a tag or a digest) and returns the digest
	// reference it resolved to, e.g. "ghcr.io/owner/app@sha256:...". auth is
	// sent with this request only and never stored in the daemon. Nil means
	// an anonymous pull.
	PullImage(ctx context.Context, ref string, auth *RegistryAuth) (string, error)

	// RegistryLogin checks auth against its registry, through the daemon.
	// Nothing is stored.
	RegistryLogin(ctx context.Context, auth RegistryAuth) error

	// DeployService creates the service, or updates it in place, so it
	// matches spec. Every update rolls the tasks even if nothing else
	// changed, so a redeploy always restarts the app.
	DeployService(ctx context.Context, spec ServiceSpec) error

	// ServiceStatus returns the service's replicas, image and update state.
	// The error wraps ErrServiceNotFound if there's no such service.
	ServiceStatus(ctx context.Context, name string) (ServiceStatus, error)

	// WaitForUpdate blocks until the last DeployService for name has
	// finished: completed, rolled back or paused. It gives up after the
	// configured deploy timeout, or when ctx ends.
	WaitForUpdate(ctx context.Context, name string) (UpdateResult, error)

	// StreamServiceLogs yields the service's log lines, stdout and stderr
	// separated, each with the task that wrote it. With Follow it keeps
	// going until ctx ends or the caller stops iterating, which closes the
	// Docker log stream.
	StreamServiceLogs(ctx context.Context, name string, opts LogOptions) (iter.Seq2[LogLine, error], error)

	// ServiceTasks lists the service's tasks: the running ones, and the
	// stopped ones Swarm keeps (5 per replica by default). The error wraps
	// ErrServiceNotFound if there's no such service.
	ServiceTasks(ctx context.Context, name string) ([]Task, error)

	// RemoveService removes the service. Removing a missing service is not
	// an error.
	RemoveService(ctx context.Context, name string) error

	// RemoveVolume removes a volume Echowa's services created. Removing a
	// missing volume is not an error. The error wraps ErrVolumeInUse while a
	// container, even a stopped task, still uses it.
	RemoveVolume(ctx context.Context, name string) error

	// VolumeExists reports whether a volume with this name exists, whoever
	// made it.
	VolumeExists(ctx context.Context, name string) (bool, error)

	// PruneImages removes unused images from opts.Repositories, keeping
	// recent ones and those listed in opts.Keep.
	PruneImages(ctx context.Context, opts PruneOptions) (PruneReport, error)

	// ContainerStats reads every running container's resource use, once
	// each, without the one-second pre-sample Docker can take. A container
	// that stops between the list and its read is left out.
	ContainerStats(ctx context.Context) ([]ContainerStats, error)

	// Exec runs cmd, without a TTY, in a running task container of the
	// service, and returns its stdout, at most MaxExecOutput bytes. cmd is
	// a constant compiled into Echowa, never input (ADR 0027). The error
	// wraps ErrNotRunning when the service has no running container, or
	// ErrNotManaged, and is an *ExecError when cmd exits with a status
	// other than 0.
	Exec(ctx context.Context, service string, cmd []string) ([]byte, error)

	// ExecTo runs cmd, without a TTY, in the service's newest running task
	// container, writes its stdout and stderr to out as they arrive, and
	// returns its exit code, which isn't an error whatever it is. cmd is a
	// scheduled task's, the admin's own command (ADR 0032). The error wraps
	// ErrNotRunning or ErrNotManaged as Exec's does, or is ctx's when ctx
	// ends first; the command may then still be running in the container.
	ExecTo(ctx context.Context, service string, cmd []string, out io.Writer) (int, error)

	// Host is what the daemon reports about the machine it runs on.
	Host(ctx context.Context) (Host, error)
}

var (
	ErrDaemonUnreachable = errors.New("the Docker daemon is unreachable")
	ErrNotSwarm          = errors.New("the Docker daemon is not in Swarm mode (run `docker swarm init`)")
	ErrNotManager        = errors.New("this Docker node is not a Swarm manager")
	ErrServiceNotFound   = errors.New("service not found")
	// ErrNotManaged protects services and volumes Echowa didn't create, such
	// as Traefik's.
	ErrNotManaged = errors.New("not managed by Echowa")
	ErrTimeout    = errors.New("timed out waiting for the service update")
	// ErrVolumeInUse means a container still uses the volume. Swarm removes
	// a service's tasks after RemoveService returns, so retry.
	ErrVolumeInUse = errors.New("volume is in use")
	ErrNotRunning  = errors.New("no running container")
	// ErrExecOutputTooLarge means a command wrote more than MaxExecOutput.
	ErrExecOutputTooLarge = errors.New("command output is too large")
)

// Labels Echowa puts on every service it creates. It only updates or
// removes services that carry LabelManaged.
const (
	LabelManaged = "echowa.managed"
	LabelApp     = "echowa.app"
)

// RegistryAuth is the login for a private registry.
type RegistryAuth struct {
	ServerAddress string // e.g. "ghcr.io"
	Username      string
	Password      string // a token for GHCR
}

// ServiceSpec is the desired state of one app's Swarm service.
type ServiceSpec struct {
	Name     string            // Swarm service name, e.g. "echowa-blog"
	App      string            // Echowa app name, e.g. "blog"
	Image    string            // pinned digest reference, repo@sha256:...
	Env      map[string]string // environment variables
	Replicas uint64
	// Route makes the service reachable through Traefik. Nil means it
	// isn't routed.
	Route *Route
	// Healthcheck overrides the image's HEALTHCHECK. Nil keeps the image's.
	Healthcheck *Healthcheck
	// Args replace the image's CMD and keep its ENTRYPOINT. They aren't run
	// through a shell. Nil keeps the image's CMD.
	Args []string
	// Volumes keep data across tasks. A service with any runs one replica
	// and updates stop-first, since a volume has one writer.
	Volumes []Volume
	// HostPort publishes a container port on the host's own address, for
	// clients outside the server. Nil publishes nothing.
	HostPort *HostPort
	// Limits cap each task's CPU and memory. Zero leaves a resource
	// uncapped.
	Limits Limits
}

// Limits are the most CPU and memory one task may use. They're limits,
// not reservations: on one node, a start-first update runs two tasks at
// once, and reservations counted twice would leave the new one pending.
type Limits struct {
	NanoCPUs    int64 // 1e9 is one core
	MemoryBytes int64
	// SwapBytes is how much swap a task may use on top of MemoryBytes.
	SwapBytes int64
}

// Host is the machine the daemon runs on.
type Host struct {
	DataRoot    string // the daemon's data directory, where volumes live
	CPUs        int
	MemoryBytes int64
}

// HostPort is a port the host listens on for a container port. It's bound
// in host mode, so clients' addresses reach the app and two tasks can't
// hold it at once: a service with one runs one replica and updates
// stop-first. Docker opens it in iptables ahead of ufw's rules.
type HostPort struct {
	Published int // on the host, e.g. 4040
	Target    int // in the container, e.g. 5432
}

// Volume is a Docker named volume mounted into every task. Swarm creates it
// on first use, with Echowa's labels.
type Volume struct {
	Name   string // e.g. "echowa-db-data"
	Target string // absolute path in the container
}

// Route is how Traefik reaches the service.
type Route struct {
	Domain string // e.g. "blog.example.com"
	Port   int    // the port the app listens on inside the container
	TLS    bool   // HTTPS with a Let's Encrypt certificate, or plain HTTP
}

// Healthcheck runs Command with the container's shell. Zero durations and
// retries use the defaults in healthConfig.
type Healthcheck struct {
	Command     string
	Interval    time.Duration
	Timeout     time.Duration
	StartPeriod time.Duration
	Retries     int
}

// UpdateState is Swarm's state for a service update. Empty means the service
// has never been updated (it was just created).
type UpdateState string

const (
	UpdateNone              UpdateState = ""
	UpdateUpdating          UpdateState = "updating"
	UpdatePaused            UpdateState = "paused"
	UpdateCompleted         UpdateState = "completed"
	UpdateRollbackStarted   UpdateState = "rollback_started"
	UpdateRollbackPaused    UpdateState = "rollback_paused"
	UpdateRollbackCompleted UpdateState = "rollback_completed"
)

// ServiceStatus is a snapshot of a running service.
type ServiceStatus struct {
	Name          string
	Image         string // the image the spec asks for (repo@sha256:...)
	Desired       uint64 // replicas Swarm is aiming for
	Running       uint64 // tasks actually running
	UpdateState   UpdateState
	UpdateMessage string
	// Limits are the ones the service's spec sets now, which tasks from an
	// older spec may not run with yet.
	Limits Limits
	// OOMKilledAt is when the kernel last killed one of the service's
	// tasks for using more memory than its limit, among the stopped tasks
	// Swarm keeps. Nil if none was.
	OOMKilledAt *time.Time
}

// Outcome is how a deploy ended.
type Outcome string

const (
	// OutcomeCompleted: the new spec is running on every replica.
	OutcomeCompleted Outcome = "completed"
	// OutcomeRolledBack: Swarm rejected the update and restored the
	// previous spec.
	OutcomeRolledBack Outcome = "rolled_back"
	// OutcomePaused: the update (or its rollback) failed and Swarm stopped
	// part way. The service needs attention.
	OutcomePaused Outcome = "paused"
)

// UpdateResult is what WaitForUpdate saw at the end.
type UpdateResult struct {
	Outcome Outcome
	Message string // Swarm's explanation, e.g. why it rolled back
}

// LogOptions selects which log lines to stream.
type LogOptions struct {
	Since  time.Time // zero: from the start
	Tail   int       // last N lines per task; 0 means all
	Follow bool      // keep streaming new lines
}

// Stream is where a log line was written.
type Stream string

const (
	Stdout Stream = "stdout"
	Stderr Stream = "stderr"
)

// LogLine is one line of service output.
type LogLine struct {
	Time   time.Time
	Stream Stream
	Text   string // without the trailing newline
	Task   string // the task that wrote it; empty if Docker didn't say
}

// Task is one of a service's containers, as Swarm schedules it.
type Task struct {
	ID string
	// Generation is the service's ForceUpdate when Swarm made the task.
	// Every deploy bumps it (ADR 0015), so tasks that share one came from
	// the same deploy, restarts included. A Swarm rollback goes back to the
	// previous deploy's.
	Generation uint64
	Image      string
	CreatedAt  time.Time
	// Running is whether Swarm wants the task running: false once a newer
	// task replaced it or it was shut down.
	Running bool
}

// PruneOptions limits pruning to images Echowa deployed.
type PruneOptions struct {
	// Repositories whose images may be removed, e.g. "ghcr.io/owner/app".
	// Images from any other repository are never touched.
	Repositories []string
	// OlderThan protects images created more recently than this.
	OlderThan time.Duration
	// Keep lists digest references (repo@sha256:...) that must stay, such
	// as the images of each app's recent successful deployments.
	Keep []string
}

// PruneReport lists what was removed.
type PruneReport struct {
	Removed        []string // image references or IDs
	SpaceReclaimed int64    // bytes
}

// ContainerStats is one running container's resource use. CPU, network and
// block I/O are cumulative since the container started; callers turn them
// into rates between two reads.
type ContainerStats struct {
	ID   string
	Name string // without Docker's leading slash
	// App is the echowa.app label Echowa puts on every task of an app.
	// Empty for containers Echowa doesn't manage.
	App  string
	Read time.Time // when Docker read the counters

	CPUNanos    uint64 // CPU time used, all cores
	MemoryUsed  uint64 // without the inactive file cache, as `docker stats` shows
	MemoryLimit uint64 // the host's memory when the container has no limit

	NetRxBytes      uint64 // summed over the container's interfaces
	NetTxBytes      uint64
	BlockReadBytes  uint64
	BlockWriteBytes uint64
}
