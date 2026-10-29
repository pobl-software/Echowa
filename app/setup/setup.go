// Package setup is `echowa setup`: it turns a fresh Ubuntu or Debian server into one
// running Echowa, Traefik included, or upgrades the Echowa on it (ADR 0021). The one-line
// install.sh downloads the binary and runs it. Every step checks before it
// changes anything, so running it again is safe, and is how you upgrade.
//
// The systemd units, the env template, the artisan wrapper and the Traefik
// stack are compiled in (files/), so they always match the binary.
package setup

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	nethttp "net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"time"

	"goravel/app/release"
)

//go:embed files
var files embed.FS

// Where things go on the server (ADR 0020).
const (
	Bin     = "/usr/local/bin/echowa"
	Artisan = "/usr/local/bin/echowa-artisan"
	EnvDir  = "/etc/echowa"
	EnvFile = "/etc/echowa/env"
	DataDir = "/var/lib/echowa"
	// Database is Echowa's SQLite file (DB_DATABASE in files/echowa.env).
	Database = "/var/lib/echowa/echowa.sqlite"
	Backups  = "/var/backups/echowa"
	UnitDir  = "/etc/systemd/system"
	User     = "echowa"
	Network  = "echowa-public"
)

// Options are `echowa setup`'s flags.
type Options struct {
	// HostOnly stops after Docker, the Swarm and the network: what the dev
	// machine needs (deploy/dev-vm.sh).
	HostOnly bool
	// AdvertiseAddr is the address Swarm advertises. Empty: the default
	// route's source address.
	AdvertiseAddr string
	// Access, Domain and TunnelToken say how the UI is reached, on a first
	// install (access.go). Empty Access asks, or picks AccessHTTP.
	Access      Access
	Domain      string
	TunnelToken string
	WithBackups bool
}

// Main runs `echowa setup` with args and returns the exit code.
func Main(args []string, out io.Writer) int {
	opts, err := parse(args, out)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		fmt.Fprintln(out, "echowa setup:", err)
		return 2
	}
	// Ctrl-C reaches the commands running too, which stop in their own
	// way (apt finishes what dpkg is doing); the step they're in then
	// fails and says so, and no later step starts. A second Ctrl-C ends
	// setup there and then.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		stop()
	}()
	err = Run(ctx, opts, out)
	switch {
	case errors.Is(err, errCancelled):
		return 130
	case err != nil && interrupted(ctx, err):
		return 130
	case err != nil:
		return 1
	}

	return 0
}

func parse(args []string, out io.Writer) (Options, error) {
	var opts Options
	fs := flag.NewFlagSet("echowa setup", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.Usage = func() {
		fmt.Fprint(out, `Usage: sudo echowa setup [options]

Installs Docker, a single-node Swarm, Traefik and Echowa as a systemd service
on a fresh Ubuntu or Debian server, or upgrades them. Safe to run again.

On a first install it asks how the UI should be reached (HTTP on port `+UIPort+`,
HTTPS on a domain on port `+UIPort+`, or a Cloudflare Tunnel), unless --access or --tunnel-token
answers. Without a terminal, it picks HTTP.

`)
		fs.PrintDefaults()
	}
	fs.BoolVar(&opts.HostOnly, "host-only", false, "only Docker, the Swarm and the "+Network+" network")
	fs.StringVar(&opts.AdvertiseAddr, "advertise-addr", "", "address the Swarm advertises (default: the default route's)")
	fs.Func("access", "how the UI is reached on a first install: http (port "+UIPort+", the default), https (also "+UIPort+") or tunnel; asked when there's a terminal", func(v string) error {
		opts.Access = Access(v)
		if !opts.Access.valid() {
			return errors.New("must be http, https or tunnel")
		}
		return nil
	})
	fs.StringVar(&opts.Domain, "domain", "", "the UI's domain, for https (required) or tunnel, e.g. echowa.example.com")
	fs.StringVar(&opts.TunnelToken, "tunnel-token", "", "Cloudflare Tunnel token (implies --access tunnel); asked, hidden, when there's a terminal")
	fs.BoolVar(&opts.WithBackups, "with-backups", false, "back the database up daily to "+Backups)
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	if fs.NArg() > 0 {
		return opts, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if opts.TunnelToken != "" {
		if opts.Access != "" && opts.Access != AccessTunnel {
			return opts, errors.New("--tunnel-token is for --access tunnel")
		}
		opts.Access = AccessTunnel
	}
	opts.Domain = strings.ToLower(opts.Domain)

	return opts, nil
}

// Run does the setup, showing each step on out. A failure is shown
// there too, as well as returned.
func Run(ctx context.Context, opts Options, out io.Writer) error {
	s := &steps{ctx: ctx, ui: newUI(out), opts: opts}
	s.ui.banner(release.Version, machine())

	err := s.install(opts)
	switch {
	case errors.Is(err, errCancelled):
		s.ui.cancelled()
	case err != nil:
		s.ui.failed(err, interrupted(ctx, err))
	}

	return err
}

func (s *steps) install(opts Options) error {
	if err := s.preflight(); err != nil {
		return err
	}
	if !opts.HostOnly {
		if err := s.chooseAccess(); err != nil {
			return err
		}
	}
	if err := s.each(s.docker, s.swarm, s.network); err != nil {
		return err
	}
	if opts.HostOnly {
		s.ui.card("Docker, the Swarm and the " + Network + " network are ready")
		return nil
	}

	return s.each(s.traefik, s.traefikRoute, s.user, s.config, s.binary, s.units, s.cloudflared, s.start, s.finish)
}

// interrupted says whether err comes from Ctrl-C or a stop. A command the
// terminal interrupted can fail before the signal reaches setup itself.
func interrupted(ctx context.Context, err error) bool {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return status.Signal() == syscall.SIGINT || status.Signal() == syscall.SIGTERM
		}
	}

	return ctx.Err() != nil
}

// each runs steps in order, and none after an interrupt.
func (s *steps) each(steps ...func() error) error {
	for _, step := range steps {
		if err := s.ctx.Err(); err != nil {
			return errors.New("stopped between steps")
		}
		if err := step(); err != nil {
			return err
		}
	}

	return nil
}

// machine names this server for the banner: host name, OS and CPU.
func machine() string {
	parts := []string{}
	if host, err := os.Hostname(); err == nil {
		parts = append(parts, host)
	}
	if data, err := os.ReadFile("/etc/os-release"); err == nil {
		if name := parseOSRelease(string(data))["PRETTY_NAME"]; name != "" {
			parts = append(parts, name)
		}
	}

	return strings.Join(append(parts, runtime.GOARCH), " · ")
}

type steps struct {
	ctx context.Context
	// ui shows the steps, and commands' output goes through it.
	ui        *ui
	opts      Options
	upgrading bool
	// fresh means there was no env file: a first install.
	fresh bool
	// url is APP_URL, and host and port are where Echowa answers, from
	// the env file.
	url, host, port string
}

// command runs to the end even after Ctrl-C, which the terminal sends
// it as well: killing apt part way could leave dpkg half done. Probes
// like `systemctl is-active` keep giving true answers too.
func (s *steps) command(name string, args ...string) *exec.Cmd {
	return exec.Command(name, args...)
}

// stream runs a command, its output going to the log as it comes.
func (s *steps) stream(env []string, name string, args ...string) error {
	cmd := s.command(name, args...)
	cmd.Stdout, cmd.Stderr = s.ui, s.ui
	cmd.Env = append(os.Environ(), env...)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}

	return nil
}

// quiet runs a command whose output only matters if it fails, like apt's.
func (s *steps) quiet(env []string, name string, args ...string) error {
	var out bytes.Buffer
	cmd := s.command(name, args...)
	cmd.Stdout, cmd.Stderr = &out, &out
	cmd.Env = append(os.Environ(), env...)
	if err := cmd.Run(); err != nil {
		s.ui.Write(out.Bytes())
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}

	return nil
}

// output runs a command and returns its trimmed standard output.
func (s *steps) output(name string, args ...string) (string, error) {
	out, err := s.command(name, args...).Output()

	return strings.TrimSpace(string(out)), err
}

// tested are the releases the installer is checked on (docs/verification.md,
// "Installer"), as os-release's ID and VERSION_ID.
var tested = []string{"ubuntu 24.04", "ubuntu 26.04", "debian 12", "debian 13"}

func (s *steps) preflight() error {
	s.ui.begin("Server")
	if os.Geteuid() != 0 {
		return errors.New("run as root: curl -fsSL <url>/install.sh | sudo bash, or sudo echowa setup")
	}
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		return errors.New("systemd isn't running here, and Echowa runs as a systemd service")
	}
	osRelease, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return err
	}
	info := parseOSRelease(string(osRelease))
	if _, _, ok := dockerRepo(info); !ok {
		return fmt.Errorf("this supports Ubuntu and Debian (found %s)", info["PRETTY_NAME"])
	}
	if !slices.Contains(tested, info["ID"]+" "+info["VERSION_ID"]) {
		s.ui.warn("Echowa is tested on Ubuntu 24.04 and 26.04 and Debian 12 and 13, and this is %s", info["PRETTY_NAME"])
	}
	// Before Docker is even installed, so a server that isn't fresh is
	// refused before anything changes. The Traefik step checks again for
	// servers that already have Docker.
	if _, err := exec.LookPath("docker"); err != nil {
		if err := s.portsFree(); err != nil {
			return err
		}
		s.ui.ok("%s, ports 80 and 443 free", info["PRETTY_NAME"])
		return nil
	}
	s.ui.ok("%s", info["PRETTY_NAME"])

	return nil
}

func (s *steps) units() error {
	s.ui.begin("Services")
	names := []string{"echowa", "echowa-update"}
	for _, unit := range []string{"echowa.service", "echowa-update.path", "echowa-update.service"} {
		if err := writeFile("files/"+unit, filepath.Join(UnitDir, unit), 0o644); err != nil {
			return err
		}
	}
	if s.opts.WithBackups {
		if err := os.MkdirAll(Backups, 0o700); err != nil {
			return err
		}
		if err := s.quiet(nil, "chown", User+":"+User, Backups); err != nil {
			return err
		}
		names = append(names, "echowa-backup")
		for _, unit := range []string{"echowa-backup.service", "echowa-backup.timer"} {
			if err := writeFile("files/"+unit, filepath.Join(UnitDir, unit), 0o644); err != nil {
				return err
			}
		}
	}

	if err := s.quiet(nil, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	s.ui.ok("%s", strings.Join(names, ", "))

	return nil
}

// start starts Echowa. A fresh install migrates first, so a broken database
// shows up here. An upgrade restarts instead: stopping lets running deploys
// finish, and the new binary migrates as it starts, so the old one never
// runs on a newer schema.
func (s *steps) start() error {
	s.ui.begin("Echowa")
	if err := s.quiet(nil, "systemctl", "enable", "--quiet", "echowa"); err != nil {
		return err
	}
	if err := s.quiet(nil, "systemctl", "enable", "--quiet", "--now", "echowa-update.path"); err != nil {
		return err
	}
	if s.upgrading {
		s.ui.doing("restarting. Deploys in progress finish first, which can take minutes")
		if err := s.quiet(nil, "systemctl", "restart", "echowa"); err != nil {
			return err
		}
	} else {
		s.ui.doing("migrating the database")
		if err := s.quiet(nil, Artisan, "--no-ansi", "migrate"); err != nil {
			return err
		}
		s.ui.doing("starting")
		if err := s.quiet(nil, "systemctl", "start", "echowa"); err != nil {
			return err
		}
	}
	if s.opts.WithBackups {
		return s.quiet(nil, "systemctl", "enable", "--quiet", "--now", "echowa-backup.timer")
	}

	return nil
}

// finish waits for Echowa to answer, then says what's next.
func (s *steps) finish() error {
	s.ui.doing("waiting for %s to answer", release.Version)
	if err := s.waitForVersion(release.Version, time.Minute); err != nil {
		logs, _ := s.output("journalctl", "-u", "echowa", "-n", "30", "--no-pager")
		return fmt.Errorf("%w\n\nIts last log lines:\n%s", err, logs)
	}
	s.ui.ok("%s, answering on %s", release.Version, net.JoinHostPort(s.host, s.port))
	if !s.fresh {
		s.ui.card("Echowa "+release.Version+" is running", "\t"+s.url)
		return nil
	}

	// The setup page creates the admin, and only opens from this link.
	page := s.setupPath()
	var before []string
	link := ""
	switch s.opts.Access {
	case AccessHTTPS:
		before = append(before,
			fmt.Sprintf("Point %s at this server with an A record, if it doesn't already. Traefik gets its certificate once the name resolves, through port 80.", s.opts.Domain),
			"Allow TCP port "+UIPort+" in your provider's firewall, from your own addresses if you can. Apps keep 80 and 443.")
		link = "https://" + s.opts.Domain + ":" + UIPort + page
	case AccessTunnel:
		host := s.opts.Domain
		if host == "" {
			host = "<your hostname>"
		}
		before = append(before, fmt.Sprintf("In Cloudflare Zero Trust, route %s to the tunnel, as HTTP to 127.0.0.1:%s, with an Access application in front of it.", host, innerPort))
		if s.opts.Domain == "" {
			before = append(before, "Set APP_URL in "+EnvFile+" to https://<that hostname>, then run: sudo systemctl restart echowa")
		}
		link = "https://" + host + page
	default:
		before = append(before, "Allow TCP port "+UIPort+" in your provider's firewall, and in ufw if you use it.")
		link = "http://" + s.address() + ":" + UIPort + page
	}

	lines := []string{"Open this link to create your admin account:", "\t" + link}
	if page == "" {
		lines = []string{"Echowa couldn't make the setup link. Get it with:", "\tsudo echowa-artisan setup:link"}
	} else if s.opts.Access == AccessHTTP {
		lines = append(lines, "If that address is private, use the server's public one.")
	}
	s.ui.card("Echowa "+release.Version+" is installed", lines...)
	s.ui.list("Before you open it", before)
	s.ui.list("Keep safe", []string{"Back up APP_KEY from " + EnvFile + ", apart from any database backup. Without it, the registry tokens and env values in the database can't be read."})
	if s.opts.Access == AccessHTTP {
		fmt.Fprintln(s.ui)
		s.ui.warn("The UI is plain HTTP, so your password and session cross the network unencrypted.\nFor anything but a trial, reinstall with HTTPS or a Cloudflare Tunnel (deploy/README.md).")
	}
	fmt.Fprintln(s.ui)

	return nil
}

// setupPath is the path of the first-run setup page with its one-time
// token (ADR 0023), or "" if Echowa couldn't make one. The host comes from
// the access choice, which knows the public name better than APP_URL.
func (s *steps) setupPath() string {
	out, err := s.output(Artisan, "--no-ansi", "setup:link")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(out, "\n") {
		if u, err := url.Parse(strings.TrimSpace(line)); err == nil && u.Path == "/setup" && u.Query().Get("token") != "" {
			return u.RequestURI()
		}
	}

	return ""
}

// waitForVersion waits until Echowa's /up, asked from loopback, reports
// version. The old process answers until it has stopped, so answering
// isn't enough.
func (s *steps) waitForVersion(version string, timeout time.Duration) error {
	url := "http://" + net.JoinHostPort(s.host, s.port) + "/up"
	client := &nethttp.Client{Timeout: 2 * time.Second}
	seen := "no answer"
	for deadline := time.Now().Add(timeout); time.Now().Before(deadline); time.Sleep(time.Second) {
		if s.ctx.Err() != nil {
			return errors.New("while waiting for Echowa to answer")
		}
		resp, err := client.Get(url)
		if err != nil {
			continue
		}
		var up struct {
			Version string `json:"version"`
		}
		err = json.NewDecoder(io.LimitReader(resp.Body, 4<<10)).Decode(&up)
		resp.Body.Close()
		switch {
		case err != nil || resp.StatusCode != nethttp.StatusOK:
			seen = resp.Status
		case up.Version == version:
			return nil
		default:
			seen = "version " + up.Version
		}
	}

	return fmt.Errorf("Echowa %s didn't answer on %s within %s (last: %s)", version, url, timeout, seen)
}

// writeFile copies an embedded file to dest with mode, by rename so a
// reader never sees half of it.
func writeFile(embedded, dest string, mode os.FileMode) error {
	data, err := files.ReadFile(embedded)
	if err != nil {
		return err
	}

	return writeAtomic(dest, data, mode)
}

func writeAtomic(dest string, data []byte, mode os.FileMode) error {
	tmp := dest + ".new"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		return err
	}

	return os.Rename(tmp, dest)
}
