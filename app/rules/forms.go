package rules

import (
	"fmt"
	"maps"
	"strconv"
	"strings"

	"github.com/goravel/framework/contracts/validation"
	frameworkvalidation "github.com/goravel/framework/validation"

	"goravel/app/tasks"
)

// Registry returns the rules for the registry form. id is 0 when creating.
// On update a blank token keeps the stored one.
func Registry(id uint) map[string]any {
	token := "required|string|max:4096"
	if id != 0 {
		token = "nullable|string|max:4096"
	}

	return map[string]any{
		"name":     "required|string|max:100|" + unique("registries", "name", id),
		"host":     "required|string|max:255|registry_host",
		"username": "required|string|max:255",
		"token":    token,
	}
}

// App returns the rules for creating an app: its name, Docker config and
// domain. The name can't change after creation, so only this checks it.
func App(hasVolumes bool) map[string]any {
	rules := AppDocker(hasVolumes)
	maps.Copy(rules, AppDomain(0))
	rules["name"] = "required|string|slug|" + unique("apps", "name", 0) + "|not_pooler_name"

	return rules
}

// AppDocker returns the rules for an app's Docker config. An app with
// volumes runs one replica, since a volume has one writer (ADR 0025).
func AppDocker(hasVolumes bool) map[string]any {
	replicas := "required|integer|min:1|max:20"
	if hasVolumes {
		replicas = "required|integer|min:1|max:1"
	}

	return map[string]any{
		"repository":      "required|string|max:255|image_repository",
		"default_tag":     "required|string|image_tag",
		"registry_id":     "nullable|integer|exists:registries,id",
		"port":            "required|integer|min:1|max:65535",
		"replicas":        replicas,
		"healthcheck_cmd": "nullable|string|max:1000",
		"command":         "nullable|string|max:1000|command",
	}
}

// AppService returns the rules for a new service, a custom app made from
// another app's Services section: its name, its Docker config, and whether
// it starts with that app's environment.
func AppService() map[string]any {
	rules := AppDocker(false)
	rules["name"] = "required|string|slug|" + unique("apps", "name", 0) + "|not_pooler_name"
	rules["copy_env"] = "boolean"

	return rules
}

// AppDomain returns the rules for app id's domain. id is 0 when creating.
func AppDomain(id uint) map[string]any {
	return map[string]any{
		"domain": "nullable|string|domain|not_ui_host|" + unique("apps", "domain", id),
		"tls":    "boolean",
	}
}

// AppDomainCheck returns the rules for a domain the Domains tab is about to
// add to app id, before its DNS is looked up.
func AppDomainCheck(id uint) map[string]any {
	return map[string]any{"domain": "required|string|domain|not_ui_host|" + unique("apps", "domain", id)}
}

// ExternalAccess returns the rules for publishing app id's port on the host
// (ADR 0026). Ports below 1024 belong to the system and to Traefik.
func ExternalAccess(id uint) map[string]any {
	return map[string]any{
		"external_port": "required|integer|min:1024|max:65535|" + unique("apps", "external_port", id),
	}
}

// ExternalAccessMessages explains the port limits.
func ExternalAccessMessages() validation.Option {
	return frameworkvalidation.Messages(map[string]string{
		"external_port.min":    "Pick a port from 1024 to 65535. Lower ones are for the system and Traefik.",
		"external_port.unique": "Another app already listens on this port.",
	})
}

// RegistryFilters normalises the registry form before validation.
func RegistryFilters() validation.Option {
	return frameworkvalidation.Filters(map[string]any{
		"name":     "trim",
		"host":     "trim|lower",
		"username": "trim",
	})
}

// AppFilters normalises the app form before validation.
func AppFilters() validation.Option {
	return frameworkvalidation.Filters(map[string]any{
		"name":       "trim|lower",
		"repository": "trim",
		"domain":     "trim|lower",
	})
}

// AppMessages explains the limits that aren't obvious from the form.
func AppMessages() validation.Option {
	return frameworkvalidation.Messages(map[string]string{
		"replicas.max": "An app with a volume runs one replica. Two tasks can't share its data.",
	})
}

// Smallest limits. Below them most apps can't start at all.
const (
	MinCPUMillicores = 100
	MinMemoryMiB     = 64
)

// AppResources returns the rules for an app's CPU, memory and swap limits,
// at most the server's maxCPU millicores, maxMemory MiB and maxSwap MiB.
// Empty uses the default.
func AppResources(maxCPU, maxMemory, maxSwap int) map[string]any {
	return map[string]any{
		"cpu_millicores": fmt.Sprintf("nullable|integer|min:%d|max:%d", MinCPUMillicores, maxCPU),
		"memory_mib":     fmt.Sprintf("nullable|integer|min:%d|max:%d", MinMemoryMiB, maxMemory),
		"swap_mib":       fmt.Sprintf("nullable|integer|min:0|max:%d", maxSwap),
	}
}

// AppResourcesMessages words the limits in cores, as the form takes CPU.
func AppResourcesMessages(maxCPU, maxMemory, maxSwap int) validation.Option {
	swapMax := fmt.Sprintf("This server has %d MiB of swap.", maxSwap)
	if maxSwap == 0 {
		swapMax = "This server has no swap, so there's none to allow."
	}

	cores := strconv.FormatFloat(float64(maxCPU)/1000, 'f', -1, 64)

	return frameworkvalidation.Messages(map[string]string{
		"cpu_millicores.min":     "Give it at least 0.1 of a core.",
		"cpu_millicores.max":     fmt.Sprintf("This server has %s cores.", cores),
		"cpu_millicores.integer": "Use a number of cores, such as 0.5 or 2.",
		"memory_mib.min":         fmt.Sprintf("Give it at least %d MiB.", MinMemoryMiB),
		"memory_mib.max":         fmt.Sprintf("This server has %d MiB.", maxMemory),
		"memory_mib.integer":     "Use a whole number of MiB.",
		"swap_mib.min":           "Use 0 for no swap.",
		"swap_mib.max":           swapMax,
		"swap_mib.integer":       "Use a whole number of MiB.",
	})
}

// AppVolume returns the rules for adding a volume to a custom app (ADR
// 0039): its short name, which makes the Docker volume's, and the path it
// mounts at.
func AppVolume() map[string]any {
	rules := AppVolumeTarget()
	rules["name"] = "required|string|volume_name"

	return rules
}

// AppVolumeTarget returns the rules for moving a volume to another path.
// Its name never changes, since it's the Docker volume's.
func AppVolumeTarget() map[string]any {
	return map[string]any{
		"target": "required|string|mount_path",
	}
}

// AppVolumeFilters normalises the volume form before validation.
func AppVolumeFilters() validation.Option {
	return frameworkvalidation.Filters(map[string]any{
		"name":   "trim",
		"target": "trim",
	})
}

// AppVolumeMessages words the form's errors, which the rules' own messages
// would show beside a field called "target".
func AppVolumeMessages() validation.Option {
	return frameworkvalidation.Messages(map[string]string{
		"name.required":     "Give the volume a name.",
		"name.volume_name":  "Use lowercase letters, digits and underscores, starting with a letter, at most 32 characters.",
		"target.required":   "Enter the path in the container.",
		"target.mount_path": "Use an absolute path such as /app/storage, with no .., no trailing slash and no spaces. Not /, /proc, /sys or /dev.",
	})
}

// MaxNewDatabases is how many databases one request may create.
const MaxNewDatabases = 10

// AppDatabases returns the rules for adding databases to an app: new ones
// from templates, and existing ones by ID (ADR 0028). Wildcard fields
// can't be required: Goravel fails "required" on them when the list is
// empty, so the controller checks for blanks.
func AppDatabases() map[string]any {
	return map[string]any{
		"databases":            "array|max:" + strconv.Itoa(MaxNewDatabases),
		"databases.*.template": "string",
		"databases.*.name":     "string|slug|distinct|" + unique("apps", "name", 0) + "|not_pooler_name",
		"link":                 "array|max:100",
		"link.*":               "integer|distinct",
	}
}

// AppDatabasesFilters normalises the new databases' names.
func AppDatabasesFilters() validation.Option {
	return frameworkvalidation.Filters(map[string]any{
		"databases.*.name": "trim|lower",
	})
}

// AppDatabasesMessages words the name errors without the field, which
// would read as "databases.0.name". Messages match exact fields only, so
// there's a set for each row.
func AppDatabasesMessages() validation.Option {
	messages := map[string]string{}
	for i := range MaxNewDatabases {
		field := "databases." + strconv.Itoa(i) + ".name"
		messages[field+".slug"] = "Use lowercase letters, digits and hyphens, starting and ending with a letter or digit, at most 40 characters."
		messages[field+".distinct"] = "Two new databases can't share a name."
		messages[field+".unique"] = "There's already an app with this name."
	}

	return frameworkvalidation.Messages(messages)
}

// EnvVar returns the rules for one environment variable.
func EnvVar() map[string]any {
	return map[string]any{
		"key":   "required|string|env_key",
		"value": "string|max:65536",
	}
}

// EnvEdits returns the rules for the Environment tab's editor
// (services.EnvEdit rows).
func EnvEdits() map[string]any {
	return map[string]any{
		"vars":                "present|array|max:500",
		"vars.*.key":          "required|string|env_key|distinct",
		"vars.*.original_key": "nullable|string",
		"vars.*.value":        "nullable|string|max:65536",
	}
}

// Deploy returns the rules for a deploy request's ref.
func Deploy() map[string]any {
	return map[string]any{
		"ref": "required|string|deploy_ref",
	}
}

// ScheduledTask returns the rules for a scheduled task's form (ADR 0032).
func ScheduledTask() map[string]any {
	return map[string]any{
		"name":            "required|string|max:100",
		"schedule":        "required|string|cron",
		"timezone":        "required|string|iana_timezone",
		"command":         "required|string|max:4096",
		"timeout_minutes": fmt.Sprintf("required|integer|min:1|max:%d", tasks.MaxTimeoutMinutes),
		"enabled":         "boolean",
	}
}

// ScheduledTaskFilters normalises the scheduled task form before
// validation.
func ScheduledTaskFilters() validation.Option {
	return frameworkvalidation.Filters(map[string]any{
		"name":     "trim",
		"schedule": "trim",
		"timezone": "trim",
		"command":  "trim",
	})
}

// ScheduledTaskMessages words the timeout's limits.
func ScheduledTaskMessages() validation.Option {
	return frameworkvalidation.Messages(map[string]string{
		"timeout_minutes.min":     "Give it at least a minute.",
		"timeout_minutes.max":     fmt.Sprintf("At most %d minutes (24 hours).", tasks.MaxTimeoutMinutes),
		"timeout_minutes.integer": "Use a whole number of minutes.",
	})
}

// MaxBackupKeep is the most successful backups a schedule keeps.
const MaxBackupKeep = 100

// BackupSchedule returns the rules for a backup schedule's form (ADR 0034).
func BackupSchedule() map[string]any {
	return map[string]any{
		"storage_location_id": "required|integer|exists:storage_locations,id",
		"schedule":            "required|string|cron",
		"timezone":            "required|string|iana_timezone",
		"keep":                fmt.Sprintf("required|integer|min:1|max:%d", MaxBackupKeep),
		"enabled":             "boolean",
	}
}

// BackupScheduleFilters normalises the backup schedule form before
// validation.
func BackupScheduleFilters() validation.Option {
	return frameworkvalidation.Filters(map[string]any{
		"schedule": "trim",
		"timezone": "trim",
	})
}

// BackupScheduleMessages words the form's limits.
func BackupScheduleMessages() validation.Option {
	return frameworkvalidation.Messages(map[string]string{
		"storage_location_id.required": "Choose a storage location.",
		"storage_location_id.exists":   "That storage location doesn't exist any more.",
		"keep.min":                     "Keep at least one.",
		"keep.max":                     fmt.Sprintf("At most %d.", MaxBackupKeep),
		"keep.integer":                 "Use a whole number.",
	})
}

// Restore returns the rules for restoring into an app (ADR 0034). The
// source is a backup's record, a location and a manifest's key in it, or
// an upload, whose safety backup goes to safety_location.
func Restore() map[string]any {
	return map[string]any{
		"backup":          "integer",
		"location":        "integer",
		"key":             "string|max:1024",
		"upload":          "string|max:64",
		"safety":          "boolean",
		"safety_location": "integer",
		"confirm":         "required|string",
	}
}

// RestoreUpload returns the rules for starting the upload of a file to
// restore from: its name and size, before any of it is sent.
func RestoreUpload() map[string]any {
	return map[string]any{
		"name": "required|string|max:255",
		"size": "required|integer|min:1",
	}
}

// NewAppRestore returns the rules for restoring a backup into a new app:
// the backup's record, or a location and a manifest's key, and the new
// app's name and tag.
func NewAppRestore() map[string]any {
	return map[string]any{
		"backup":   "integer",
		"location": "integer",
		"key":      "string|max:1024",
		"name":     "required|string|slug|" + unique("apps", "name", 0) + "|not_pooler_name",
		"tag":      "required|string|image_tag",
	}
}

// NewAppRestoreFilters normalises the new app's name and tag.
func NewAppRestoreFilters() validation.Option {
	return frameworkvalidation.Filters(map[string]any{
		"name": "trim|lower",
		"tag":  "trim",
	})
}

// RestoreMessages are the restore form's errors in words.
func RestoreMessages() validation.Option {
	return frameworkvalidation.Messages(map[string]string{
		"confirm.required": "Type the app's name to confirm.",
	})
}

// StorageLocation returns the rules for an S3 storage location's form
// (ADR 0034). id is 0 when creating. On update a blank secret key keeps
// the stored one.
func StorageLocation(id uint) map[string]any {
	rules := StorageLocationCheck(id)
	rules["name"] = "required|string|max:100|" + unique("storage_locations", "name", id)

	return rules
}

// StorageLocationCheck returns the rules for testing a location's form
// before it's saved: everything but its name.
func StorageLocationCheck(id uint) map[string]any {
	secret := "required|string|max:500"
	if id != 0 {
		secret = "nullable|string|max:500"
	}

	return map[string]any{
		"endpoint":      "required|string|max:255|storage_endpoint",
		"region":        "required|string|max:50|alpha_dash",
		"bucket":        "required|string|s3_bucket",
		"prefix":        "nullable|string|key_prefix",
		"access_key_id": "required|string|max:200",
		"secret_key":    secret,
		"path_style":    "boolean",
	}
}

// StorageLocationFilters normalises the storage location form before
// validation.
func StorageLocationFilters() validation.Option {
	return frameworkvalidation.Filters(map[string]any{
		"name":          "trim",
		"endpoint":      "trim",
		"region":        "trim|lower",
		"bucket":        "trim",
		"prefix":        "trim",
		"access_key_id": "trim",
		"secret_key":    "trim",
	})
}

// NotificationProvider returns the rules for a notification provider's
// form (ADR 0040): its name, its kind and the kind's fields. id is 0 when
// creating; a saved provider's kind never changes, so it isn't sent. On
// update a secret sent as null keeps the stored one.
func NotificationProvider(kind string, id uint) map[string]any {
	rules := NotificationProviderCheck(kind, id)
	rules["name"] = "required|string|max:100|" + unique("notification_providers", "name", id)

	return rules
}

// NotificationProviderCheck returns the rules for testing a provider's
// form before it's saved: everything but its name.
func NotificationProviderCheck(kind string, id uint) map[string]any {
	// Goravel's nullable passes an empty string too, so an update that
	// empties a required secret is caught by NotificationProviderInput's
	// Problem instead.
	secret := func(required bool, format string) string {
		rule := "nullable|string|max:2000"
		if required && id == 0 {
			rule = "required|string|max:2000"
		}
		if format != "" {
			rule += "|" + format
		}
		return rule
	}
	rules := map[string]any{}
	if id == 0 {
		rules["kind"] = "required|in:slack,discord,teams,ntfy,smtp,resend"
	}
	switch kind {
	case "slack", "discord", "teams":
		rules["webhook_url"] = secret(true, "service_url")
	case "ntfy":
		rules["server"] = "required|string|max:255|service_url"
		rules["topic"] = secret(true, "ntfy_topic")
		rules["token"] = secret(false, "")
	case "smtp":
		rules["host"] = "required|string|max:253|smtp_host"
		rules["port"] = "required|integer|between:1,65535"
		rules["security"] = "required|in:starttls,tls,none"
		rules["username"] = "nullable|string|max:200"
		rules["password"] = secret(false, "")
		rules["from"] = "required|string|email_address"
		rules["to"] = "required|string|max:2600|email_list"
	case "resend":
		rules["api_key"] = secret(true, "")
		rules["from"] = "required|string|email_address"
		rules["to"] = "required|string|max:2600|email_list"
	}

	return rules
}

// NotificationProviderFilters normalises the provider form before
// validation. Secrets aren't filtered: a filter turns null, which keeps
// the stored secret, into an empty string.
func NotificationProviderFilters() validation.Option {
	return frameworkvalidation.Filters(map[string]any{
		"name":     "trim",
		"server":   "trim",
		"host":     "trim|lower",
		"username": "trim",
		"from":     "trim",
		"to":       "trim",
	})
}

// MaxAlertProviders is how many providers one alert may name.
const MaxAlertProviders = 20

// AlertRule returns the rules for an alert's form (ADR 0040). triggers are
// the ones the page may choose from, and only a new alert sends one: a
// saved alert's trigger never changes.
func AlertRule(triggers []string) map[string]any {
	rules := map[string]any{
		"severity":    "required|in:info,warning,critical",
		"providers":   fmt.Sprintf("required|array|max:%d", MaxAlertProviders),
		"providers.*": "integer|distinct",
	}
	if triggers != nil {
		rules["trigger"] = "required|string|in:" + strings.Join(triggers, ",")
	}

	return rules
}

// AlertSuggestions returns the rules for adding suggested alerts: some of
// triggers, the ones suggested on the page, and where to send them.
func AlertSuggestions(triggers []string) map[string]any {
	return map[string]any{
		"triggers":    "required|array",
		"triggers.*":  "string|distinct|in:" + strings.Join(triggers, ","),
		"providers":   fmt.Sprintf("required|array|max:%d", MaxAlertProviders),
		"providers.*": "integer|distinct",
	}
}

// AlertMessages words the alert forms' errors.
func AlertMessages() validation.Option {
	return frameworkvalidation.Messages(map[string]string{
		"trigger.required":   "Choose what to alert on.",
		"trigger.in":         "Choose one of the listed alerts.",
		"triggers.required":  "Choose at least one alert.",
		"triggers.*.in":      "That alert isn't suggested here any more.",
		"providers.required": "Choose where to send it.",
		"providers.max":      fmt.Sprintf("At most %d providers.", MaxAlertProviders),
		"severity.required":  "Choose a severity.",
	})
}

// unique ignores the row being updated.
func unique(table, column string, id uint) string {
	if id == 0 {
		return "unique:" + table + "," + column
	}

	return "unique:" + table + "," + column + ",id," + strconv.FormatUint(uint64(id), 10)
}
