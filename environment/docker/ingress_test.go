package docker

import (
	"strings"
	"testing"

	"github.com/Minenetpro/pelican-wings/environment"
)

func TestRenderConduitClientConfig(t *testing.T) {
	t.Parallel()

	t.Run("renders explicit conduit proxy ports", func(t *testing.T) {
		t.Parallel()

		rendered, err := renderConduitClientConfig("server-1", environment.Ingress{
			Mode: environment.ConduitDedicatedIngressMode,
			Conduit: &environment.ConduitIngress{
				ServerAddr: "203.0.113.10",
				ServerPort: 7000,
				AuthToken:  "secret",
				PortStart:  25565,
				PortEnd:    25566,
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		for _, expected := range []string{
			`serverAddr = "203.0.113.10"`,
			`serverPort = 7000`,
			`token = "secret"`,
			`localIP = "server-1"`,
			`remotePort = 25565`,
			`remotePort = 25566`,
		} {
			if !strings.Contains(rendered, expected) {
				t.Fatalf("expected rendered config to contain %q, got:\n%s", expected, rendered)
			}
		}
	})

	t.Run("renders the full dedicated conduit range", func(t *testing.T) {
		t.Parallel()

		rendered, err := renderConduitClientConfig("server-1", environment.Ingress{
			Mode: environment.ConduitDedicatedIngressMode,
			Conduit: &environment.ConduitIngress{
				ServerAddr: "203.0.113.10",
				ServerPort: 7000,
				AuthToken:  "secret",
				PortStart:  1024,
				PortEnd:    1026,
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		for _, expected := range []string{
			`remotePort = 1024`,
			`remotePort = 1025`,
			`remotePort = 1026`,
		} {
			if !strings.Contains(rendered, expected) {
				t.Fatalf("expected dedicated range port in rendered config, got:\n%s", rendered)
			}
		}
	})

	t.Run("rejects conduit ingress without a valid range", func(t *testing.T) {
		t.Parallel()

		_, err := renderConduitClientConfig("server-1", environment.Ingress{
			Mode: environment.ConduitDedicatedIngressMode,
			Conduit: &environment.ConduitIngress{
				ServerAddr: "203.0.113.10",
				ServerPort: 7000,
				AuthToken:  "secret",
			},
		})
		if err == nil {
			t.Fatal("expected missing range error")
		}
	})
}
