package middleware

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"html/template"
	"io/fs"
	"regexp"
	"strings"
	"sync"

	"github.com/goravel/framework/contracts/http"

	"goravel/app/assets"
	"goravel/app/facades"
)

// SecurityHeaders sets the browser protections on every response. It runs
// globally (routes.GlobalMiddleware) and sets headers before the handler,
// since Inertia writes its body itself.
//
// The Content-Security-Policy allows scripts only from Echowa's origin, plus
// the inline theme script in the root template, by hash. It's left off
// while Vite's dev server runs (public/hot exists), because the dev server
// is another origin and injects inline scripts of its own.
type SecurityHeaders struct{}

func (m *SecurityHeaders) Signature() string {
	return "SecurityHeaders"
}

func (m *SecurityHeaders) Handle(ctx http.Context) {
	response := ctx.Response()
	response.Header("X-Content-Type-Options", "nosniff")
	response.Header("X-Frame-Options", "DENY")
	response.Header("Referrer-Policy", "same-origin")
	response.Header("Cross-Origin-Opener-Policy", "same-origin")
	response.Header("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
	if _, err := fs.Stat(assets.FS(), assets.HotFile); err != nil {
		response.Header("Content-Security-Policy", contentSecurityPolicy())
	}
	if strings.HasPrefix(facades.Config().GetString("http.url"), "https://") {
		response.Header("Strict-Transport-Security", "max-age=31536000")
	}

	ctx.Request().Next()
}

var inlineScript = regexp.MustCompile(`(?s)<script>(.*?)</script>`)

// contentSecurityPolicy is built once, from the root template's inline
// scripts as html/template renders them: it drops JS comments, so the file's
// own bytes hash differently. Inline style attributes stay allowed: React
// and the log viewer position elements with them.
var contentSecurityPolicy = sync.OnceValue(func() string {
	scripts := []string{"'self'"}
	rendered, err := renderRootTemplate()
	if err != nil {
		facades.Log().Error(fmt.Errorf("content security policy: %w", err))
	}
	for _, match := range inlineScript.FindAllSubmatch(rendered, -1) {
		sum := sha256.Sum256(match[1])
		scripts = append(scripts, "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'")
	}

	return strings.Join([]string{
		"default-src 'self'",
		"script-src " + strings.Join(scripts, " "),
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data:",
		"font-src 'self' data:",
		"connect-src 'self'",
		"object-src 'none'",
		"base-uri 'self'",
		"form-action 'self'",
		"frame-ancestors 'none'",
	}, "; ")
})

func renderRootTemplate() ([]byte, error) {
	source, err := fs.ReadFile(assets.FS(), assets.RootTemplate)
	if err != nil {
		return nil, err
	}
	tmpl, err := template.New("app").Parse(string(source))
	if err != nil {
		return nil, err
	}
	var out strings.Builder
	if err := tmpl.Execute(&out, map[string]any{}); err != nil {
		return nil, err
	}

	return []byte(out.String()), nil
}
