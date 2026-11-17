package certsetup

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Info struct {
	Fingerprint string   `json:"fingerprint"`
	NotAfter    string   `json:"not_after"`
	Subject     string   `json:"subject"`
	DNSNames    []string `json:"dns_names"`
	IPAddresses []string `json:"ip_addresses"`
	SelfSigned  bool     `json:"self_signed"`
	CertFile    string   `json:"-"`
}

func Ensure(dir string, extraHosts []string) (string, string, Info, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", Info{}, err
	}
	caPath := filepath.Join(dir, "ca.crt")
	caKeyPath := filepath.Join(dir, "ca.key")
	caCert, caKey, err := loadOrCreateCA(caPath, caKeyPath)
	if err != nil {
		return "", "", Info{}, err
	}

	hosts := mergeHosts(localHosts(), extraHosts)
	certPath := filepath.Join(dir, "server.crt")
	keyPath := filepath.Join(dir, "server.key")
	if !leafValid(certPath, keyPath, caCert, hosts) {
		if err := generateLeaf(certPath, keyPath, caCert, caKey, hosts); err != nil {
			return "", "", Info{}, err
		}
	}
	info, err := inspectAuto(caPath, certPath)
	if err != nil {
		return "", "", Info{}, err
	}
	return certPath, keyPath, info, nil
}

func Inspect(certPath string) (Info, error) {
	cert, err := loadCert(certPath)
	if err != nil {
		return Info{}, err
	}
	return infoFromCert(cert, certPath), nil
}

func inspectAuto(caPath, leafPath string) (Info, error) {
	ca, err := loadCert(caPath)
	if err != nil {
		return Info{}, err
	}
	leaf, err := loadCert(leafPath)
	if err != nil {
		return Info{}, err
	}
	info := infoFromCert(ca, caPath)
	info.DNSNames = leaf.DNSNames
	info.IPAddresses = nil
	for _, ip := range leaf.IPAddresses {
		info.IPAddresses = append(info.IPAddresses, ip.String())
	}
	info.SelfSigned = true
	return info, nil
}

func infoFromCert(cert *x509.Certificate, certPath string) Info {
	var ips []string
	for _, ip := range cert.IPAddresses {
		ips = append(ips, ip.String())
	}
	selfSigned := false
	if len(cert.AuthorityKeyId) == 0 || string(cert.RawIssuer) == string(cert.RawSubject) {
		selfSigned = true
	}
	return Info{
		Fingerprint: fingerprint(cert),
		NotAfter:    cert.NotAfter.UTC().Format(time.RFC3339),
		Subject:     cert.Subject.CommonName,
		DNSNames:    cert.DNSNames,
		IPAddresses: ips,
		SelfSigned:  selfSigned,
		CertFile:    certPath,
	}
}

func fingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	var parts []string
	for _, b := range sum {
		parts = append(parts, strings.ToUpper(hex.EncodeToString([]byte{b})))
	}
	return strings.Join(parts, ":")
}

func loadCert(path string) (*x509.Certificate, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(body)
	if block == nil {
		return nil, fmt.Errorf("证书文件格式无效")
	}
	return x509.ParseCertificate(block.Bytes)
}

func loadKey(path string) (*ecdsa.PrivateKey, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(body)
	if block == nil {
		return nil, fmt.Errorf("私钥文件格式无效")
	}
	return x509.ParseECPrivateKey(block.Bytes)
}

func loadOrCreateCA(certPath, keyPath string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	if cert, err := loadCert(certPath); err == nil {
		if key, err := loadKey(keyPath); err == nil {
			if cert.IsCA && cert.NotAfter.After(time.Now().AddDate(1, 0, 0)) && key.PublicKey.Equal(cert.PublicKey) {
				return cert, key, nil
			}
		}
	}
	return generateCA(certPath, keyPath)
}

func generateCA(certPath, keyPath string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, err
	}
	ski := make([]byte, 20)
	if _, err := rand.Read(ski); err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "办公室文件中心本地根证书", Organization: []string{"Office LAN"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		SubjectKeyId:          ski,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, err
	}
	if err := writePair(certPath, keyPath, der, key); err != nil {
		return nil, nil, err
	}
	return cert, key, nil
}

func generateLeaf(certPath, keyPath string, ca *x509.Certificate, caKey *ecdsa.PrivateKey, hosts []string) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}
	ski := make([]byte, 20)
	if _, err := rand.Read(ski); err != nil {
		return err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "办公室文件中心", Organization: []string{"Office LAN"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(2, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  false,
		SubjectKeyId:          ski,
		AuthorityKeyId:        ca.SubjectKeyId,
	}
	for _, host := range hosts {
		host = strings.TrimSpace(host)
		if host == "" {
			continue
		}
		if ip := net.ParseIP(host); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, strings.ToLower(host))
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		return err
	}
	return writePair(certPath, keyPath, der, key)
}

func writePair(certPath, keyPath string, der []byte, key *ecdsa.PrivateKey) error {
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return err
	}
	return os.WriteFile(keyPath, keyPEM, 0o600)
}

func leafValid(certPath, keyPath string, ca *x509.Certificate, hosts []string) bool {
	cert, err := loadCert(certPath)
	if err != nil {
		return false
	}
	key, err := loadKey(keyPath)
	if err != nil {
		return false
	}
	if !key.PublicKey.Equal(cert.PublicKey) {
		return false
	}
	if err := cert.CheckSignatureFrom(ca); err != nil {
		return false
	}
	if !cert.NotAfter.After(time.Now().AddDate(0, 0, 30)) {
		return false
	}
	return hostsCovered(cert, hosts)
}

func hostsCovered(cert *x509.Certificate, hosts []string) bool {
	for _, host := range hosts {
		host = strings.ToLower(strings.TrimSpace(host))
		if host == "" {
			continue
		}
		if ip := net.ParseIP(host); ip != nil {
			found := false
			for _, have := range cert.IPAddresses {
				if have.Equal(ip) {
					found = true
					break
				}
			}
			if !found {
				return false
			}
			continue
		}
		found := false
		for _, have := range cert.DNSNames {
			if strings.EqualFold(have, host) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func mergeHosts(base []string, extra []string) []string {
	set := map[string]bool{}
	for _, host := range append(base, extra...) {
		host = strings.ToLower(strings.TrimSpace(host))
		if host != "" {
			set[host] = true
		}
	}
	return keys(set)
}

func localHosts() []string {
	set := map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true}
	if host, err := os.Hostname(); err == nil && host != "" {
		set[strings.ToLower(host)] = true
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return keys(set)
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok || ipnet.IP.IsLoopback() || ipnet.IP.IsLinkLocalUnicast() {
				continue
			}
			set[ipnet.IP.String()] = true
		}
	}
	return keys(set)
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
