package environment

import "os"

// Provider reads instance identity from an environment variable.
type Provider struct {
	Key string // default SHIFTLOCK_INSTANCE_ID
}

// InstanceID returns the env value or empty string.
func (p Provider) InstanceID() string {
	key := p.Key
	if key == "" {
		key = "SHIFTLOCK_INSTANCE_ID"
	}
	return os.Getenv(key)
}
