package setup

import (
	"fmt"
	"io"
	nethttp "net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// docker installs Docker Engine from Docker's apt repository, unless it's
// already installed, and makes sure it's running.
func (s *steps) docker() error {
	s.ui.begin("Docker Engine")
	fresh := !s.installed("docker-ce")
	if fresh {
		if err := s.installDocker(); err != nil {
			return err
		}
	}

	for _, unit := range []string{"containerd.service", "docker.service"} {
		if err := s.command("systemctl", "is-active", "--quiet", unit).Run(); err != nil {
			s.ui.doing("starting %s", unit)
			if err := s.quiet(nil, "systemctl", "enable", "--now", unit); err != nil {
				return err
			}
		}
	}
	version, err := s.output("docker", "version", "--format", "{{.Server.Version}}")
	if err != nil {
		return fmt.Errorf("the Docker daemon isn't responding (journalctl -u docker): %w", err)
	}
	if fresh {
		s.ui.ok("%s, installed from Docker's apt repository", version)
	} else {
		s.ui.ok("%s", version)
	}

	return nil
}

func (s *steps) installed(pkg string) bool {
	status, err := s.output("dpkg-query", "-W", "-f=${Status}", pkg)

	return err == nil && strings.Contains(status, "install ok installed")
}

func (s *steps) installDocker() error {
	apt := []string{"DEBIAN_FRONTEND=noninteractive"}
	var conflicting []string
	for _, pkg := range []string{"docker.io", "docker-doc", "docker-compose", "docker-compose-v2", "podman-docker", "containerd", "runc"} {
		if s.installed(pkg) {
			conflicting = append(conflicting, pkg)
		}
	}
	if len(conflicting) > 0 {
		s.ui.say("Removing Ubuntu's Docker packages, which conflict with Docker's: %s", strings.Join(conflicting, " "))
		if err := s.quiet(apt, "apt-get", append([]string{"remove", "-y", "-qq"}, conflicting...)...); err != nil {
			return err
		}
	}

	s.ui.doing("adding Docker's apt repository")
	if err := s.quiet(apt, "apt-get", "update", "-qq"); err != nil {
		return err
	}
	if err := s.quiet(apt, "apt-get", "install", "-y", "-qq", "ca-certificates"); err != nil {
		return err
	}
	if err := os.MkdirAll("/etc/apt/keyrings", 0o755); err != nil {
		return err
	}
	key, err := download("https://download.docker.com/linux/ubuntu/gpg")
	if err != nil {
		return fmt.Errorf("Docker's apt key: %w", err)
	}
	if err := writeAtomic("/etc/apt/keyrings/docker.asc", key, 0o644); err != nil {
		return err
	}
	osRelease, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return err
	}
	if err := writeAtomic("/etc/apt/sources.list.d/docker.sources", []byte(dockerSources(parseOSRelease(string(osRelease)))), 0o644); err != nil {
		return err
	}

	s.ui.doing("installing")
	if err := s.quiet(apt, "apt-get", "update", "-qq"); err != nil {
		return err
	}

	return s.quiet(apt, "apt-get", "install", "-y", "-qq",
		"docker-ce", "docker-ce-cli", "containerd.io", "docker-buildx-plugin", "docker-compose-plugin")
}

func dockerSources(osRelease map[string]string) string {
	suite := osRelease["UBUNTU_CODENAME"]
	if suite == "" {
		suite = osRelease["VERSION_CODENAME"]
	}

	return "Types: deb\nURIs: https://download.docker.com/linux/ubuntu\nSuites: " + suite +
		"\nComponents: stable\nSigned-By: /etc/apt/keyrings/docker.asc\n"
}

func download(url string) ([]byte, error) {
	client := &nethttp.Client{Timeout: time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != nethttp.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}

	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

// swarm makes this node a single-node Swarm manager.
func (s *steps) swarm() error {
	s.ui.begin("Swarm")
	state, err := s.output("docker", "info", "--format", "{{.Swarm.LocalNodeState}}")
	if err != nil {
		return err
	}
	switch state {
	case "active":
	case "inactive":
		addr := s.opts.AdvertiseAddr
		if addr == "" {
			route, _ := s.output("ip", "-4", "route", "get", "1.1.1.1")
			addr = routeSource(route)
		}
		if addr == "" {
			return fmt.Errorf("couldn't find this server's address; pass --advertise-addr")
		}
		s.ui.doing("initialising, advertising %s", addr)
		if _, err := s.output("docker", "swarm", "init", "--advertise-addr", addr); err != nil {
			return fmt.Errorf("docker swarm init: %w", err)
		}
	default:
		return fmt.Errorf("the Swarm is in state %q; fix it by hand (docker swarm leave --force), then run this again", state)
	}

	manager, err := s.output("docker", "info", "--format", "{{.Swarm.ControlAvailable}}")
	if err != nil || manager != "true" {
		return fmt.Errorf("this node is in a Swarm but isn't a manager; run Echowa on a manager node")
	}
	addr, _ := s.output("docker", "info", "--format", "{{.Swarm.NodeAddr}}")
	s.ui.ok("single node, on %s", addr)

	return nil
}

// routeSource picks the "src" address out of `ip route get`.
func routeSource(route string) string {
	fields := strings.Fields(route)
	for i := 0; i < len(fields)-1; i++ {
		if fields[i] == "src" {
			return fields[i+1]
		}
	}

	return ""
}

// network creates the attachable overlay network Traefik and the apps share.
func (s *steps) network() error {
	s.ui.begin("Network")
	existing, err := s.output("docker", "network", "inspect", "--format", "{{.Driver}} {{.Attachable}}", Network)
	if err == nil {
		if existing != "overlay true" {
			return fmt.Errorf("network %s exists but isn't an attachable overlay (%s); remove it and run this again", Network, existing)
		}
		s.ui.ok("%s", Network)
		return nil
	}
	s.ui.doing("creating %s", Network)
	if _, err := s.output("docker", "network", "create", "--driver", "overlay", "--attachable", Network); err != nil {
		return err
	}
	s.ui.ok("%s, created", Network)

	return nil
}

// traefik deploys the Traefik stack, or updates it to the version this
// release carries. Docker leaves an unchanged stack alone, so on most runs
// this changes nothing. Let's Encrypt is used without a contact email.
func (s *steps) traefik() error {
	s.ui.begin("Traefik")
	_, err := s.output("docker", "service", "inspect", "traefik_traefik")
	running := err == nil
	if !running {
		if err := s.portsFree(); err != nil {
			return err
		}
	}

	// The stack mounts it for routes that aren't Swarm services, so it
	// must exist even while empty.
	if err := os.MkdirAll(TraefikDynamic, 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "echowa-traefik-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	stack, ports := []string{"traefik.yml"}, "80 and 443"
	if s.traefikServesUI() {
		stack, ports = append(stack, "traefik-ui.yml"), "80, 443 and "+UIPort
	}
	args := []string{"stack", "deploy", "--detach=false", "--quiet"}
	for _, name := range stack {
		data, err := files.ReadFile("files/" + name)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(tmp, name), data, 0o600); err != nil {
			return err
		}
		args = append(args, "-c", filepath.Join(tmp, name))
	}

	if running {
		s.ui.doing("updating it if this release changed it")
	} else {
		s.ui.doing("deploying, with Let's Encrypt")
	}
	if err := s.quiet(nil, "docker", append(args, "traefik")...); err != nil {
		return err
	}
	s.ui.ok("on ports %s, with Let's Encrypt", ports)

	return nil
}

// portsFree checks nothing listens on the ports Traefik binds on the host.
// Anything there means this isn't the fresh server Echowa expects.
func (s *steps) portsFree() error {
	ports := []string{"80", "443"}
	if s.traefikServesUI() {
		ports = append(ports, UIPort)
	}
	for _, port := range ports {
		if listener, _ := s.output("ss", "-Hltnp", "sport = :"+port); listener != "" {
			return fmt.Errorf("something already listens on port %s, which Traefik needs. Echowa expects a fresh server:\n%s", port, listener)
		}
	}

	return nil
}
