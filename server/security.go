package server

import (
	"fmt"

	"github.com/Minenetpro/pelican-wings/environment"
)

func validateSecureConfiguration(c *Configuration) error {
	if len(c.Mounts) > 0 {
		return fmt.Errorf("custom server mounts are not supported in secure multi-tenant mode")
	}

	if c.Allocations.ForceOutgoingIP {
		return fmt.Errorf("force_outgoing_ip is not supported in secure multi-tenant mode")
	}

	switch c.Ingress.EffectiveMode() {
	case environment.ConduitDedicatedIngressMode, environment.NoIngressMode:
	default:
		return fmt.Errorf("unsupported ingress mode: %s", c.Ingress.Mode)
	}
	if c.Ingress.EffectiveMode() == environment.ConduitDedicatedIngressMode && c.Ingress.Conduit == nil {
		return fmt.Errorf("conduit ingress settings are required for conduit_dedicated mode")
	}
	if c.Ingress.EffectiveMode() == environment.ConduitDedicatedIngressMode {
		if c.Ingress.Conduit.PortStart < 1 || c.Ingress.Conduit.PortEnd > 65535 || c.Ingress.Conduit.PortStart > c.Ingress.Conduit.PortEnd {
			return fmt.Errorf("valid conduit port_start and port_end are required for conduit_dedicated mode")
		}
	}

	return nil
}
