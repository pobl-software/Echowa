package services

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"

	"goravel/app/facades"
	"goravel/app/models"
)

// domainCheckTimeout bounds a domain's DNS and certificate checks, which
// run side by side, so a page asking for them stays inside the 3s request
// timeout.
const domainCheckTimeout = 2 * time.Second

// DNSState says whether a domain's records reach this server.
type DNSState string

const (
	// Every A and AAAA record is one of this server's addresses.
	DNSPointsHere DNSState = "points_here"
	// At least one record is another address, such as a proxy's.
	DNSElsewhere DNSState = "elsewhere"
	// The domain has no A or AAAA records.
	DNSMissing DNSState = "missing"
	// A .localhost name, which resolves to the machine asking (RFC 6761).
	DNSLocal DNSState = "local"
	// The lookup failed, or this server's address isn't known.
	DNSUnknown DNSState = "unknown"
)

// DNSRecord is one address a domain resolves to, and whether it's this
// server's.
type DNSRecord struct {
	Address string `json:"address"`
	Here    bool   `json:"here"`
}

// DNSCheck is what a domain's DNS says, against this server's public
// addresses.
type DNSCheck struct {
	State   DNSState    `json:"state"`
	Records []DNSRecord `json:"records"`
	Server  []string    `json:"server"`
	Error   string      `json:"error,omitempty"`
}

// CertificateState says what Traefik serves for a domain over HTTPS.
type CertificateState string

const (
	// The domain is plain HTTP.
	CertificateOff CertificateState = "off"
	// A trusted certificate for the domain, not yet due for renewal.
	CertificateActive CertificateState = "active"
	// Trusted, but past the point Traefik should have renewed it.
	CertificateRenewalOverdue CertificateState = "renewal_overdue"
	// Traefik serves its own default certificate: Let's Encrypt hasn't
	// issued one yet, or the challenge is failing.
	CertificatePending CertificateState = "pending"
	CertificateExpired CertificateState = "expired"
	// A certificate that isn't trusted for the domain.
	CertificateInvalid CertificateState = "invalid"
	// Nothing answered HTTPS for the domain.
	CertificateUnreachable CertificateState = "unreachable"
)

// CertificateCheck is the certificate Traefik serves for a domain.
type CertificateCheck struct {
	State     CertificateState `json:"state"`
	Issuer    string           `json:"issuer,omitempty"`
	NotBefore *time.Time       `json:"not_before,omitempty"`
	NotAfter  *time.Time       `json:"not_after,omitempty"`
	// When Traefik should renew it: a third of its lifetime before it
	// expires, which is 30 days for Let's Encrypt's 90-day certificates.
	RenewsAt *time.Time `json:"renews_at,omitempty"`
	Error    string     `json:"error,omitempty"`
}

// DomainStatus is the Domains tab's view of an app's domain.
type DomainStatus struct {
	Domain      string           `json:"domain"`
	DNS         DNSCheck         `json:"dns"`
	Certificate CertificateCheck `json:"certificate"`
	CheckedAt   time.Time        `json:"checked_at"`
}

// traefikDefaultCert is the subject Traefik gives the certificate it
// serves when it has none for the name asked for.
const traefikDefaultCert = "TRAEFIK DEFAULT CERT"

// Swapped by tests, which have neither DNS nor a public address.
var (
	LookupDomain = func(ctx context.Context, domain string) ([]netip.Addr, error) {
		return net.DefaultResolver.LookupNetIP(ctx, "ip", domain)
	}
	ServerAddresses = serverAddresses
	// CertificateRoots verifies served certificates. Nil uses the system's.
	CertificateRoots *x509.CertPool
)

// CheckAppDomain checks the app's domain's DNS and certificate. It's nil
// for an app with no domain.
func CheckAppDomain(ctx context.Context, app *models.App) *DomainStatus {
	if app.Domain == nil {
		return nil
	}
	domain := *app.Domain
	ctx, cancel := context.WithTimeout(ctx, domainCheckTimeout)
	defer cancel()

	status := &DomainStatus{Domain: domain, CheckedAt: time.Now().UTC()}
	var wg sync.WaitGroup
	wg.Go(func() { status.DNS = CheckDNS(ctx, domain) })
	wg.Go(func() {
		if app.TLS {
			status.Certificate = CheckCertificate(ctx, domain, status.CheckedAt)
		} else {
			status.Certificate = CertificateCheck{State: CertificateOff}
		}
	})
	wg.Wait()

	return status
}

// CheckDNS looks up domain's A and AAAA records and compares them with
// this server's public addresses.
func CheckDNS(ctx context.Context, domain string) DNSCheck {
	check := DNSCheck{Records: []DNSRecord{}, Server: []string{}}
	if domain == "localhost" || strings.HasSuffix(domain, ".localhost") {
		check.State = DNSLocal
		return check
	}

	var records, server []netip.Addr
	var lookupErr, serverErr error
	var wg sync.WaitGroup
	wg.Go(func() { records, lookupErr = LookupDomain(ctx, domain) })
	wg.Go(func() { server, serverErr = ServerAddresses(ctx) })
	wg.Wait()

	for _, addr := range server {
		check.Server = append(check.Server, addr.String())
	}
	var dnsErr *net.DNSError
	switch {
	case errors.As(lookupErr, &dnsErr) && dnsErr.IsNotFound:
		check.State = DNSMissing
		return check
	case lookupErr != nil:
		check.State = DNSUnknown
		check.Error = "Couldn't look up the domain: " + lookupErr.Error()
		return check
	}

	check.State = DNSPointsHere
	for _, addr := range uniqueAddrs(records) {
		here := slices.Contains(server, addr)
		check.Records = append(check.Records, DNSRecord{Address: addr.String(), Here: here})
		if !here {
			check.State = DNSElsewhere
		}
	}
	switch {
	case len(check.Records) == 0:
		check.State = DNSMissing
	case len(server) == 0:
		check.State = DNSUnknown
		check.Error = "Couldn't find this server's public address"
		if serverErr != nil {
			check.Error += ": " + serverErr.Error()
		}
		check.Error += "."
	}

	return check
}

func uniqueAddrs(addrs []netip.Addr) []netip.Addr {
	out := make([]netip.Addr, 0, len(addrs))
	for _, addr := range addrs {
		if addr = addr.Unmap(); !slices.Contains(out, addr) {
			out = append(out, addr)
		}
	}

	return out
}

// CheckCertificate connects to Traefik on this machine, asks for domain
// and reads the certificate it serves. Going to Traefik directly, not
// through DNS, shows this server's certificate even behind a proxy.
func CheckCertificate(ctx context.Context, domain string, now time.Time) CertificateCheck {
	return certificateAt(ctx, facades.Config().GetString("echowa.traefik_https", "127.0.0.1:443"), domain, now)
}

func certificateAt(ctx context.Context, addr, domain string, now time.Time) CertificateCheck {
	dialer := tls.Dialer{Config: &tls.Config{
		ServerName: domain,
		// Verified below, so an untrusted certificate can say what's wrong
		// with it instead of failing the handshake.
		InsecureSkipVerify: true, //nolint:gosec
	}}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return CertificateCheck{State: CertificateUnreachable, Error: "Traefik didn't answer HTTPS: " + err.Error()}
	}
	defer conn.Close()

	chain := conn.(*tls.Conn).ConnectionState().PeerCertificates
	if len(chain) == 0 {
		return CertificateCheck{State: CertificateInvalid, Error: "Traefik sent no certificate."}
	}

	return readCertificate(domain, chain, now)
}

func readCertificate(domain string, chain []*x509.Certificate, now time.Time) CertificateCheck {
	leaf := chain[0]
	if leaf.Subject.CommonName == traefikDefaultCert {
		return CertificateCheck{State: CertificatePending}
	}

	notBefore, notAfter := leaf.NotBefore.UTC(), leaf.NotAfter.UTC()
	renewsAt := notAfter.Add(-notAfter.Sub(notBefore) / 3).Truncate(time.Second)
	check := CertificateCheck{Issuer: issuerName(leaf), NotBefore: &notBefore, NotAfter: &notAfter, RenewsAt: &renewsAt}

	intermediates := x509.NewCertPool()
	for _, cert := range chain[1:] {
		intermediates.AddCert(cert)
	}
	_, err := leaf.Verify(x509.VerifyOptions{DNSName: domain, Roots: CertificateRoots, Intermediates: intermediates, CurrentTime: now})
	var invalid x509.CertificateInvalidError
	switch {
	case errors.As(err, &invalid) && invalid.Reason == x509.Expired && now.After(notAfter):
		check.State = CertificateExpired
	case err != nil:
		check.State = CertificateInvalid
		check.Error = err.Error()
	// Traefik checks once a day, so allow a day before calling it late.
	case now.After(renewsAt.Add(24 * time.Hour)):
		check.State = CertificateRenewalOverdue
	default:
		check.State = CertificateActive
	}

	return check
}

// issuerName is "Let's Encrypt R12": the issuer's organisation and name.
func issuerName(cert *x509.Certificate) string {
	parts := append(slices.Clone(cert.Issuer.Organization), cert.Issuer.CommonName)

	return strings.TrimSpace(strings.Join(parts, " "))
}

// serverAddresses is this server's public addresses: its interfaces'
// own, and when none is IPv4, the address the outside world sees, since
// clouds such as AWS and GCP put the public IPv4 on a NAT in front.
func serverAddresses(ctx context.Context) ([]netip.Addr, error) {
	addrs, err := publicInterfaceAddrs()
	if err != nil {
		return nil, err
	}
	if slices.ContainsFunc(addrs, netip.Addr.Is4) {
		return addrs, nil
	}
	outside, err := outsideAddress(ctx)
	if err != nil {
		if len(addrs) > 0 {
			return addrs, nil
		}
		return nil, err
	}

	return append(addrs, outside), nil
}

// cgnat is the shared address space carriers put in front of customers
// (RFC 6598). IsPrivate doesn't cover it.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

func publicInterfaceAddrs() ([]netip.Addr, error) {
	ifaceAddrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}
	var addrs []netip.Addr
	for _, a := range ifaceAddrs {
		prefix, err := netip.ParsePrefix(a.String())
		if err != nil {
			continue
		}
		addr := prefix.Addr().Unmap()
		if addr.IsGlobalUnicast() && !addr.IsPrivate() && !cgnat.Contains(addr) && !slices.Contains(addrs, addr) {
			addrs = append(addrs, addr)
		}
	}

	return addrs, nil
}

// traceURL answers with the address a request came from, as "ip=...". It
// is Cloudflare's resolver's own address, so it needs no DNS and is IPv4.
const traceURL = "https://1.1.1.1/cdn-cgi/trace"

// outside caches the address traceURL reported, for an hour.
var outside struct {
	sync.Mutex
	addr netip.Addr
	at   time.Time
}

func outsideAddress(ctx context.Context) (netip.Addr, error) {
	outside.Lock()
	defer outside.Unlock()
	if outside.addr.IsValid() && time.Since(outside.at) < time.Hour {
		return outside.addr, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, traceURL, nil)
	if err != nil {
		return netip.Addr{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return netip.Addr{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return netip.Addr{}, fmt.Errorf("%s answered %s", traceURL, resp.Status)
	}
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		if value, ok := strings.CutPrefix(scanner.Text(), "ip="); ok {
			addr, err := netip.ParseAddr(value)
			if err != nil {
				return netip.Addr{}, err
			}
			outside.addr, outside.at = addr.Unmap(), time.Now()
			return outside.addr, nil
		}
	}

	return netip.Addr{}, fmt.Errorf("%s didn't say the address", traceURL)
}
