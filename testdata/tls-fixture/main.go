// Command tls-fixture writes a throw-away self-signed TLS certificate for the
// maintained HTTPS browser acceptance suite (ADR 019 slice 5).
//
// It is test fixture code under testdata/, never part of the GoAl binary:
// go build ./... skips testdata directories.
//
// Usage:
//
//	tls-fixture -cert <path> -key <path> -dns <host[,host...]> [-hours N]
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"flag"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"
)

func main() {
	certPath := flag.String("cert", "", "absolute path of the certificate PEM to write")
	keyPath := flag.String("key", "", "absolute path of the private key PEM to write")
	dnsList := flag.String("dns", "", "comma-separated DNS names to put in the SAN")
	hours := flag.Int("hours", 24, "validity window in hours, starting one hour ago")
	flag.Parse()

	if err := run(*certPath, *keyPath, *dnsList, *hours); err != nil {
		fmt.Fprintln(os.Stderr, "tls-fixture:", err)
		os.Exit(1)
	}
}

func run(certPath, keyPath, dnsList string, hours int) error {
	if certPath == "" || keyPath == "" {
		return fmt.Errorf("-cert and -key are required")
	}
	var dnsNames []string
	for _, name := range strings.Split(dnsList, ",") {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			dnsNames = append(dnsNames, trimmed)
		}
	}
	if len(dnsNames) == 0 {
		return fmt.Errorf("-dns is required")
	}
	if hours <= 1 {
		return fmt.Errorf("-hours must be greater than the one-hour backdate")
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fmt.Errorf("generate serial: %w", err)
	}

	// Backdated by an hour so clock skew between the fixture and the process
	// start cannot trip GoAl's fail-closed "not yet valid" startup check.
	notBefore := time.Now().Add(-time.Hour)
	notAfter := notBefore.Add(time.Duration(hours) * time.Hour)

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: dnsNames[0], Organization: []string{"GoAl test fixture"}},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     dnsNames,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return fmt.Errorf("create certificate: %w", err)
	}

	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return fmt.Errorf("marshal key: %w", err)
	}

	if err := writePEM(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return err
	}
	if err := writePEM(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return err
	}

	fmt.Printf("cert=%s key=%s dns=%s notBefore=%s notAfter=%s\n",
		certPath, keyPath, strings.Join(dnsNames, ","),
		notBefore.Format(time.RFC3339), notAfter.Format(time.RFC3339))
	return nil
}

func writePEM(path string, data []byte, mode os.FileMode) error {
	if err := os.WriteFile(path, data, mode); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
