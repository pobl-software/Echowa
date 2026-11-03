package services

import (
	"strings"

	"goravel/app/facades"
	"goravel/app/models"
)

// HookURL is where CI sends the app's deploys: ECHOWA_HOOKS_URL, which
// defaults to APP_URL.
func HookURL(app *models.App) string {
	return strings.TrimRight(facades.Config().GetString("echowa.hooks_url"), "/") + "/hooks/deploy/" + app.Name
}

// HookSnippet is a CI step that deploys the app, for the CI section's
// tabs.
type HookSnippet struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Code  string `json:"code"`
}

// HookSnippets are the app's CI steps. Each signs and sends the same
// request, with the secret from the CI variable ECHOWA_SECRET and the ref
// from REF: a tag, a sha256: digest, or empty for the app's default tag.
// They need only date, openssl and curl.
func HookSnippets(app *models.App) []HookSnippet {
	call := hookCall(app)

	return []HookSnippet{
		{ID: "github", Label: "GitHub Actions", Code: `# After docker/build-push-action with id: build. Add the secret as a
# repository secret named ECHOWA_SECRET.
- name: Deploy to Echowa
  env:
    ECHOWA_SECRET: ${{ secrets.ECHOWA_SECRET }}
    REF: ${{ steps.build.outputs.digest }}
  run: |
` + indent(call, "    ")},
		{ID: "gitlab", Label: "GitLab CI", Code: `# After the job that pushes the image tagged with the commit. Add the
# secret as a masked CI/CD variable named ECHOWA_SECRET.
deploy:
  stage: deploy
  image: alpine:3
  variables:
    REF: $CI_COMMIT_SHORT_SHA
  script:
    - apk add --no-cache curl openssl
    - |
` + indent(call, "      ")},
		{ID: "shell", Label: "Any shell", Code: `# ECHOWA_SECRET holds the secret. REF is a tag or sha256: digest;
# empty deploys ` + app.DefaultTag + `.
REF=` + app.DefaultTag + `
` + call},
	}
}

// hookCall signs and sends the deploy request. The app's name and the URL
// are written in: names are slugs, and the URL is the admin's setting.
//
// curl fails the step on any answer but a 2xx. --fail-with-body prints a
// refusal's error, where -f prints only the status. -L with --max-redirs
// 0 makes a redirect an error (exit 47): -f alone passes one, so a URL
// behind Cloudflare Access, which redirects to its login, would go green
// with nothing deployed.
func hookCall(app *models.App) string {
	return `ts=$(date +%s)
body="{\"ref\":\"$REF\"}"
sig=$(printf '%s.%s.%s' "$ts" "` + app.Name + `" "$body" | openssl dgst -sha256 -hmac "$ECHOWA_SECRET" -r | cut -d' ' -f1)
curl -sS --fail-with-body -L --max-redirs 0 --retry 3 -X POST "` + HookURL(app) + `" \
  -H 'Content-Type: application/json' \
  -H "X-Echowa-Timestamp: $ts" \
  -H "X-Echowa-Signature: sha256=$sig" \
  --data "$body"
`
}

// HookPostman is the same request as a Postman one, for trying the
// webhook by hand: the URL, the headers, the body and a pre-request
// script that fills in the timestamp and signature.
type HookPostman struct {
	URL     string       `json:"url"`
	Headers []HookHeader `json:"headers"`
	Body    string       `json:"body"`
	Script  string       `json:"script"`
}

type HookHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// HookPostmanRequest's script signs pm.request.body.raw, the body as
// typed, so the body must hold no variables: Postman resolves them only
// when it sends. The secret is the Postman variable ECHOWA_SECRET.
func HookPostmanRequest(app *models.App) HookPostman {
	return HookPostman{
		URL: HookURL(app),
		Headers: []HookHeader{
			{Name: "Content-Type", Value: "application/json"},
			{Name: "X-Echowa-Timestamp", Value: "{{timestamp}}"},
			{Name: "X-Echowa-Signature", Value: "sha256={{signature}}"},
		},
		Body: `{"ref":"` + app.DefaultTag + `"}`,
		Script: `const CryptoJS = require('crypto-js');
const timestamp = Math.floor(Date.now() / 1000).toString();
const signed = timestamp + '.` + app.Name + `.' + pm.request.body.raw;
const secret = pm.variables.get('ECHOWA_SECRET');
pm.variables.set('timestamp', timestamp);
pm.variables.set('signature', CryptoJS.HmacSHA256(signed, secret).toString());
`,
	}
}

func indent(text, prefix string) string {
	lines := strings.SplitAfter(strings.TrimSuffix(text, "\n"), "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}

	return strings.Join(lines, "") + "\n"
}
