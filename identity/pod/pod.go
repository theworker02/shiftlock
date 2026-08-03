package pod

import "os"

// Provider returns Kubernetes pod name as instance identity.
type Provider struct {
	EnvKey string // default POD_NAME, then HOSTNAME
}

// InstanceID prefers POD_NAME, then HOSTNAME, else "unknown".
func (p Provider) InstanceID() string {
	keys := []string{p.EnvKey, "POD_NAME", "HOSTNAME"}
	for _, k := range keys {
		if k == "" {
			continue
		}
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return "unknown"
}
