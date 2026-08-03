package hostname

import (
	"os"
)

// Provider returns the OS hostname as instance identity.
type Provider struct{}

// InstanceID returns hostname or "unknown".
func (Provider) InstanceID() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "unknown"
	}
	return h
}
