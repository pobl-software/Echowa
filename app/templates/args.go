package templates

import "strings"

// Options are the features that change how a template's server starts.
type Options struct {
	// TLS serves the app's certificate, while external access is on (ADR
	// 0026).
	TLS bool
	// SlowQueries collects what the slow query list reads, for a template
	// that needs a flag for it (ADR 0027).
	SlowQueries bool
}

// Args start the template's server with opts on, each adding its flags to
// Server (ADR 0027). Nil keeps the image's CMD.
func (t Template) Args(opts Options) []string {
	command := []string{t.Server}
	setup := ""
	if opts.TLS && t.TLS != nil {
		command = append(command, t.TLS.Flags)
		setup = tlsSetup(t.TLS.Owner)
	}
	if opts.SlowQueries && t.Inspect != nil && t.Inspect.SlowQueryFlags != "" {
		command = append(command, t.Inspect.SlowQueryFlags)
	}
	if len(command) == 1 && !t.ReplaceCMD {
		return nil
	}

	return []string{"sh", "-c", setup + "exec " + strings.Join(command, " ")}
}
