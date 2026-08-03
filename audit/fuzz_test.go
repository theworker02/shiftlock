package audit

import (
	"encoding/json"
	"testing"
)

func FuzzVerifyRecordsJSON(f *testing.F) {
	s := New()
	_, _ = s.Append(Actor{ID: "op"}, "seed", "r", "ok", "op-1", nil)
	_, _ = s.Append(Actor{ID: "op"}, "seed2", "r", "ok", "op-2", nil)
	seed, _ := json.Marshal(s.Records())
	f.Add(seed)
	f.Add([]byte(`[]`))
	f.Add([]byte(`[{}]`))
	f.Add([]byte(`[{"sequence":1,"action":"x"}]`))
	f.Add([]byte(`[{"sequence":2,"previous_hash":"00","record_hash":"ff"}]`))

	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 1<<18 {
			return
		}
		var recs []Record
		if err := json.Unmarshal(raw, &recs); err != nil {
			return
		}
		if len(recs) > 256 {
			recs = recs[:256]
		}
		rep := VerifyRecords(recs, nil)
		_ = rep.OK
	})
}

func FuzzComputeHash(f *testing.F) {
	f.Add([]byte(`{"sequence":1,"action":"a"}`))
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 1<<14 {
			return
		}
		var r Record
		if err := json.Unmarshal(raw, &r); err != nil {
			return
		}
		_ = ComputeHash(&r)
	})
}
