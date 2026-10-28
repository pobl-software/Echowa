// Shapes of the props the Go controllers send. Keep in sync with
// app/models, app/services/status.go and app/http/controllers.

export type DeploymentStatus = 'queued' | 'pulling' | 'deploying' | 'succeeded' | 'failed' | 'rolled_back';
export type DeploymentTrigger = 'manual' | 'webhook' | 'rollback';
export type LiveState = 'running' | 'degraded' | 'down' | 'not_deployed' | 'unknown';

// services.LiveStatus
export interface LiveStatus {
    state: LiveState;
    running: number;
    desired: number;
    update: string;
    digest: string;
    // Set once the service has been short of replicas for over 2 minutes
    // (RFC 3339, with a zone).
    unhealthy_since: string | null;
    // What the service runs with; 0 is uncapped.
    limits: ResourceLimits;
    // When a task was last killed for going over its memory limit.
    oom_killed_at: string | null;
}

// services.ResourceLimits: CPU in millicores (1000 to a core), memory in
// MiB, for each task.
export interface ResourceLimits {
    cpu_millicores: number;
    memory_mib: number;
    // Swap a task may use past its memory limit.
    swap_mib: number;
}

// controllers.ResourcesProps: the defaults (half the server) and the
// whole server.
export interface ResourcesInfo {
    defaults: ResourceLimits;
    host: ResourceLimits;
    // The server's swap; null before the first metrics reading.
    host_swap_mib: number | null;
}

// services.DeploymentSummary
export interface DeploymentSummary {
    id: number;
    status: DeploymentStatus;
    trigger: DeploymentTrigger;
    ref: string;
    digest: string | null;
    created_at: string | null;
    started_at: string | null;
    finished_at: string | null;
}

// models.Deployment, with its log.
export interface Deployment extends DeploymentSummary {
    app_id: number;
    log: string;
    error: string | null;
}

export const runningStatuses: DeploymentStatus[] = ['queued', 'pulling', 'deploying'];

// services.AppSummary
export interface AppSummary {
    id: number;
    name: string;
    repository: string;
    domain: string | null;
    tls: boolean;
    // The template's slug (ADR 0025); null for a custom app.
    template: string | null;
    status: LiveStatus;
    last_deployment: DeploymentSummary | null;
    // IDs of the apps this one is linked to (ADR 0028).
    databases: number[];
}

// models.Registry. The token is never sent.
export interface Registry {
    id: number;
    name: string;
    host: string;
    username: string;
}

// controllers.RegistryRow
export interface RegistryRow extends Registry {
    apps_count: number;
}

// models.StorageLocation. The secret key never leaves the server.
export interface StorageLocation {
    id: number;
    name: string;
    endpoint: string;
    region: string;
    bucket: string;
    prefix: string;
    access_key_id: string;
    path_style: boolean;
}

// backups.Step: one stage of a storage location's Test. error is empty
// when it passed.
export interface StorageCheckStep {
    name: string;
    error: string;
}

// controllers.RegistryOption
export interface RegistryOption {
    id: number;
    name: string;
    host: string;
}

// controllers.TemplateOption: a template on the New app page (ADR 0025).
// Env vars come as keys and where their values come from, never values.
export interface TemplateOption {
    slug: string;
    name: string;
    description: string;
    repository: string;
    default_tag: string;
    port: number;
    // False: no domain; other apps reach it at echowa-<name>:<port>.
    http: boolean;
    healthcheck_cmd: string;
    env: { key: string; source: 'fixed' | 'app_name' | 'generated' }[];
    // Container paths, each a named volume.
    volumes: string[];
    // How to connect with TLS while external access is on (ADR 0026); empty
    // when the template doesn't serve TLS.
    tls_client: string;
    // 'switch' when it lists its slow queries once they're switched on,
    // 'always' when it lists them anyway, empty when it can't (ADR 0027).
    slow_queries: '' | 'switch' | 'always';
}

// services.LinkedDatabase: a template app a custom app is linked to (ADR
// 0028). Other apps reach it at host:port.
export interface LinkedDatabase {
    id: number;
    name: string;
    template: string;
    host: string;
    port: number;
    status: LiveStatus;
}

// services.DatabaseChoice: a template app that isn't linked yet.
export interface DatabaseChoice {
    id: number;
    name: string;
    template: string;
}

// controllers.DatabaseChoices: what the Databases tab can add.
export interface DatabaseChoices {
    templates: TemplateOption[];
    existing: DatabaseChoice[];
}

// services.SlowQueryResult: an app's slow queries, or why they couldn't be
// read.
export interface SlowQueryResult {
    report: SlowQueryReport | null;
    problem: string;
}

// templates.SlowQueryReport: the queries that took the most time in total.
export interface SlowQueryReport {
    // When the totals start; null when the database doesn't say.
    since: string | null;
    // How many queries were forgotten to make room for others.
    dropped: number | null;
    queries: SlowQuery[];
}

export interface SlowQuery {
    // Values replaced by placeholders, or only a command's name.
    query: string;
    calls: number;
    total_ms: number;
    mean_ms: number;
    // Null when the database doesn't count them.
    max_ms: number | null;
    rows: number | null;
}

// controllers.ConnectInfo: how other apps reach a template app. preview has
// env keys where the values go; the real URL and the password come from
// POST /apps/{id}/connect/reveal.
export interface ConnectInfo {
    host: string;
    port: number;
    user: string;
    preview: string;
}

// models.App. The webhook secret is never sent.
export interface App {
    id: number;
    name: string;
    repository: string;
    default_tag: string;
    registry_id: number | null;
    registry?: Registry | null;
    domain: string | null;
    port: number;
    replicas: number;
    healthcheck_cmd: string | null;
    tls: boolean;
    // The template's slug (ADR 0025); null for a custom app.
    template: string | null;
    // Container arguments; null keeps the image's CMD.
    args: string[] | null;
    // Only when the controller loads them.
    volumes?: AppVolume[];
    // The host port a template database is published on (ADR 0026); null
    // when only apps on the server can reach it.
    external_port: number | null;
    // Whether a template database that needs a switch for it collects its
    // slow queries (ADR 0027).
    slow_queries: boolean;
    // Each task's limits; null uses the default.
    cpu_millicores: number | null;
    memory_mib: number | null;
    // Swap each task may use past its memory limit; 0 is none.
    swap_mib: number;
    current_deployment_id: number | null;
    created_at: string;
    updated_at: string;
}

// services.DNSCheck: a domain's A and AAAA records against this server's
// public addresses.
export interface DNSCheck {
    state: 'points_here' | 'elsewhere' | 'missing' | 'local' | 'unknown';
    records: { address: string; here: boolean }[];
    server: string[];
    error?: string;
}

// services.CertificateCheck: what Traefik serves for a domain over HTTPS.
// Times are RFC 3339, with a zone.
export interface CertificateCheck {
    state: 'off' | 'active' | 'renewal_overdue' | 'pending' | 'expired' | 'invalid' | 'unreachable';
    issuer?: string;
    not_before?: string;
    not_after?: string;
    renews_at?: string;
    error?: string;
}

// services.DomainStatus, the Domains section's checks of the saved domain.
export interface DomainStatus {
    domain: string;
    dns: DNSCheck;
    certificate: CertificateCheck;
    checked_at: string;
}

// services.LogDeploy: a deploy whose containers Swarm still keeps, which
// the Logs section can show the lines of.
export interface LogDeploy {
    generation: number;
    current: boolean;
    // When its oldest kept container was made.
    started_at: string;
    // The deployment that made it, when one matches.
    deployment: { id: number; ref: string; started_at: string } | null;
}

// models.AppVolume: a Docker named volume mounted at target.
export interface AppVolume {
    id: number;
    app_id: number;
    name: string;
    target: string;
}

// services.PruneRun. Times are RFC 3339, with a zone.
export interface PruneRun {
    trigger: 'schedule' | 'manual';
    started_at: string;
    finished_at: string | null;
    kept: string[];
    removed: string[];
    space_reclaimed: number;
    error: string;
}

// controllers.SettingsController.Index
export interface PruneSettings {
    keep: number;
    min_age: string;
    running: PruneRun | null;
    last: PruneRun | null;
}

// update.Status: the last update the root updater ran (ADR 0021).
export interface UpdateStatus {
    state: 'running' | 'succeeded' | 'rolled_back' | 'failed';
    from: string;
    to: string;
    message: string;
    log: string[];
    started_at: string;
    finished_at: string | null;
}

// controllers.updateProps
export interface UpdateSettings {
    current: string;
    // Dev builds never check for or install updates.
    dev: boolean;
    check: {
        checking: boolean;
        checked_at: string | null;
        latest: { version: string; published_at: string; notes: string } | null;
        error: string;
    };
    // The newest release is newer than the running one.
    available: boolean;
    // A version waiting for the updater to pick it up, or "".
    requested: string;
    // Requested or running: no other update may start.
    busy: boolean;
    status: UpdateStatus | null;
}

// A scheduled task's runs (ADR 0032, models.ScheduledTaskRun).
export type RunStatus = 'running' | 'succeeded' | 'failed' | 'timed_out' | 'skipped' | 'interrupted';
export type RunTrigger = 'schedule' | 'manual';

// A run as lists show it, without its output (services.TaskRun).
export interface TaskRun {
    id: number;
    trigger: RunTrigger;
    status: RunStatus;
    exit_code: number | null;
    // Echowa's own note, such as why the run was skipped.
    message: string | null;
    started_at: string | null;
    finished_at: string | null;
}

// One run with its output, from GET /apps/{app}/tasks/{task}/runs/{run}.
export interface TaskRunDetail extends TaskRun {
    scheduled_task_id: number;
    // The last 64 KiB of stdout and stderr, as written.
    output: string;
}

// A custom app's scheduled task (services.ScheduledTaskSummary).
export interface ScheduledTask {
    id: number;
    app_id: number;
    name: string;
    // Five cron fields; presets are expressions too.
    schedule: string;
    timezone: string;
    command: string;
    timeout_minutes: number;
    enabled: boolean;
    dismissed_run_id: number | null;
    // RFC 3339; null while the task is disabled.
    next_run_at: string | null;
    last_run: TaskRun | null;
}

// A task's newest failed or timed-out run that hasn't been dismissed, for
// the Overview (services.TaskFailure).
export interface TaskFailure {
    task_id: number;
    task_name: string;
    run: TaskRun;
    // The last few lines of the run's output.
    tail: string;
    // When a later run succeeded, if one has.
    succeeded_at: string | null;
}
