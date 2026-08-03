package aws

import "os"

// Provider returns AWS-style instance identity from environment.
type Provider struct{}

// InstanceID prefers ECS_TASK_ARN, then EC2 INSTANCE_ID, else HOSTNAME.
func (Provider) InstanceID() string {
	for _, k := range []string{"ECS_TASK_ARN", "AWS_ECS_TASK_ARN", "EC2_INSTANCE_ID", "HOSTNAME"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return "unknown"
}
