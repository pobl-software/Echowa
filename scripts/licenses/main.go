// Command licenses writes dist/licenses.txt, the licence notices a release
// ships (ADR 0033): Echowa's own licence and NOTICE, then the licence files
// of every Go module linked into the Linux binaries, the Go standard library,
// and the npm packages Vite bundled (dist/npm-licenses.json, written by
// `npm run build`). The release build embeds the file, `echowa licenses`
// prints it, and `make publish` uploads it beside the binaries.
//
//	go run ./scripts/licenses   (make licenses, from the project root)
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// A component is a Go module or an npm package, with the licence files it
// ships. License is what an npm package declares, used when it ships none.
type component struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	License string `json:"license"`
	Files   []file `json:"files"`
}

type file struct {
	Name string `json:"name"`
	Text string `json:"text"`
}

var licenseFile = regexp.MustCompile(`(?i)^(licen[cs]e|copying|notice)([.\-_].*)?$`)

// The release targets (Makefile PLATFORMS). A module can be linked into one
// and not the other.
var platforms = []string{"linux/amd64", "linux/arm64"}

func main() {
	npmList := flag.String("npm", "dist/npm-licenses.json", "the npm packages Vite bundled")
	out := flag.String("o", "dist/licenses.txt", "where to write the notices")
	flag.Parse()

	if err := run(*npmList, *out); err != nil {
		fmt.Fprintln(os.Stderr, "licenses:", err)
		os.Exit(1)
	}
}

func run(npmList, out string) error {
	own, err := ownNotices()
	if err != nil {
		return err
	}
	goComponents, err := goModules()
	if err != nil {
		return err
	}
	npmComponents, err := npmPackages(npmList)
	if err != nil {
		return err
	}

	var b bytes.Buffer
	b.WriteString(own)
	b.WriteString(heading("Third-party software"))
	b.WriteString("Echowa includes the software below. Each licence text is followed by\n" +
		"the Go modules (linked into the echowa binary) and npm packages (bundled\n" +
		"into its web interface) that ship it.\n")
	writeGrouped(&b, append(goComponents, npmComponents...))

	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(out, b.Bytes(), 0o644); err != nil {
		return err
	}
	fmt.Printf("licenses: %s, %d Go modules and %d npm packages, %d KiB\n",
		out, len(goComponents), len(npmComponents), b.Len()/1024)
	return nil
}

func ownNotices() (string, error) {
	license, err := os.ReadFile("LICENSE.md")
	if err != nil {
		return "", err
	}
	notice, err := os.ReadFile("NOTICE")
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(heading("Echowa"), "\n") + string(license) + "\n" + string(notice), nil
}

// goModules lists the modules linked into any release binary, and the
// standard library, with their licence files from the module cache. A
// module without one is an error: its notice can't be shipped.
func goModules() ([]component, error) {
	seen := map[string]component{}
	for _, platform := range platforms {
		goos, goarch, _ := strings.Cut(platform, "/")
		// -e: embed_release.go names dist/licenses.txt, which this writes.
		cmd := exec.Command("go", "list", "-e", "-deps", "-tags", "release",
			"-f", "{{with .Module}}{{if not .Main}}{{.Path}}\t{{.Version}}\t{{.Dir}}{{end}}{{end}}", ".")
		cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
		cmd.Stderr = os.Stderr
		listed, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("go list for %s: %w", platform, err)
		}
		for _, line := range strings.Split(strings.TrimSpace(string(listed)), "\n") {
			fields := strings.Split(line, "\t")
			if len(fields) != 3 || seen[fields[0]].Name != "" {
				continue
			}
			files, err := licenseFiles(fields[2])
			if err != nil {
				return nil, err
			}
			if len(files) == 0 {
				return nil, fmt.Errorf("%s %s has no licence file in %s", fields[0], fields[1], fields[2])
			}
			seen[fields[0]] = component{Name: fields[0], Version: fields[1], Files: files}
		}
	}

	modules := make([]component, 0, len(seen)+1)
	for _, c := range seen {
		modules = append(modules, c)
	}
	sort.Slice(modules, func(i, j int) bool { return modules[i].Name < modules[j].Name })

	std, err := standardLibrary()
	if err != nil {
		return nil, err
	}
	return append([]component{std}, modules...), nil
}

func standardLibrary() (component, error) {
	env, err := exec.Command("go", "env", "GOROOT", "GOVERSION").Output()
	if err != nil {
		return component{}, fmt.Errorf("go env: %w", err)
	}
	goroot, version, _ := strings.Cut(strings.TrimSpace(string(env)), "\n")
	files, err := licenseFiles(goroot)
	if err != nil {
		return component{}, err
	}
	if len(files) == 0 {
		// Homebrew keeps Go's LICENSE beside GOROOT (libexec), not in it.
		files, err = licenseFiles(filepath.Dir(goroot))
		if err != nil {
			return component{}, err
		}
	}
	if len(files) == 0 {
		return component{}, fmt.Errorf("no LICENSE in GOROOT %s", goroot)
	}
	return component{Name: "Go standard library", Version: version, Files: files}, nil
}

func npmPackages(path string) ([]component, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%s is missing: run `npm run build` first", path)
	}
	if err != nil {
		return nil, err
	}
	var packages []component
	if err := json.Unmarshal(data, &packages); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(packages) == 0 {
		return nil, fmt.Errorf("%s lists no packages", path)
	}
	return packages, nil
}

func licenseFiles(dir string) ([]file, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []file
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !licenseFile.MatchString(entry.Name()) {
			continue
		}
		text, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		files = append(files, file{Name: entry.Name(), Text: string(text)})
	}
	return files, nil
}

// writeGrouped writes each distinct licence text once, in the order first
// seen, followed by every component that ships it. Most Apache 2.0 and many
// BSD texts are identical, so this keeps the file small. Packages that
// declare a licence without shipping its text come last.
func writeGrouped(b *bytes.Buffer, components []component) {
	var texts []string
	users := map[string][]string{}
	var declaredOnly []string
	for _, c := range components {
		label := c.Name + " " + c.Version
		if len(c.Files) == 0 {
			declaredOnly = append(declaredOnly, fmt.Sprintf("%s: %s", label, c.License))
			continue
		}
		for _, f := range c.Files {
			text := strings.TrimSpace(strings.ReplaceAll(f.Text, "\r\n", "\n"))
			if _, ok := users[text]; !ok {
				texts = append(texts, text)
			}
			users[text] = append(users[text], label+" ("+f.Name+")")
		}
	}

	for _, text := range texts {
		b.WriteString("\n" + strings.Repeat("-", 80) + "\n\n")
		for _, user := range users[text] {
			b.WriteString(user + "\n")
		}
		b.WriteString("\n" + text + "\n")
	}

	if len(declaredOnly) > 0 {
		b.WriteString("\n" + strings.Repeat("-", 80) + "\n\n")
		b.WriteString("These packages declare a licence but ship no licence file:\n\n")
		for _, line := range declaredOnly {
			b.WriteString(line + "\n")
		}
	}
}

func heading(title string) string {
	return "\n" + strings.Repeat("=", 80) + "\n" + title + "\n" + strings.Repeat("=", 80) + "\n\n"
}
