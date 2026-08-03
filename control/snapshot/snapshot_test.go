package snapshot

import (
	"strings"
	"testing"
)

func TestCreateRedactsSecrets(t *testing.T) {
	s, err := Create("svc", "i1", map[string]any{
		"claims":  2,
		"db_password": "hunter2",
		"nested": map[string]any{"token": "abc", "ok": true},
	}, map[string]string{"api_key": "xyz"})
	if err != nil {
		t.Fatal(err)
	}
	if s.SchemaVersion != SchemaVersion || s.ContentHash == "" {
		t.Fatalf("bad snapshot: %+v", s)
	}
	raw, _ := s.Marshal()
	if strings.Contains(string(raw), "hunter2") || strings.Contains(string(raw), "xyz") {
		t.Fatalf("secret leaked: %s", raw)
	}
}

func TestDiff(t *testing.T) {
	a, _ := Create("s", "i", map[string]any{"x": 1, "y": "a"}, nil)
	b, _ := Create("s", "i", map[string]any{"x": 2, "z": true}, nil)
	d := Diff(a, b)
	ops := map[string]string{}
	for _, e := range d {
		ops[e.Path] = e.Op
	}
	if ops["x"] != "change" || ops["y"] != "remove" || ops["z"] != "add" {
		t.Fatalf("%+v", d)
	}
}
