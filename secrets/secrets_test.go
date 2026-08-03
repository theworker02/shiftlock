package secrets

import (
	"strings"
	"testing"
)

func TestParseAndResolveEnv(t *testing.T) {
	ref, err := ParseRef("env://SHIFTLOCK_TEST_SECRET")
	if err != nil {
		t.Fatal(err)
	}
	r := EnvFileResolver{
		LookupEnv: func(k string) (string, bool) {
			if k == "SHIFTLOCK_TEST_SECRET" {
				return "super-secret", true
			}
			return "", false
		},
	}
	v, err := r.Resolve(ref)
	if err != nil {
		t.Fatal(err)
	}
	if string(v.Bytes()) != "super-secret" {
		t.Fatal("resolve mismatch")
	}
	if v.String() != "[redacted]" {
		t.Fatalf("String leaked: %q", v.String())
	}
	if strings.Contains(ref.String(), "super") {
		t.Fatal("ref should stay opaque")
	}
}

func TestUnsupportedScheme(t *testing.T) {
	if _, err := ParseRef("http://x"); err == nil {
		t.Fatal("expected error")
	}
}

func TestRedact(t *testing.T) {
	in := `password=hunter2 token: abc Authorization: Bearer abc.def`
	out := Redact(in)
	if strings.Contains(out, "hunter2") || strings.Contains(out, "abc.def") {
		t.Fatalf("not redacted: %s", out)
	}
	m := RedactMap(map[string]string{"db_password": "x", "service": "ok"})
	if m["db_password"] != "[REDACTED]" || m["service"] != "ok" {
		t.Fatalf("%v", m)
	}
}
