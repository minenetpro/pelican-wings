package server

import (
	"testing"

	"github.com/Minenetpro/pelican-wings/environment"
)

func secureTestConfig(image string) Configuration {
	var c Configuration
	c.Container.Image = image
	return c
}

func TestValidateSecureConfiguration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		config  Configuration
		wantErr bool
	}{
		{
			name:   "accepts digest pinned image",
			config: secureTestConfig("ghcr.io/pelican-dev/example@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		},
		{
			name:   "accepts tag image",
			config: secureTestConfig("ghcr.io/pelican-dev/example:latest"),
		},
		{
			name: "rejects custom mounts",
			config: func() Configuration {
				c := secureTestConfig("ghcr.io/pelican-dev/example@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
				c.Mounts = []Mount{{Source: "/tmp/source", Target: "/tmp/target"}}
				return c
			}(),
			wantErr: true,
		},
		{
			name: "rejects forced outgoing ip",
			config: func() Configuration {
				c := secureTestConfig("ghcr.io/pelican-dev/example@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
				c.Allocations = environment.Allocations{ForceOutgoingIP: true}
				return c
			}(),
			wantErr: true,
		},
		{
			name: "accepts none ingress mode",
			config: func() Configuration {
				c := secureTestConfig("ghcr.io/pelican-dev/example:latest")
				c.Ingress = environment.Ingress{Mode: environment.NoIngressMode}
				return c
			}(),
		},
		{
			name: "accepts conduit ingress mode with settings",
			config: func() Configuration {
				c := secureTestConfig("ghcr.io/pelican-dev/example:latest")
				c.Ingress = environment.Ingress{
					Mode: environment.ConduitDedicatedIngressMode,
					Conduit: &environment.ConduitIngress{
						ServerAddr: "203.0.113.10",
						ServerPort: 7000,
						AuthToken:  "token",
						PortStart:  1024,
						PortEnd:    49151,
					},
				}
				return c
			}(),
		},
		{
			name: "rejects conduit ingress without settings",
			config: func() Configuration {
				c := secureTestConfig("ghcr.io/pelican-dev/example:latest")
				c.Ingress = environment.Ingress{Mode: environment.ConduitDedicatedIngressMode}
				return c
			}(),
			wantErr: true,
		},
		{
			name: "rejects conduit ingress without range",
			config: func() Configuration {
				c := secureTestConfig("ghcr.io/pelican-dev/example:latest")
				c.Ingress = environment.Ingress{
					Mode: environment.ConduitDedicatedIngressMode,
					Conduit: &environment.ConduitIngress{
						ServerAddr: "203.0.113.10",
						ServerPort: 7000,
						AuthToken:  "token",
					},
				}
				return c
			}(),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateSecureConfiguration(&tt.config)
			if tt.wantErr && err == nil {
				t.Fatal("expected validation error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected validation error: %v", err)
			}
		})
	}
}
