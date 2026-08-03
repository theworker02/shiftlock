package redteam

import "testing"

func TestCatalogHasRequiredScenarios(t *testing.T) {
	want := []ScenarioID{
		ScenarioForgedGeneration,
		ScenarioStolenCapability,
		ScenarioReplayedCommand,
		ScenarioStaleOwner,
		ScenarioMaliciousCandidate,
		ScenarioAuditTampering,
		ScenarioConfigurationSubstitution,
		ScenarioProtocolDowngrade,
		ScenarioQuorumCollusion,
		ScenarioCandidateFlood,
		ScenarioLockdownBypass,
		ScenarioSecretExfiltration,
	}
	have := map[ScenarioID]bool{}
	for _, s := range Catalog() {
		have[s.ID] = true
		if s.Threat == "" || s.ExpectedPrevention == "" {
			t.Fatalf("%s missing threat/prevention metadata", s.ID)
		}
	}
	for _, id := range want {
		if !have[id] {
			t.Fatalf("missing scenario %s", id)
		}
	}
}

func TestRunnableScenarios(t *testing.T) {
	results, err := RunAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < 8 {
		t.Fatalf("need >=8 runnable tests, got %d", len(results))
	}
	for _, res := range results {
		if !res.Passed {
			t.Fatalf("%s did not pass: %s", res.ID, res.Message)
		}
	}
}
