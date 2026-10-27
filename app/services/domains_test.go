package services

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fakeDNS(t *testing.T, records []netip.Addr, lookupErr error, server ...netip.Addr) {
	t.Helper()
	lookup, addrs := LookupDomain, ServerAddresses
	t.Cleanup(func() { LookupDomain, ServerAddresses = lookup, addrs })
	LookupDomain = func(context.Context, string) ([]netip.Addr, error) { return records, lookupErr }
	ServerAddresses = func(context.Context) ([]netip.Addr, error) { return server, nil }
}

var (
	here      = netip.MustParseAddr("203.0.113.10")
	hereV6    = netip.MustParseAddr("2001:db8::10")
	elsewhere = netip.MustParseAddr("198.51.100.7")
)

func TestCheckDNS(t *testing.T) {
	notFound := &net.DNSError{Err: "no such host", Name: "blog.example.com", IsNotFound: true}
	tests := []struct {
		name    string
		records []netip.Addr
		err     error
		server  []netip.Addr
		want    DNSState
		listed  int
	}{
		{"every record is this server", []netip.Addr{here, hereV6}, nil, []netip.Addr{here, hereV6}, DNSPointsHere, 2},
		{"an IPv4-mapped record is the same address", []netip.Addr{netip.AddrFrom16(here.As16())}, nil, []netip.Addr{here}, DNSPointsHere, 1},
		{"one record elsewhere is enough to warn", []netip.Addr{here, elsewhere}, nil, []netip.Addr{here}, DNSElsewhere, 2},
		{"no such domain", nil, notFound, []netip.Addr{here}, DNSMissing, 0},
		{"no A or AAAA records", []netip.Addr{}, nil, []netip.Addr{here}, DNSMissing, 0},
		{"the lookup fails", nil, errors.New("i/o timeout"), []netip.Addr{here}, DNSUnknown, 0},
		{"this server's address isn't known", []netip.Addr{here}, nil, nil, DNSUnknown, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeDNS(t, tt.records, tt.err, tt.server...)
			check := CheckDNS(context.Background(), "blog.example.com")
			assert.Equal(t, tt.want, check.State, check.Error)
			assert.Len(t, check.Records, tt.listed)
			assert.NotNil(t, check.Server, "an empty list, not null")
		})
	}

	t.Run("records say which address is this server's", func(t *testing.T) {
		fakeDNS(t, []netip.Addr{here, elsewhere}, nil, here)
		check := CheckDNS(context.Background(), "blog.example.com")
		assert.Equal(t, []DNSRecord{{Address: here.String(), Here: true}, {Address: elsewhere.String(), Here: false}}, check.Records)
		assert.Equal(t, []string{here.String()}, check.Server)
	})

	t.Run(".localhost needs no lookup", func(t *testing.T) {
		fakeDNS(t, nil, errors.New("must not be called"))
		assert.Equal(t, DNSLocal, CheckDNS(context.Background(), "blog.localhost").State)
	})
}

type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func newTestCA(t *testing.T) testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{Organization: []string{"Test CA"}, CommonName: "R1"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(365 * 24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)

	return testCA{cert, key}
}

// issue signs a certificate for names, valid from notBefore for 90 days.
// An empty commonName with no names makes Traefik's default certificate.
func (ca testCA) issue(t *testing.T, commonName string, names []string, notBefore time.Time) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: commonName}, DNSNames: names,
		NotBefore: notBefore, NotAfter: notBefore.Add(90 * 24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.cert, &key.PublicKey, ca.key)
	require.NoError(t, err)

	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// serveTLS stands in for Traefik, serving cert for any name.
func serveTLS(t *testing.T, cert tls.Certificate) string {
	t.Helper()
	server := httptest.NewUnstartedServer(http.NotFoundHandler())
	server.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	server.StartTLS()
	t.Cleanup(server.Close)

	return server.Listener.Addr().String()
}

func TestCheckCertificate(t *testing.T) {
	ca := newTestCA(t)
	roots := CertificateRoots
	t.Cleanup(func() { CertificateRoots = roots })
	CertificateRoots = x509.NewCertPool()
	CertificateRoots.AddCert(ca.cert)

	issued := time.Now().Add(-10 * 24 * time.Hour).Truncate(time.Second)
	valid := ca.issue(t, "blog.example.com", []string{"blog.example.com"}, issued)
	now := time.Now()
	ctx := context.Background()

	t.Run("a trusted certificate for the domain", func(t *testing.T) {
		check := certificateAt(ctx, serveTLS(t, valid), "blog.example.com", now)
		assert.Equal(t, CertificateActive, check.State, check.Error)
		assert.Equal(t, "Test CA R1", check.Issuer)
		assert.Equal(t, issued.UTC(), *check.NotBefore)
		assert.Equal(t, issued.Add(90*24*time.Hour).UTC(), *check.NotAfter)
		assert.Equal(t, issued.Add(60*24*time.Hour).UTC(), *check.RenewsAt, "30 days before a 90-day certificate expires")
	})

	t.Run("Traefik renews a day late at most", func(t *testing.T) {
		addr := serveTLS(t, valid)
		assert.Equal(t, CertificateActive, certificateAt(ctx, addr, "blog.example.com", issued.Add(60*24*time.Hour+time.Hour)).State)
		assert.Equal(t, CertificateRenewalOverdue, certificateAt(ctx, addr, "blog.example.com", issued.Add(62*24*time.Hour)).State)
	})

	t.Run("expired", func(t *testing.T) {
		check := certificateAt(ctx, serveTLS(t, valid), "blog.example.com", issued.Add(91*24*time.Hour))
		assert.Equal(t, CertificateExpired, check.State)
	})

	t.Run("for another name", func(t *testing.T) {
		check := certificateAt(ctx, serveTLS(t, valid), "shop.example.com", now)
		assert.Equal(t, CertificateInvalid, check.State)
		assert.Contains(t, check.Error, "shop.example.com")
	})

	t.Run("from a CA nobody trusts", func(t *testing.T) {
		other := newTestCA(t).issue(t, "blog.example.com", []string{"blog.example.com"}, issued)
		assert.Equal(t, CertificateInvalid, certificateAt(ctx, serveTLS(t, other), "blog.example.com", now).State)
	})

	t.Run("Traefik's default certificate means none was issued yet", func(t *testing.T) {
		pending := ca.issue(t, traefikDefaultCert, nil, issued)
		check := certificateAt(ctx, serveTLS(t, pending), "blog.example.com", now)
		assert.Equal(t, CertificatePending, check.State)
		assert.Nil(t, check.NotAfter)
	})

	t.Run("nothing listening", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		addr := listener.Addr().String()
		require.NoError(t, listener.Close())
		assert.Equal(t, CertificateUnreachable, certificateAt(ctx, addr, "blog.example.com", now).State)
	})
}
