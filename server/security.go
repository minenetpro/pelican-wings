package server

import "fmt"

func validateSecureConfiguration(c *Configuration) error {
	if len(c.Mounts) > 0 {
		return fmt.Errorf("custom server mounts are not supported in secure multi-tenant mode")
	}

	if c.Allocations.ForceOutgoingIP {
		return fmt.Errorf("force_outgoing_ip is not supported in secure multi-tenant mode")
	}

	return nil
}
