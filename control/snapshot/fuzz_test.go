package snapshot

import (
	"encoding/json"
	"testing"
)

func FuzzCreateAndDiff(f *testing.F) {
	seed, _ := json.Marshal(map[string]any{
		"claims": 1, "password": "secret", "nested": map[string]any{"token": "t"},
	})
	f.Add(seed)
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"a":[1,2,3],"api_key":"x"}`))
	f.Add([]byte(`"plain"`))

	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 1<<16 {
			return
		}
		var data map[string]any
		if err := json.Unmarshal(raw, &data); err != nil {
			// also accept arbitrary string payloads via wrapper
			data = map[string]any{"payload": string(raw)}
		}
		a, err := Create("fuzz", "i", data, map[string]string{"token": "abc"})
		if err != nil {
			t.Fatal(err)
		}
		b, err := Create("fuzz", "i", map[string]any{"x": 1}, nil)
		if err != nil {
			t.Fatal(err)
		}
		_ = Diff(a, b)
		out, err := a.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		var round Snapshot
		_ = json.Unmarshal(out, &round)
	})
}
