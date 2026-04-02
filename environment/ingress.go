package environment

const (
	ConduitDedicatedIngressMode = "conduit_dedicated"
	NoIngressMode              = "none"
)

type ConduitIngress struct {
	FrpsID       string `json:"frps_id"`
	EndpointHost string `json:"endpoint_host"`
	ServerAddr   string `json:"server_addr"`
	ServerPort   int    `json:"server_port"`
	AuthToken    string `json:"auth_token"`
	AllowedPorts string `json:"allowed_ports"`
	PortStart    int    `json:"port_start"`
	PortEnd      int    `json:"port_end"`
}

type Ingress struct {
	Mode    string          `json:"mode"`
	Conduit *ConduitIngress `json:"conduit"`
}

func (i Ingress) EffectiveMode() string {
	switch i.Mode {
	case ConduitDedicatedIngressMode, NoIngressMode:
		return i.Mode
	default:
		return ""
	}
}

func (i Ingress) ServiceBindAddress(_ string) string {
	return "0.0.0.0"
}

func (i Ingress) PortList() []int {
	if i.EffectiveMode() != ConduitDedicatedIngressMode || i.Conduit == nil {
		return nil
	}
	if i.Conduit.PortStart < 1 || i.Conduit.PortEnd > 65535 || i.Conduit.PortStart > i.Conduit.PortEnd {
		return nil
	}

	out := make([]int, 0, i.Conduit.PortEnd-i.Conduit.PortStart+1)
	for port := i.Conduit.PortStart; port <= i.Conduit.PortEnd; port += 1 {
		out = append(out, port)
	}
	return out
}
