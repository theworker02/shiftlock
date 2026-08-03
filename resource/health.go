package resource

import "time"

// HealthStatus is a per-dimension or overall health signal.
type HealthStatus string

const (
	HealthUnknown   HealthStatus = "unknown"
	HealthHealthy   HealthStatus = "healthy"
	HealthDegraded  HealthStatus = "degraded"
	HealthUnhealthy HealthStatus = "unhealthy"
	HealthBlocked   HealthStatus = "blocked"
)

// HealthDimension names a measurable facet of resource health.
type HealthDimension string

const (
	DimConnectivity  HealthDimension = "connectivity"
	DimLatency       HealthDimension = "latency"
	DimCapacity      HealthDimension = "capacity"
	DimAuthn         HealthDimension = "authn"
	DimAuthz         HealthDimension = "authz"
	DimConsistency   HealthDimension = "consistency"
	DimReplication   HealthDimension = "replication"
	DimDurability    HealthDimension = "durability"
	DimAvailability  HealthDimension = "availability"
	DimConfiguration HealthDimension = "configuration"
	DimSecurity      HealthDimension = "security"
)

// AllHealthDimensions lists the standard dimensions.
func AllHealthDimensions() []HealthDimension {
	return []HealthDimension{
		DimConnectivity, DimLatency, DimCapacity, DimAuthn, DimAuthz,
		DimConsistency, DimReplication, DimDurability, DimAvailability,
		DimConfiguration, DimSecurity,
	}
}

// DimensionHealth is one dimension's status.
type DimensionHealth struct {
	Status  HealthStatus `json:"status"`
	Message string       `json:"message,omitempty"`
}

// ResourceHealth aggregates dimensions into an overall status.
type ResourceHealth struct {
	Overall    HealthStatus                       `json:"overall"`
	CheckedAt  time.Time                          `json:"checked_at"`
	Message    string                             `json:"message,omitempty"`
	Dimensions map[HealthDimension]DimensionHealth `json:"dimensions,omitempty"`
}

// ComputeOverall sets Overall from the worst dimension (unknown if empty).
func (h *ResourceHealth) ComputeOverall() {
	if len(h.Dimensions) == 0 {
		if h.Overall == "" {
			h.Overall = HealthUnknown
		}
		return
	}
	worst := HealthHealthy
	for _, d := range h.Dimensions {
		worst = worseHealth(worst, d.Status)
	}
	h.Overall = worst
}

func worseHealth(a, b HealthStatus) HealthStatus {
	if rankHealth(b) > rankHealth(a) {
		return b
	}
	return a
}

func rankHealth(s HealthStatus) int {
	switch s {
	case HealthHealthy:
		return 0
	case HealthDegraded:
		return 1
	case HealthUnknown:
		return 2
	case HealthBlocked:
		return 3
	case HealthUnhealthy:
		return 4
	default:
		return 2
	}
}

// HealthyReport is a convenience constructor.
func HealthyReport(msg string) ResourceHealth {
	h := ResourceHealth{
		Overall:   HealthHealthy,
		CheckedAt: time.Now().UTC(),
		Message:   msg,
		Dimensions: map[HealthDimension]DimensionHealth{
			DimAvailability: {Status: HealthHealthy},
			DimConnectivity: {Status: HealthHealthy},
		},
	}
	return h
}
