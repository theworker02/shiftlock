package resource

// ResourceCapabilities declares what a resource adapter actually supports.
// Callers and workflows must not assume a capability unless the corresponding
// flag is true. Adapters must never silently claim unsupported capabilities.
type ResourceCapabilities struct {
	SupportsOwnership     bool `json:"supports_ownership"`
	SupportsFencing       bool `json:"supports_fencing"`
	SupportsDrain         bool `json:"supports_drain"`
	SupportsHealth        bool `json:"supports_health"`
	SupportsFailover      bool `json:"supports_failover"`
	SupportsTransactions  bool `json:"supports_transactions"`
	SupportsSnapshots     bool `json:"supports_snapshots"`
	SupportsRecovery      bool `json:"supports_recovery"`
	SupportsRateLimit     bool `json:"supports_rate_limit"`
}

// Require returns ErrCapabilityClaimed if any required flag is false on c.
func (c ResourceCapabilities) Require(required ResourceCapabilities) error {
	check := func(name string, need, have bool) error {
		if need && !have {
			return &Error{Op: "Capabilities.Require", Err: ErrCapabilityClaimed, Message: name + " not supported"}
		}
		return nil
	}
	if err := check("ownership", required.SupportsOwnership, c.SupportsOwnership); err != nil {
		return err
	}
	if err := check("fencing", required.SupportsFencing, c.SupportsFencing); err != nil {
		return err
	}
	if err := check("drain", required.SupportsDrain, c.SupportsDrain); err != nil {
		return err
	}
	if err := check("health", required.SupportsHealth, c.SupportsHealth); err != nil {
		return err
	}
	if err := check("failover", required.SupportsFailover, c.SupportsFailover); err != nil {
		return err
	}
	if err := check("transactions", required.SupportsTransactions, c.SupportsTransactions); err != nil {
		return err
	}
	if err := check("snapshots", required.SupportsSnapshots, c.SupportsSnapshots); err != nil {
		return err
	}
	if err := check("recovery", required.SupportsRecovery, c.SupportsRecovery); err != nil {
		return err
	}
	if err := check("rate_limit", required.SupportsRateLimit, c.SupportsRateLimit); err != nil {
		return err
	}
	return nil
}
