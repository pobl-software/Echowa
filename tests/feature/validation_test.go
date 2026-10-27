package feature

import (
	"context"
	"strings"
	"testing"

	contractsvalidation "github.com/goravel/framework/contracts/validation"
	"github.com/stretchr/testify/suite"

	"goravel/app/facades"
	"goravel/app/rules"
	"goravel/app/services"
	"goravel/tests"
)

// ValidationTestSuite runs the form rule maps through Goravel's validator,
// as the controllers will, including the database-backed unique and exists
// checks.
type ValidationTestSuite struct {
	suite.Suite
	tests.TestCase
}

func TestValidationTestSuite(t *testing.T) {
	suite.Run(t, new(ValidationTestSuite))
}

func (s *ValidationTestSuite) SetupTest() {
	s.RefreshDatabase()
}

func (s *ValidationTestSuite) validate(data map[string]any, ruleset map[string]any, options ...contractsvalidation.Option) contractsvalidation.Validator {
	validator, err := facades.Validation().Make(context.Background(), data, ruleset, options...)
	s.Require().NoError(err)

	return validator
}

func validApp() map[string]any {
	return map[string]any{
		"name": "blog", "repository": "ghcr.io/owner/blog", "default_tag": "latest",
		"domain": "blog.example.com", "port": 8080, "replicas": 1, "tls": true,
	}
}

func with(base map[string]any, key string, value any) map[string]any {
	data := map[string]any{}
	for k, v := range base {
		data[k] = v
	}
	data[key] = value

	return data
}

func (s *ValidationTestSuite) TestValidAppPasses() {
	v := s.validate(validApp(), rules.App(false), rules.AppFilters())
	s.False(v.Fails(), "%v", failures(v))

	minimal := map[string]any{"name": "worker", "repository": "nginx", "default_tag": "1.27", "port": 80, "replicas": 2,
		"domain": nil, "registry_id": nil, "healthcheck_cmd": nil}
	v = s.validate(minimal, rules.App(false))
	s.False(v.Fails(), "%v", failures(v))
}

func (s *ValidationTestSuite) TestAppFieldRules() {
	tests := []struct {
		field string
		value any
	}{
		{"name", ""},
		{"name", "My_App"},
		{"name", strings.Repeat("a", 41)},
		{"repository", "ghcr.io/owner/blog:v1"},
		{"repository", ""},
		{"default_tag", "not a tag"},
		{"domain", "https://blog.example.com"},
		{"domain", "*.example.com"},
		{"port", 0},
		{"port", 65536},
		{"port", "eighty"},
		{"replicas", 0},
		{"replicas", 21},
		{"registry_id", 999},
		{"healthcheck_cmd", strings.Repeat("x", 1001)},
	}

	for _, tt := range tests {
		v := s.validate(with(validApp(), tt.field, tt.value), rules.App(false))
		s.True(v.Errors().Has(tt.field), "%s=%v should fail, errors: %v", tt.field, tt.value, failures(v))
	}
}

func (s *ValidationTestSuite) TestAppMessagesComeFromTheCustomRules() {
	v := s.validate(with(validApp(), "name", "My App"), rules.App(false))
	s.Contains(v.Errors().One("name"), "lowercase letters, digits and hyphens")

	v = s.validate(with(validApp(), "domain", "blog.example.com/x"), rules.App(false))
	s.Contains(v.Errors().One("domain"), "domain name")
}

func (s *ValidationTestSuite) TestAppFiltersNormaliseBeforeValidating() {
	data := with(with(validApp(), "name", "  Blog  "), "domain", " Blog.Example.COM ")
	v := s.validate(data, rules.App(false), rules.AppFilters())
	s.False(v.Fails(), "%v", failures(v))

	var in services.AppInput
	s.Require().NoError(v.Bind(&in))
	s.Equal("blog", in.Name)
	s.Equal("blog.example.com", *in.Domain)
}

func (s *ValidationTestSuite) TestAppNameAndDomainAreUnique() {
	domain := "blog.example.com"
	existing, err := services.CreateApp(context.Background(), services.AppInput{
		Name: "blog", Repository: "nginx", DefaultTag: "latest", Domain: &domain, Port: 80, Replicas: 1,
	}, nil)
	s.Require().NoError(err)

	v := s.validate(validApp(), rules.App(false))
	s.True(v.Errors().Has("name"))
	s.True(v.Errors().Has("domain"))

	// Updating the same app keeps its own domain without a clash.
	v = s.validate(validApp(), rules.AppDomain(existing.ID))
	s.False(v.Fails(), "%v", failures(v))

	other, err := services.CreateApp(context.Background(), services.AppInput{
		Name: "shop", Repository: "nginx", DefaultTag: "latest", Port: 80, Replicas: 1,
	}, nil)
	s.Require().NoError(err)
	v = s.validate(validApp(), rules.AppDomain(other.ID))
	s.True(v.Errors().Has("domain"), "another app can't take blog's domain")
}

func (s *ValidationTestSuite) TestAnAppCantTakeTheUIsHost() {
	original := facades.Config().GetString("http.url")
	facades.Config().Add("http.url", "https://echowa.example.com:8192")
	s.T().Cleanup(func() { facades.Config().Add("http.url", original) })

	for _, ruleset := range []map[string]any{rules.App(false), rules.AppDomain(0), rules.AppDomainCheck(0)} {
		v := s.validate(with(validApp(), "domain", " Echowa.Example.com "), ruleset, rules.AppFilters())
		s.Contains(v.Errors().One("domain"), "Echowa's own UI")
	}
	v := s.validate(with(validApp(), "domain", "blog.echowa.example.com"), rules.AppDomain(0), rules.AppFilters())
	s.False(v.Fails(), "%v", failures(v))
}

func (s *ValidationTestSuite) TestRegistryRules() {
	valid := map[string]any{"name": "ghcr", "host": "ghcr.io", "username": "louis", "token": "ghp_x"}
	v := s.validate(valid, rules.Registry(0))
	s.False(v.Fails(), "%v", failures(v))

	v = s.validate(with(valid, "host", "https://ghcr.io"), rules.Registry(0))
	s.True(v.Errors().Has("host"))
	v = s.validate(with(valid, "host", "registry.example.com:5000"), rules.Registry(0))
	s.False(v.Fails(), "%v", failures(v))

	v = s.validate(with(valid, "token", ""), rules.Registry(0))
	s.True(v.Errors().Has("token"), "a token is required when creating")

	registry, err := services.CreateRegistry(context.Background(), services.RegistryInput{Name: "ghcr", Host: "ghcr.io", Username: "louis", Token: "t"})
	s.Require().NoError(err)
	v = s.validate(with(valid, "token", nil), rules.Registry(registry.ID))
	s.False(v.Fails(), "a blank token keeps the stored one on update: %v", failures(v))
	v = s.validate(valid, rules.Registry(0))
	s.True(v.Errors().Has("name"), "registry names are unique")
}

func (s *ValidationTestSuite) TestRegistryIDMustExist() {
	registry, err := services.CreateRegistry(context.Background(), services.RegistryInput{Name: "ghcr", Host: "ghcr.io", Username: "louis", Token: "t"})
	s.Require().NoError(err)

	v := s.validate(with(validApp(), "registry_id", registry.ID), rules.App(false))
	s.False(v.Fails(), "%v", failures(v))
}

func (s *ValidationTestSuite) TestEnvVarAndDeployRules() {
	s.False(s.validate(map[string]any{"key": "DATABASE_URL", "value": "x"}, rules.EnvVar()).Fails())
	s.False(s.validate(map[string]any{"key": "EMPTY", "value": ""}, rules.EnvVar()).Fails())
	s.True(s.validate(map[string]any{"key": "MY-VAR", "value": "x"}, rules.EnvVar()).Errors().Has("key"))
	s.True(s.validate(map[string]any{"key": "1ST", "value": "x"}, rules.EnvVar()).Errors().Has("key"))

	s.False(s.validate(map[string]any{"ref": "v1.2.3"}, rules.Deploy()).Fails())
	s.False(s.validate(map[string]any{"ref": "sha256:" + strings.Repeat("0", 64)}, rules.Deploy()).Fails())
	s.True(s.validate(map[string]any{"ref": "ghcr.io/owner/app:v1"}, rules.Deploy()).Errors().Has("ref"))
	s.True(s.validate(map[string]any{}, rules.Deploy()).Errors().Has("ref"))
}

func validTask() map[string]any {
	return map[string]any{
		"name": "Laravel scheduler", "schedule": "* * * * *", "timezone": "Europe/London",
		"command": "php artisan schedule:run", "timeout_minutes": 15, "enabled": true,
	}
}

func (s *ValidationTestSuite) TestScheduledTaskRules() {
	v := s.validate(validTask(), rules.ScheduledTask(), rules.ScheduledTaskFilters())
	s.False(v.Fails(), "%v", failures(v))
	v = s.validate(with(validTask(), "schedule", "*/15 9-17 * * 1-5"), rules.ScheduledTask())
	s.False(v.Fails(), "%v", failures(v))
	v = s.validate(with(validTask(), "timeout_minutes", 1440), rules.ScheduledTask())
	s.False(v.Fails(), "%v", failures(v))

	for _, tt := range []struct {
		field string
		value any
	}{
		{"name", ""},
		{"name", strings.Repeat("a", 101)},
		{"schedule", ""},
		{"schedule", "@hourly"},
		{"schedule", "* * * *"},
		{"schedule", "0 0 30 2 *"},
		{"timezone", ""},
		{"timezone", "Local"},
		{"timezone", "Mars/Olympus"},
		{"command", ""},
		{"command", strings.Repeat("a", 4097)},
		{"timeout_minutes", 0},
		{"timeout_minutes", 1441},
		{"timeout_minutes", "soon"},
	} {
		v := s.validate(with(validTask(), tt.field, tt.value), rules.ScheduledTask())
		s.True(v.Errors() != nil && v.Errors().Has(tt.field), "%s = %v should fail", tt.field, tt.value)
	}

	v = s.validate(with(validTask(), "timeout_minutes", 0), rules.ScheduledTask(), rules.ScheduledTaskMessages())
	s.Equal("Give it at least a minute.", v.Errors().One("timeout_minutes"))
	v = s.validate(with(validTask(), "schedule", "@hourly"), rules.ScheduledTask())
	s.Contains(v.Errors().One("schedule"), "*/5 * * * *")
}

// failures is safe to format when validation passed and Errors() is nil.
func failures(v contractsvalidation.Validator) any {
	if v.Errors() == nil {
		return nil
	}

	return v.Errors().All()
}
