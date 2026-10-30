package setup

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/term"

	"goravel/app/rules"
)

// Access is how the UI is reached, chosen once, on the first install
// (ADR 0022). It decides where Echowa listens and what APP_URL is.
type Access string

const (
	// AccessHTTP serves the UI on every address, port 8192, unencrypted.
	// The default, so an install works with nothing else set up.
	AccessHTTP Access = "http"
	// AccessHTTPS has Traefik serve https://<domain>:8192 with a Let's
	// Encrypt certificate, proxying to Echowa on the Docker bridge.
	AccessHTTPS Access = "https"
	// AccessTunnel keeps Echowa on loopback; cloudflared dials out to
	// Cloudflare, so no port is open for it.
	AccessTunnel Access = "tunnel"
)

const (
	// UIPort is the UI's public port with AccessHTTP and AccessHTTPS, apart
	// from the apps' 80 and 443 so a firewall can limit it on its own.
	UIPort = "8192"
	// innerPort is where Echowa listens behind Traefik or cloudflared.
	innerPort = "3000"
	// TraefikDynamic holds Traefik routes that aren't Swarm services: the
	// UI's, with AccessHTTPS. files/traefik.yml mounts it.
	TraefikDynamic = "/etc/echowa/traefik"
	// UIRoute is Traefik's route to the UI, with AccessHTTPS.
	UIRoute = TraefikDynamic + "/echowa.yml"
)

func (a Access) valid() bool {
	return a == AccessHTTP || a == AccessHTTPS || a == AccessTunnel
}

// chooseAccess settles how the UI will be reached, before anything is
// installed, so an install asks its questions and then runs unattended. It
// asks only on a first install with a terminal; flags answer in advance,
// and without either the UI goes on HTTP, port 8192.
func (s *steps) chooseAccess() error {
	if _, err := os.Stat(EnvFile); err == nil {
		if s.opts.Access != "" || s.opts.TunnelToken != "" {
			s.ui.say("Keeping how the UI is reached: %s already exists (deploy/README.md, \"Changing how the UI is reached\")", EnvFile)
		}
		return nil
	}
	s.fresh = true

	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		if s.opts.Access == "" {
			s.opts.Access = AccessHTTP
		}
		return s.showAccess(s.checkAccess())
	}
	defer tty.Close()
	if err := s.askAccess(tty); err != nil {
		return err
	}

	return s.showAccess(s.checkAccess())
}

// askAccess asks whatever the flags didn't answer. Ctrl-C while it waits
// leaves the terminal as it found it.
func (s *steps) askAccess(tty *os.File) error {
	fd := int(tty.Fd())
	if state, err := term.GetState(fd); err == nil {
		asked := make(chan struct{})
		defer close(asked)
		go func() {
			select {
			case <-s.ctx.Done():
				select {
				case <-asked:
					return
				default:
				}
				_ = term.Restore(fd, state)
				fmt.Fprint(tty, "\033[?25h\r\033[J")
				s.ui.cancelled()
				os.Exit(130)
			case <-asked:
			}
		}()
	}

	p := newPrompter(tty)
	if s.opts.Access == "" {
		accesses := []Access{AccessHTTP, AccessHTTPS, AccessTunnel}
		i, err := p.choose("How will you reach Echowa?", []choice{
			{"HTTP on port " + UIPort, "works straight away, but unencrypted"},
			{"HTTPS on a domain, port " + UIPort, "a Let's Encrypt certificate, needs a DNS record"},
			{"Cloudflare Tunnel", "no open port, Cloudflare in front"},
		})
		if err != nil {
			return err
		}
		s.opts.Access = accesses[i]
	}
	if s.opts.Access == AccessTunnel && s.opts.TunnelToken == "" {
		token, err := p.ask("Tunnel token", "In Zero Trust: Networks, Tunnels, your tunnel. Paste it; it stays hidden.", true, func(token string) error {
			if checkTunnelToken(token) != nil {
				return errors.New("Not a tunnel token: paste the string after `cloudflared service install`")
			}
			return nil
		})
		if err != nil {
			return err
		}
		s.opts.TunnelToken = token
	}
	if s.opts.Domain == "" && s.opts.Access != AccessHTTP {
		label, hint := "Domain", "Already pointing at this server, e.g. echowa.example.com"
		if s.opts.Access == AccessTunnel {
			label, hint = "Hostname", "The one you'll route to the tunnel, e.g. echowa.example.com"
		}
		domain, err := p.ask(label, hint, false, func(answer string) error {
			answer = strings.ToLower(answer)
			switch {
			case answer == "":
				return fmt.Errorf("Enter a domain, e.g. echowa.example.com")
			case !rules.IsDomain(answer):
				return fmt.Errorf("%q isn't a domain like echowa.example.com", answer)
			}
			return nil
		})
		if err != nil {
			return err
		}
		s.opts.Domain = strings.ToLower(domain)
	}

	return nil
}

// showAccess shows the access chosen, once it's known to be good.
func (s *steps) showAccess(err error) error {
	if err != nil {
		return err
	}
	switch s.opts.Access {
	case AccessHTTPS:
		s.ui.fact("Access", "https://%s:%s, with Let's Encrypt", s.opts.Domain, UIPort)
		if proxiedByCloudflare(s.opts.Domain) {
			s.ui.warn("%s points at Cloudflare's proxy, which doesn't pass port %s on, so the UI will time out. Set its record to \"DNS only\" (grey cloud), or choose Cloudflare Tunnel instead.", s.opts.Domain, UIPort)
		}
	case AccessTunnel:
		if s.opts.Domain != "" {
			s.ui.fact("Access", "https://%s, through a Cloudflare Tunnel", s.opts.Domain)
		} else {
			s.ui.fact("Access", "a Cloudflare Tunnel")
		}
	default:
		s.ui.fact("Access", "http://%s:%s, unencrypted", s.address(), UIPort)
	}

	return nil
}

// cloudflareRanges are the addresses Cloudflare's proxy answers on
// (https://www.cloudflare.com/ips/).
var cloudflareRanges = func() []*net.IPNet {
	var ranges []*net.IPNet
	for _, cidr := range []string{
		"173.245.48.0/20", "103.21.244.0/22", "103.22.200.0/22", "103.31.4.0/22",
		"141.101.64.0/18", "108.162.192.0/18", "190.93.240.0/20", "188.114.96.0/20",
		"197.234.240.0/22", "198.41.128.0/17", "162.158.0.0/15", "104.16.0.0/13",
		"104.24.0.0/14", "172.64.0.0/13", "131.0.72.0/22",
		"2400:cb00::/32", "2606:4700::/32", "2803:f800::/32", "2405:b500::/32",
		"2405:8100::/32", "2a06:98c0::/29", "2c0f:f248::/32",
	} {
		_, ipnet, _ := net.ParseCIDR(cidr)
		ranges = append(ranges, ipnet)
	}
	return ranges
}()

// lookupDomain resolves a domain; tests swap it.
var lookupDomain = func(domain string) ([]net.IP, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return net.DefaultResolver.LookupIP(ctx, "ip", domain)
}

// proxiedByCloudflare says whether a domain resolves to Cloudflare's proxy
// (an orange-cloud record). The proxy only passes certain ports on, and
// 8192 isn't one, so the HTTPS UI would time out behind it. A failed
// lookup says nothing: the record may not exist yet.
func proxiedByCloudflare(domain string) bool {
	ips, err := lookupDomain(domain)
	if err != nil {
		return false
	}
	for _, ip := range ips {
		for _, r := range cloudflareRanges {
			if r.Contains(ip) {
				return true
			}
		}
	}
	return false
}

func (s *steps) checkAccess() error {
	if s.opts.Domain != "" && !rules.IsDomain(s.opts.Domain) {
		return fmt.Errorf("%q isn't a domain like echowa.example.com", s.opts.Domain)
	}
	switch s.opts.Access {
	case AccessHTTPS:
		if s.opts.Domain == "" {
			return errors.New("HTTPS needs --domain, a name that points at this server")
		}
	case AccessTunnel:
		if err := checkTunnelToken(s.opts.TunnelToken); err != nil {
			return err
		}
	}

	return nil
}

// checkTunnelToken catches a token pasted wrong before anything is
// installed. A token is base64 JSON naming the account, tunnel and secret.
func checkTunnelToken(token string) error {
	if token == "" {
		return errors.New("a Cloudflare Tunnel needs its token (--tunnel-token, from the tunnel's install command)")
	}
	data, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		data, err = base64.RawStdEncoding.DecodeString(token)
	}
	var fields struct {
		Account string `json:"a"`
		Tunnel  string `json:"t"`
		Secret  string `json:"s"`
	}
	if err != nil || json.Unmarshal(data, &fields) != nil || fields.Account == "" || fields.Tunnel == "" || fields.Secret == "" {
		return errors.New("that isn't a Cloudflare Tunnel token: copy the long string after `cloudflared service install` in the tunnel's setup page")
	}

	return nil
}

// envFor is a new env file's listening lines for the chosen access. With
// AccessHTTPS, Traefik runs in a container, so Echowa listens on the
// Docker bridge gateway, which containers reach but the internet doesn't,
// and trusts Traefik's X-Real-IP from that network only.
func (s *steps) envFor() (map[string]string, error) {
	switch s.opts.Access {
	case AccessHTTPS:
		gateway, subnet, err := s.bridge()
		if err != nil {
			return nil, err
		}
		return map[string]string{
			"APP_URL":                 "https://" + s.opts.Domain + ":" + UIPort,
			"APP_HOST":                gateway,
			"APP_PORT":                innerPort,
			"ECHOWA_CLIENT_IP_HEADER": "X-Real-IP",
			"ECHOWA_TRUSTED_PROXIES":  subnet,
		}, nil
	case AccessTunnel:
		url := "https://echowa.example.com"
		if s.opts.Domain != "" {
			url = "https://" + s.opts.Domain
		}
		return map[string]string{"APP_URL": url, "APP_HOST": "127.0.0.1", "APP_PORT": innerPort}, nil
	default:
		return map[string]string{"APP_URL": "http://" + s.address() + ":" + UIPort, "APP_HOST": "0.0.0.0", "APP_PORT": UIPort}, nil
	}
}

// bridge returns docker_gwbridge's gateway address and subnet. Swarm
// creates the network, and every Swarm task, Traefik included, reaches the
// host through it.
func (s *steps) bridge() (string, string, error) {
	out, err := s.output("docker", "network", "inspect", "docker_gwbridge", "--format", "{{range .IPAM.Config}}{{.Gateway}} {{.Subnet}}{{end}}")
	gateway, subnet, _ := strings.Cut(out, " ")
	if err != nil || net.ParseIP(gateway) == nil {
		return "", "", fmt.Errorf("couldn't find the docker_gwbridge network's gateway (%q): %v", out, err)
	}
	if _, _, err := net.ParseCIDR(subnet); err != nil {
		return "", "", fmt.Errorf("docker_gwbridge's subnet %q: %v", subnet, err)
	}

	return gateway, subnet, nil
}

// address is this server's address for messages and APP_URL: the default
// route's source, which behind a cloud NAT is the private one.
func (s *steps) address() string {
	if s.opts.AdvertiseAddr != "" {
		return s.opts.AdvertiseAddr
	}
	route, _ := s.output("ip", "-4", "route", "get", "1.1.1.1")
	if addr := routeSource(route); addr != "" {
		return addr
	}

	return "<server-address>"
}

// traefikRoute writes Traefik's route to the UI for AccessHTTPS, which
// Traefik picks up as it appears. It runs after Traefik is deployed:
// docker_gwbridge only exists once a Swarm task has started.
func (s *steps) traefikRoute() error {
	if !s.fresh || s.opts.Access != AccessHTTPS {
		return nil
	}
	s.ui.begin("UI route")
	gateway, _, err := s.bridge()
	if err != nil {
		return err
	}
	if err := writeAtomic(UIRoute, []byte(renderRoute(s.opts.Domain, "http://"+gateway+":"+innerPort)), 0o644); err != nil {
		return err
	}
	s.ui.ok("https://%s:%s through Traefik", s.opts.Domain, UIPort)

	return nil
}

// traefikServesUI says whether Traefik publishes the UI's port: with
// AccessHTTPS on a first install, and afterwards while the UI's route
// exists, so a rerun or an update keeps the port.
func (s *steps) traefikServesUI() bool {
	if s.fresh {
		return s.opts.Access == AccessHTTPS
	}
	_, err := os.Stat(UIRoute)

	return err == nil
}

// renderRoute is the UI's route, on the "echowa" entrypoint (port 8192)
// only. The certificate's HTTP challenge still goes through port 80.
func renderRoute(domain, upstream string) string {
	return `# Echowa's own UI, served by Traefik on port 8192 with a Let's Encrypt
# certificate. Written by echowa setup (HTTPS access, ADR 0022).
http:
  routers:
    echowa:
      rule: Host(` + "`" + domain + "`" + `)
      entryPoints: [echowa]
      tls:
        certResolver: le
      service: echowa
  services:
    echowa:
      loadBalancer:
        servers:
          - url: ` + upstream + "\n"
}

// cloudflared installs Cloudflare's connector from its apt repository and
// runs the tunnel as a service, for AccessTunnel on a first install.
func (s *steps) cloudflared() error {
	if !s.fresh || s.opts.Access != AccessTunnel {
		return nil
	}
	s.ui.begin("Tunnel")
	apt := []string{"DEBIAN_FRONTEND=noninteractive"}
	if !s.installed("cloudflared") {
		s.ui.doing("installing cloudflared from Cloudflare's apt repository")
		key, err := download("https://pkg.cloudflare.com/cloudflare-main.gpg")
		if err != nil {
			return fmt.Errorf("Cloudflare's apt key: %w", err)
		}
		if err := os.MkdirAll("/usr/share/keyrings", 0o755); err != nil {
			return err
		}
		if err := writeAtomic("/usr/share/keyrings/cloudflare-main.gpg", key, 0o644); err != nil {
			return err
		}
		source := "deb [signed-by=/usr/share/keyrings/cloudflare-main.gpg] https://pkg.cloudflare.com/cloudflared any main\n"
		if err := writeAtomic("/etc/apt/sources.list.d/cloudflared.list", []byte(source), 0o644); err != nil {
			return err
		}
		if err := s.quiet(apt, "apt-get", "update", "-qq"); err != nil {
			return err
		}
		if err := s.quiet(apt, "apt-get", "install", "-y", "-qq", "cloudflared"); err != nil {
			return err
		}
	}
	if _, err := os.Stat(filepath.Join(UnitDir, "cloudflared.service")); err == nil {
		s.ui.ok("cloudflared already runs a tunnel, left alone")
		return nil
	}
	s.ui.doing("connecting")
	// The token stays out of the log: it lets whoever has it run the
	// tunnel. A tunnel that won't connect doesn't stop the install; it's
	// fixed apart from Echowa.
	if err := s.quiet(nil, "cloudflared", "service", "install", s.opts.TunnelToken); err != nil {
		s.ui.problem("didn't connect (output above)")
		s.ui.warn("Echowa installs anyway. Check the token, then:\nsudo cloudflared service uninstall && sudo cloudflared service install <token>")
		return nil
	}
	s.ui.ok("connected")

	return nil
}
