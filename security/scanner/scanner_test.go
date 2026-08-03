package scanner

import (
	"bytes"
	"strings"
	"testing"
)

func TestScanCriticalFindings(t *testing.T) {
	f := Scan(Input{
		Production:           true,
		UnsignedConfigActive: true,
		ShellExecEnabled:     true,
		DenyByDefault:        false,
		SecurityProfile:      "development",
	})
	if len(f) == 0 {
		t.Fatal("expected findings")
	}
	var buf bytes.Buffer
	if err := Format(&buf, "sarif", f); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "CFG-UNSIGNED-PROD") {
		t.Fatalf("sarif missing rule: %s", buf.String())
	}
}

func TestScanClean(t *testing.T) {
	f := Scan(Input{
		Production:         true,
		SignaturesRequired: true,
		DenyByDefault:      true,
		AuditVerifyOnStart: true,
		SecurityProfile:    "hardened",
		LeaseTTLSeconds:    15,
	})
	for _, x := range f {
		if x.Severity == SeverityCritical {
			t.Fatalf("unexpected critical: %+v", x)
		}
	}
}
