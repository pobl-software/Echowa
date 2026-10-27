package inertia

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"strings"

	"goravel/app/assets"
)

const buildURL = "/build/"

type manifestChunk struct {
	File    string   `json:"file"`
	CSS     []string `json:"css"`
	Imports []string `json:"imports"`
}

// viteTags is the Go version of Blade's @viteReactRefresh + @vite directives.
// It checks for the hot file on every request, so it doesn't matter whether
// Vite or the Go server was started first.
func viteTags(entries ...string) (template.HTML, error) {
	if devServer, ok := hotURL(); ok {
		tags := []string{
			fmt.Sprintf(`<script type="module">
    import RefreshRuntime from "%s/@react-refresh"
    RefreshRuntime.injectIntoGlobalHook(window)
    window.$RefreshReg$ = () => {}
    window.$RefreshSig$ = () => (type) => type
    window.__vite_plugin_react_preamble_installed__ = true
</script>`, devServer),
			fmt.Sprintf(`<script type="module" src="%s/@vite/client"></script>`, devServer),
		}
		for _, entry := range entries {
			tags = append(tags, fmt.Sprintf(`<script type="module" src="%s/%s"></script>`, devServer, entry))
		}

		return template.HTML(strings.Join(tags, "\n")), nil
	}

	// laravel-vite-plugin writes public/hot while `npm run dev` runs, and
	// the manifest on `npm run build`. A release build has only the latter.
	content, err := fs.ReadFile(assets.FS(), assets.Manifest)
	if err != nil {
		return "", fmt.Errorf("vite manifest not found, run `npm run dev` or `npm run build`: %w", err)
	}
	var manifest map[string]manifestChunk
	if err := json.Unmarshal(content, &manifest); err != nil {
		return "", fmt.Errorf("parse vite manifest: %w", err)
	}

	var tags []string
	for _, entry := range entries {
		chunk, ok := manifest[entry]
		if !ok {
			return "", fmt.Errorf("%q is not in the vite manifest", entry)
		}
		for _, css := range collectCSS(manifest, entry, map[string]bool{}) {
			tags = append(tags, fmt.Sprintf(`<link rel="stylesheet" href="%s%s">`, buildURL, css))
		}
		tags = append(tags, fmt.Sprintf(`<script type="module" src="%s%s"></script>`, buildURL, chunk.File))
	}

	return template.HTML(strings.Join(tags, "\n")), nil
}

func hotURL() (string, bool) {
	content, err := fs.ReadFile(assets.FS(), assets.HotFile)
	if err != nil {
		return "", false
	}

	return strings.TrimRight(strings.TrimSpace(string(content)), "/"), true
}

// collectCSS returns the stylesheets for a chunk and everything it imports.
func collectCSS(manifest map[string]manifestChunk, key string, seen map[string]bool) []string {
	if seen[key] {
		return nil
	}
	seen[key] = true

	chunk := manifest[key]
	css := append([]string{}, chunk.CSS...)
	for _, imported := range chunk.Imports {
		css = append(css, collectCSS(manifest, imported, seen)...)
	}

	return css
}
