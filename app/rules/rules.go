package rules

import (
	"context"

	"github.com/goravel/framework/contracts/validation"

	"goravel/app/facades"
)

// All lists the custom rules, registered in bootstrap/rules.go.
func All() []validation.Rule {
	return []validation.Rule{
		&stringRule{"slug", IsSlug, "The :attribute may only contain lowercase letters, digits and hyphens, must start and end with a letter or digit, and be at most 40 characters."},
		&stringRule{"domain", IsDomain, "The :attribute must be a lowercase domain name such as app.example.com, without http:// or a path."},
		&stringRule{"not_ui_host", isNotUIHost, "The :attribute is where Echowa's own UI runs. An app there would be sent your session cookie."},
		&stringRule{"env_key", IsEnvKey, "The :attribute may only contain letters, digits and underscores, and must not start with a digit."},
		&stringRule{"image_repository", IsRepository, "The :attribute must be an image repository without a tag, such as ghcr.io/owner/app."},
		&stringRule{"image_tag", IsTag, "The :attribute must be an image tag such as latest or v1.2.3."},
		&stringRule{"deploy_ref", IsDeployRef, "The :attribute must be an image tag or a sha256: digest."},
		&stringRule{"registry_host", IsRegistryHost, "The :attribute must be a registry host such as ghcr.io, with an optional :port."},
		&stringRule{"cron", IsCron, "The :attribute must be five cron fields, such as */5 * * * *, that run at some point."},
		&stringRule{"iana_timezone", IsTimezone, "The :attribute must be a time zone such as Europe/London or UTC."},
		&stringRule{"storage_endpoint", IsStorageEndpoint, "The :attribute must be an https:// URL with no path, such as https://<account>.r2.cloudflarestorage.com. Plain http:// works only for localhost."},
		&stringRule{"s3_bucket", IsBucket, "The :attribute must be 3 to 63 lowercase letters, digits, dots and hyphens, starting and ending with a letter or digit."},
		&stringRule{"key_prefix", IsKeyPrefix, "The :attribute may only contain letters, digits, slashes and !_.*'()-, up to 200 characters."},
	}
}

func isNotUIHost(domain string) bool {
	return IsOtherHost(domain, facades.Config().GetString("http.url"))
}

// stringRule adapts an Is* check to a Goravel rule. Non-strings fail.
type stringRule struct {
	signature string
	check     func(string) bool
	message   string
}

func (r *stringRule) Signature() string {
	return r.signature
}

func (r *stringRule) Passes(_ context.Context, _ validation.Data, val any, _ ...any) bool {
	s, ok := val.(string)
	return ok && r.check(s)
}

func (r *stringRule) Message(context.Context) string {
	return r.message
}
