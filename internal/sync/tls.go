package sync

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

func ensureCertificate(stateDir string) (certPath, keyPath, fingerprint string, err error) {
	certPath = filepath.Join(stateDir, "tls-cert.pem")
	keyPath = filepath.Join(stateDir, "tls-key.pem")
	if certPEM, readErr := os.ReadFile(certPath); readErr == nil {
		block, _ := pem.Decode(certPEM)
		if block == nil {
			return "", "", "", errors.New("stored TLS certificate is invalid")
		}
		cert, parseErr := x509.ParseCertificate(block.Bytes)
		if parseErr != nil {
			return "", "", "", parseErr
		}
		if _, keyErr := os.Stat(keyPath); keyErr != nil {
			return "", "", "", keyErr
		}
		return certPath, keyPath, certFingerprint(cert.Raw), nil
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return "", "", "", readErr
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", "", err
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return "", "", "", err
	}
	template := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "RekordLink local room"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(5, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return "", "", "", err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", "", "", err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	if err := WriteAtomic(certPath, certPEM, 0o644); err != nil {
		return "", "", "", err
	}
	if err := WriteAtomic(keyPath, keyPEM, 0o600); err != nil {
		return "", "", "", err
	}
	return certPath, keyPath, certFingerprint(der), nil
}

func certFingerprint(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
