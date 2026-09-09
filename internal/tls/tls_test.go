package tls

import (
	"crypto/rand"
	"crypto/rsa"
	stdtls "crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/milosursulovic/vortex/internal/config"
)

// genCert writes a throwaway self-signed cert/key pair for commonName into
// dir and returns their paths, so LoadConfig can be tested without shelling
// out to openssl.
func genCert(t *testing.T, dir, commonName string) (certPath, keyPath string) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}

	certPath = filepath.Join(dir, commonName+".crt")
	keyPath = filepath.Join(dir, commonName+".key")

	certOut, err := os.Create(certPath)
	if err != nil {
		t.Fatalf("create cert file: %v", err)
	}
	defer certOut.Close()
	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		t.Fatalf("encode cert: %v", err)
	}

	keyOut, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatalf("create key file: %v", err)
	}
	defer keyOut.Close()
	if err := pem.Encode(keyOut, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}); err != nil {
		t.Fatalf("encode key: %v", err)
	}

	return certPath, keyPath
}

func TestLoadConfigDefaultCertOnly(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := genCert(t, dir, "default")

	tlsCfg, err := LoadConfig(config.TLSConfig{Certificate: certPath, Key: keyPath})
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(tlsCfg.Certificates) != 1 {
		t.Fatalf("expected 1 default certificate, got %d", len(tlsCfg.Certificates))
	}
	if tlsCfg.GetCertificate != nil {
		t.Fatal("GetCertificate should be nil when no SNI entries are configured")
	}
}

func TestLoadConfigMissingFile(t *testing.T) {
	if _, err := LoadConfig(config.TLSConfig{Certificate: "/no/such.crt", Key: "/no/such.key"}); err == nil {
		t.Fatal("expected an error for a missing certificate file")
	}
}

func TestLoadConfigSNIFallsBackToDefault(t *testing.T) {
	dir := t.TempDir()
	defaultCert, defaultKey := genCert(t, dir, "default")
	sniCert, sniKey := genCert(t, dir, "sni")

	tlsCfg, err := LoadConfig(config.TLSConfig{
		Certificate: defaultCert,
		Key:         defaultKey,
		SNI: []config.SNIEntry{
			{Host: "sni.local", Certificate: sniCert, Key: sniKey},
		},
	})
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if tlsCfg.GetCertificate == nil {
		t.Fatal("expected GetCertificate to be set when SNI entries are configured")
	}

	matched, err := tlsCfg.GetCertificate(&stdtls.ClientHelloInfo{ServerName: "sni.local"})
	if err != nil {
		t.Fatalf("GetCertificate(sni.local): %v", err)
	}
	if got := leafCN(t, matched); got != "sni" {
		t.Fatalf("expected sni certificate, got CN %q", got)
	}

	fallback, err := tlsCfg.GetCertificate(&stdtls.ClientHelloInfo{ServerName: "unknown.example.com"})
	if err != nil {
		t.Fatalf("GetCertificate(unknown): %v", err)
	}
	if got := leafCN(t, fallback); got != "default" {
		t.Fatalf("expected fallback to default certificate, got CN %q", got)
	}
}

func leafCN(t *testing.T, cert *stdtls.Certificate) string {
	t.Helper()
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatalf("parse leaf certificate: %v", err)
	}
	return leaf.Subject.CommonName
}
