package main

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	crand "crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"time"
)

// selfSignedCert generates an in-memory ECDSA P-256 certificate valid for
// one year, so `-tls-self` gives instant HTTPS with zero setup. Browsers
// will warn once (unknown issuer) — that is expected for any self-signed
// cert; scripts/gen-selfsigned.sh makes a stable on-disk one for people
// who want to pin/trust it. IsCA=true so the cert doubles as its own
// trust anchor: curl --cacert server.crt verifies cleanly.
func selfSignedCert() (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), crand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := crand.Int(crand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "gvn-explorer", Organization: []string{"gvn-explorer"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		// IsCA=true is only so the cert can verify itself (curl --cacert);
		// it is never used to sign anything else.
		IsCA:                  true,
		BasicConstraintsValid: true,
		DNSNames:              dnsNames(),
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	der, err := x509.CreateCertificate(crand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
}

// dnsNames is localhost + the machine hostname, mirroring what
// scripts/gen-selfsigned.sh puts in the certificate it generates.
func dnsNames() []string {
	names := []string{"localhost"}
	if h, err := os.Hostname(); err == nil && h != "" && h != "localhost" {
		names = append(names, h)
	}
	return names
}

// httpsRedirectListener multiplexes one port by the first byte of each
// connection: a TLS record (0x16) goes to the TLS server untouched; a
// plaintext HTTP request gets an instant 308 to the https:// URL of the
// same host:port:path. Result: old http:// bookmarks and `curl http://…`
// keep working against a TLS-only port. 308 (not 301/302) preserves the
// method and body, so even a plain-HTTP POST gets re-sent as HTTPS.
func httpsRedirectListener(l net.Listener) net.Listener {
	return &sniffer{Listener: l}
}

type sniffer struct{ net.Listener }

func (s *sniffer) Accept() (net.Conn, error) {
	for {
		c, err := s.Listener.Accept()
		if err != nil {
			return nil, err
		}
		first, err := peekFirst(c)
		if err != nil { // idle probe or broken connection
			c.Close()
			continue
		}
		if first[0] == 0x16 { // TLS handshake record
			return &pushbackConn{Conn: c, prefix: first}, nil
		}
		go redirectPlain(c, first)
	}
}

// peekFirst reads exactly one byte (with a deadline so connections that
// never send anything don't wedge Accept) and returns it for replay.
func peekFirst(c net.Conn) ([]byte, error) {
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	defer c.SetReadDeadline(time.Time{})
	b := make([]byte, 1)
	if _, err := io.ReadFull(c, b); err != nil {
		return nil, err
	}
	return b, nil
}

// pushbackConn replays the sniffed byte before streaming the socket.
type pushbackConn struct {
	net.Conn
	prefix []byte
	off    int
}

func (c *pushbackConn) Read(p []byte) (int, error) {
	if c.off < len(c.prefix) {
		n := copy(p, c.prefix[c.off:])
		c.off += n
		return n, nil
	}
	return c.Conn.Read(p)
}

// redirectPlain answers one plaintext HTTP request with a 308 and closes
// the connection (the browser re-dials over TLS).
func redirectPlain(c net.Conn, first []byte) {
	defer c.Close()
	r, err := http.ReadRequest(bufio.NewReader(&pushbackConn{Conn: c, prefix: first}))
	if err != nil {
		return
	}
	host := r.Host
	if host == "" {
		host = "localhost"
	}
	fmt.Fprintf(c, "HTTP/1.1 308 Permanent Redirect\r\nLocation: https://%s%s\r\nContent-Length: 0\r\nConnection: close\r\n\r\n", host, r.RequestURI)
}
