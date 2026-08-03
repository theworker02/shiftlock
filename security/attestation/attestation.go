// Package attestation describes runtime identity evidence and trust levels.
//
// Self-reported fields must never be treated as verified without an explicit
// trust upgrade from platform or cryptographic verification.
package attestation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

// TrustLevel classifies how strongly evidence was validated.
type TrustLevel string

const (
	// TrustSelfReported is process-local data with no external verification.
	TrustSelfReported TrustLevel = "self-reported"
	// TrustPlatformVerified was confirmed by the hosting platform (e.g. K8s SA).
	TrustPlatformVerified TrustLevel = "platform-verified"
	// TrustCryptoVerified was checked against a signature or digest allowlist.
	TrustCryptoVerified TrustLevel = "crypto-verified"
	// TrustExternallyAttested was confirmed by an external attestation service.
	TrustExternallyAttested TrustLevel = "externally-attested"
)

// Evidence is one piece of attestation material with an explicit trust level.
type Evidence struct {
	Kind       string         `json:"kind"`
	Value      string         `json:"value"`
	Trust      TrustLevel     `json:"trust"`
	CollectedAt time.Time     `json:"collected_at"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

// Verified reports whether this evidence is above self-reported trust.
func (e Evidence) Verified() bool {
	switch e.Trust {
	case TrustPlatformVerified, TrustCryptoVerified, TrustExternallyAttested:
		return true
	default:
		return false
	}
}

// Report aggregates attestation evidence for a generation or process.
type Report struct {
	Service           string     `json:"service"`
	InstanceID        string     `json:"instance_id"`
	GenerationID      string     `json:"generation_id,omitempty"`
	ModuleVersion     string     `json:"module_version,omitempty"`
	ProtocolVersion   string     `json:"protocol_version,omitempty"`
	CollectedAt       time.Time  `json:"collected_at"`
	Evidence          []Evidence `json:"evidence"`
	OverallTrust      TrustLevel `json:"overall_trust"`
	SecurityPolicyHash string    `json:"security_policy_hash,omitempty"`
}

// OverallTrustFromEvidence picks the strongest trust present, defaulting to self-reported.
func OverallTrustFromEvidence(ev []Evidence) TrustLevel {
	best := TrustSelfReported
	rank := map[TrustLevel]int{
		TrustSelfReported:       1,
		TrustPlatformVerified:   2,
		TrustCryptoVerified:     3,
		TrustExternallyAttested: 4,
	}
	for _, e := range ev {
		if rank[e.Trust] > rank[best] {
			best = e.Trust
		}
	}
	return best
}

// SelfReport builds a self-reported attestation from local process metadata.
// Callers must not treat the result as platform- or crypto-verified.
func SelfReport(service, instance, generation, moduleVersion string) Report {
	now := time.Now().UTC()
	ev := []Evidence{
		{Kind: "service", Value: service, Trust: TrustSelfReported, CollectedAt: now},
		{Kind: "instance_id", Value: instance, Trust: TrustSelfReported, CollectedAt: now},
	}
	if generation != "" {
		ev = append(ev, Evidence{Kind: "generation_id", Value: generation, Trust: TrustSelfReported, CollectedAt: now})
	}
	if moduleVersion != "" {
		ev = append(ev, Evidence{Kind: "module_version", Value: moduleVersion, Trust: TrustSelfReported, CollectedAt: now})
	}
	return Report{
		Service:      service,
		InstanceID:   instance,
		GenerationID: generation,
		ModuleVersion: moduleVersion,
		CollectedAt:  now,
		Evidence:     ev,
		OverallTrust: TrustSelfReported,
	}
}

// DigestHex returns a hex SHA-256 of the report's canonical JSON (excluding OverallTrust recompute).
func (r Report) DigestHex() (string, error) {
	b, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// RequireMinTrust returns false if overall trust is below min.
func (r Report) RequireMinTrust(min TrustLevel) bool {
	rank := map[TrustLevel]int{
		TrustSelfReported:       1,
		TrustPlatformVerified:   2,
		TrustCryptoVerified:     3,
		TrustExternallyAttested: 4,
	}
	return rank[r.OverallTrust] >= rank[min]
}
