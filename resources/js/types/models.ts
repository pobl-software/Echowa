// Shapes of the props the Go controllers send. Keep in sync with
// app/models, app/services/status.go and app/http/controllers.

export type DeploymentStatus = 'queued' | 'pulling' | 'deploying' | 'succeeded' | 'failed' | 'rolled_back';
export type DeploymentTrigger = 'manual' | 'webhook' | 'rollback';
export type LiveState = 'running' | 'degraded' | 'down' | 'stopping' | 'stopped' | 'not_deployed' | 'unknown';

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
    // IDs of the apps this one is linked to: its databases (ADR 0028) and
    // services (ADR 0037).
    links: number[];
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
    // How many backup records use it, which Echowa forgets if it's deleted.
    backups: number;
    // How many backup schedules use it. While any do, it can't be deleted.
    schedules: number;
}

// models.BackupStatus and BackupTrigger (ADR 0034).
export type BackupStatus = 'running' | 'succeeded' | 'failed' | 'skipped' | 'interrupted';
export type BackupTrigger = 'schedule' | 'manual' | 'before_restore';

// services.BackupSummary: a row of the Backups section's history.
export interface BackupSummary {
    id: number;
    trigger: BackupTrigger;
    status: BackupStatus;
    location: string;
    // The dump's file name; empty until the backup names it.
    name: string;
    // Uploaded so far while running.
    size_bytes: number;
    server_version: string;
    message: string | null;
    started_at: string;
    finished_at: string | null;
}

// models.Backup, from GET /apps/{app}/backups/{backup}.
export interface BackupDetail extends Omit<BackupSummary, 'location' | 'name'> {
    key: string;
    sha256: string;
    // The end of the dump's stderr.
    output: string;
}

// services.StorageChoice: a location Back up now offers.
export interface StorageChoice {
    id: number;
    name: string;
    bucket: string;
    prefix: string;
}

// services.BackupScheduleSummary: a row of the Backups section's schedules.
export interface BackupSchedule {
    id: number;
    storage_location_id: number;
    location: string;
    // Five-field cron, read in timezone.
    schedule: string;
    timezone: string;
    // How many of its successful backups it keeps.
    keep: number;
    // How many it has now.
    kept: number;
    enabled: boolean;
    // null while it's off.
    next_run_at: string | null;
    last_backup: BackupSummary | null;
}

// services.BackupFailure: a schedule's newest backup that didn't succeed,
// until it's dismissed on Overview.
export interface BackupFailure {
    schedule_id: number;
    schedule: string;
    timezone: string;
    backup: BackupSummary;
    // When a later backup of the schedule succeeded, if one has.
    succeeded_at: string | null;
}

// services.BackupsSection: a template app's Backups section, and its
// Overview's line and alerts.
export interface BackupsSection {
    // The one database a backup covers.
    database: string;
    // The one the app last backed up to comes first.
    locations: StorageChoice[];
    schedules: BackupSchedule[];
    backups: BackupSummary[];
    last_succeeded: BackupSummary | null;
    failures: BackupFailure[];
    // The newest few restores.
    restores: RestoreSummary[];
    // The formats the database downloads in, the first by default.
    exports: ExportFormat[];
}

// templates.Export: a format the database downloads in (ADR 0034).
export interface ExportFormat {
    format: string;
    label: string;
    // What reads it.
    hint: string;
    extension: string;
}

// services.AppWork: what holds an app's lock, or empty (ADR 0034).
export type AppWork = '' | 'deploy' | 'backup' | 'restore' | 'export' | 'stop';

// models.RestoreStatus (ADR 0034).
export type RestoreStatus = 'running' | 'succeeded' | 'failed' | 'interrupted';

// services.RestoreSummary: a row of the Backups section's restores.
export interface RestoreSummary {
    id: number;
    status: RestoreStatus;
    // Empty for an uploaded file.
    location: string;
    uploaded: boolean;
    // The dump's file name.
    name: string;
    // null for a dump Echowa had no record of.
    backup_id: number | null;
    safety_backup_id: number | null;
    // Whether the app was made for this restore. While it waits for the
    // app's first deploy and server, message says which.
    new_app: boolean;
    // The dump's size, and how much of it the restore has read.
    size_bytes: number;
    read_bytes: number;
    message: string | null;
    started_at: string;
    finished_at: string | null;
}

// models.Restore, from GET /apps/{app}/restores/{restore}.
export interface RestoreDetail extends Omit<RestoreSummary, 'location' | 'name' | 'uploaded'> {
    // null for an uploaded file, whose name is the key.
    storage_location_id: number | null;
    key: string;
    output: string;
}

// services.RestorePreview, from GET /apps/{app}/restores/preview: what the
// restore dialog shows before it's confirmed.
export interface RestorePreview {
    name: string;
    // Empty for an uploaded file.
    location: string;
    uploaded: boolean;
    backup_id: number | null;
    // The backup's database and server version.
    database: string;
    server_version: string;
    size_bytes: number;
    // null for an uploaded dump that doesn't say when it was made.
    started_at: string | null;
    // What it restores into. app_version is empty when the app isn't running.
    app_database: string;
    app_version: string;
    template: string;
    // The template's, for a restore into a new app.
    default_tag: string;
    // PostgreSQL's restore applies all of a backup or none of it; MySQL's
    // doesn't.
    atomic: boolean;
    // Why it can't go ahead, and what to know before it does.
    refusal: string;
    warning: string;
}

// services.NewAppPreview, from GET /backups/preview: a backup as the New
// app page shows it before restoring it into a new app.
export interface NewAppPreview {
    name: string;
    location: string;
    // The app the backup was taken from.
    app: string;
    template: string;
    template_name: string;
    database: string;
    server_version: string;
    size_bytes: number;
    started_at: string;
    atomic: boolean;
    default_tag: string;
}

// services.BucketBackup, from GET /apps/{app}/restores/browse: a dump with
// a manifest in the app's folder of a location.
export interface BucketBackup {
    // The manifest's key, which a restore from the bucket names.
    key: string;
    name: string;
    size_bytes: number;
    modified: string;
    // null when Echowa has no record of it.
    backup_id: number | null;
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
    // The app has a Backups section (ADR 0034).
    backups: boolean;
}

// services.LinkedDatabase: a template app a custom app is linked to (ADR
// 0028). Other apps reach it at host:port.
export interface LinkedDatabase {
    id: number;
    name: string;
    template: string;
    host: string;
    port: number;
    // Its connection pooler, while pooling is on (ADR 0035).
    pooler: Endpoint | null;
    status: LiveStatus;
}

// services.Endpoint: a host and port on Echowa's network.
export interface Endpoint {
    host: string;
    port: number;
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

// services.LinkedService: a custom app another custom app is linked to, such
// as a queue worker (ADR 0037).
export interface LinkedService {
    id: number;
    name: string;
    // repository:default_tag
    image: string;
    host: string;
    port: number;
    domain: string | null;
    status: LiveStatus;
}

// services.ServiceChoice: a custom app that can be linked as a service.
export interface ServiceChoice {
    id: number;
    name: string;
    repository: string;
}

// services.AppRef
export interface AppRef {
    id: number;
    name: string;
}

// controllers.ServiceChoices: what the Services tab can add. part_of lists
// the apps this one is a service of; while it isn't empty, it has no
// services of its own.
export interface ServiceChoices {
    existing: ServiceChoice[];
    part_of: AppRef[];
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

// services.PoolerStatus: an app's connection pooler (ADR 0035).
export interface PoolerStatus {
    // Echowa is starting, updating or removing it.
    syncing: boolean;
    // Why the last start, update or removal failed, until one succeeds.
    error: string;
    // The pooler's service as Docker reports it; null while pooling is off.
    service: LiveStatus | null;
}

// controllers.PoolerInfo: the Connection section's pooling, for a
// PostgreSQL app. The pooled URL comes from POST /apps/{id}/connect/reveal.
export interface PoolerInfo {
    connect: ConnectInfo;
    status: PoolerStatus;
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
    // Whether a PostgreSQL app runs its connection pooler (ADR 0035).
    pooling: boolean;
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
