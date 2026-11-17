package certsetup

import (
	"path/filepath"
	"testing"
)

func TestEnsureCreatesCAAndReissuesLeaf(t *testing.T) {
	dir := t.TempDir()
	certPath, _, info, err := Ensure(dir, []string{"192.168.1.100"})
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	ca, err := loadCert(filepath.Join(dir, "ca.crt"))
	if err != nil {
		t.Fatalf("load ca: %v", err)
	}
	if !ca.IsCA {
		t.Fatal("root certificate is not a CA")
	}
	leaf, err := loadCert(certPath)
	if err != nil {
		t.Fatalf("load leaf: %v", err)
	}
	if err := leaf.CheckSignatureFrom(ca); err != nil {
		t.Fatalf("leaf is not signed by ca: %v", err)
	}
	if !hostsCovered(leaf, []string{"192.168.1.100", "localhost"}) {
		t.Fatalf("leaf SAN missing hosts: dns=%v ips=%v", leaf.DNSNames, leaf.IPAddresses)
	}
	if info.Fingerprint != fingerprint(ca) {
		t.Fatal("reported fingerprint is not the root certificate fingerprint")
	}

	caFingerprint := info.Fingerprint
	_, _, info, err = Ensure(dir, []string{"192.168.1.100"})
	if err != nil {
		t.Fatalf("ensure again: %v", err)
	}
	if info.Fingerprint != caFingerprint {
		t.Fatal("root certificate changed without reason")
	}

	_, _, info, err = Ensure(dir, []string{"192.168.1.100", "fileserver.office.local"})
	if err != nil {
		t.Fatalf("ensure with new host: %v", err)
	}
	if info.Fingerprint != caFingerprint {
		t.Fatal("root certificate changed when reissuing leaf")
	}
	leaf, err = loadCert(certPath)
	if err != nil {
		t.Fatalf("load reissued leaf: %v", err)
	}
	if !hostsCovered(leaf, []string{"fileserver.office.local"}) {
		t.Fatalf("reissued leaf SAN missing new host: dns=%v", leaf.DNSNames)
	}
}
