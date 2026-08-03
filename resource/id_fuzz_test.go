package resource_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/theworker02/shiftlock/resource"
)

func FuzzParseResourceID(f *testing.F) {
	f.Add("database/prod/pay/orders")
	f.Add("cache/dev/app/index")
	f.Add("")
	f.Add("a/b/c")
	f.Add("database/prod/pay/orders/extra")
	f.Add("database//pay/orders")
	f.Add("nope/prod/pay/orders")
	f.Fuzz(func(t *testing.T, raw string) {
		id, err := resource.ParseResourceID(raw)
		if err != nil {
			return
		}
		if err := id.Validate(); err != nil {
			t.Fatalf("parsed but invalid: %v", err)
		}
		if !resource.ValidKind(id.Kind) {
			t.Fatalf("unknown kind %q", id.Kind)
		}
		s := id.String()
		id2, err := resource.ParseResourceID(s)
		if err != nil {
			t.Fatal(err)
		}
		if !id.Equal(id2) {
			t.Fatalf("%q -> %q", s, id2)
		}
		if strings.Count(s, "/") != 3 {
			t.Fatalf("bad form %q", s)
		}
		if !utf8.ValidString(s) {
			t.Fatal("invalid utf8")
		}
	})
}
