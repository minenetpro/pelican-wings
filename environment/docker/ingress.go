package docker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"emperror.dev/errors"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"

	"github.com/Minenetpro/pelican-wings/config"
	"github.com/Minenetpro/pelican-wings/environment"
)

const conduitConfigMountPath = "/etc/frp/frpc.toml"

func ingressContainerName(id string) string {
	return id + "_frpc"
}

func ingressConfigPath(id string) string {
	return filepath.Join(config.Get().Conduit.ConfigDirectory, id+".toml")
}

func renderConduitClientConfig(id string, ingress environment.Ingress) (string, error) {
	if ingress.Conduit == nil {
		return "", errors.New("missing conduit ingress settings")
	}
	if ingress.Conduit.ServerAddr == "" {
		return "", errors.New("missing conduit server address")
	}
	if ingress.Conduit.ServerPort <= 0 {
		return "", errors.New("missing conduit server port")
	}
	if ingress.Conduit.AuthToken == "" {
		return "", errors.New("missing conduit auth token")
	}

	ports := ingress.PortList()
	if len(ports) == 0 {
		return "", errors.New("missing conduit port range")
	}

	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("serverAddr = %q\n", ingress.Conduit.ServerAddr))
	builder.WriteString(fmt.Sprintf("serverPort = %d\n\n", ingress.Conduit.ServerPort))
	builder.WriteString("[auth]\n")
	builder.WriteString("method = \"token\"\n")
	builder.WriteString(fmt.Sprintf("token = %q\n\n", ingress.Conduit.AuthToken))

	namePrefix := strings.ReplaceAll(id, "-", "")
	if len(namePrefix) > 16 {
		namePrefix = namePrefix[:16]
	}
	if namePrefix == "" {
		namePrefix = "server"
	}

	for _, port := range ports {
		builder.WriteString("[[proxies]]\n")
		builder.WriteString(fmt.Sprintf("name = %q\n", fmt.Sprintf("%s-%d", namePrefix, port)))
		builder.WriteString("type = \"tcp\"\n")
		builder.WriteString(fmt.Sprintf("localIP = %q\n", id))
		builder.WriteString(fmt.Sprintf("localPort = %d\n", port))
		builder.WriteString(fmt.Sprintf("remotePort = %d\n\n", port))
	}

	return builder.String(), nil
}

func (e *Environment) removeIngressContainer() error {
	ctx := context.Background()
	name := ingressContainerName(e.Id)

	if err := e.client.ContainerRemove(ctx, name, container.RemoveOptions{
		Force:         true,
		RemoveVolumes: true,
	}); err != nil && !client.IsErrNotFound(err) {
		return errors.Wrap(err, "environment/docker: failed to remove ingress sidecar")
	}

	if err := os.Remove(ingressConfigPath(e.Id)); err != nil && !os.IsNotExist(err) {
		return errors.Wrap(err, "environment/docker: failed to remove ingress config")
	}

	return nil
}

func (e *Environment) syncIngressContainer() error {
	ingress := e.Configuration.Ingress()
	if ingress.EffectiveMode() != environment.ConduitDedicatedIngressMode {
		return e.removeIngressContainer()
	}

	rendered, err := renderConduitClientConfig(e.Id, ingress)
	if err != nil {
		return err
	}
	if strings.TrimSpace(rendered) == "" {
		return e.removeIngressContainer()
	}

	cfg := config.Get()
	if err := e.removeIngressContainer(); err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.Conduit.ConfigDirectory, 0o755); err != nil {
		return errors.Wrap(err, "environment/docker: failed to create conduit config directory")
	}

	configPath := ingressConfigPath(e.Id)
	if err := os.WriteFile(configPath, []byte(rendered), 0o600); err != nil {
		return errors.Wrap(err, "environment/docker: failed to write conduit client config")
	}

	if err := e.ensureImageExists(cfg.Conduit.FrpcImage); err != nil {
		return err
	}

	networkName, err := EnsureSecureNetwork(context.Background(), e.client, e.Id)
	if err != nil {
		return err
	}

	conf := &container.Config{
		Hostname: e.Id,
		Image:    strings.TrimPrefix(cfg.Conduit.FrpcImage, "~"),
		Cmd:      []string{"-c", conduitConfigMountPath},
		Labels: map[string]string{
			"Service":       "Pelican",
			"ContainerType": "server_ingress",
			"ParentServer":  e.Id,
		},
	}

	hostConf := &container.HostConfig{
		Mounts: []mount.Mount{
			{
				Type:     mount.TypeBind,
				Source:   configPath,
				Target:   conduitConfigMountPath,
				ReadOnly: true,
			},
		},
		DNS:            cfg.Docker.Network.Dns,
		LogConfig:      cfg.Docker.ContainerLogConfig(),
		SecurityOpt:    cfg.Docker.SecurityOptions(),
		ReadonlyRootfs: true,
		CapDrop:        []string{"ALL"},
		NetworkMode:    container.NetworkMode(networkName),
		CgroupnsMode:   container.CgroupnsModePrivate,
		Runtime:        cfg.Docker.Runtime,
		RestartPolicy: container.RestartPolicy{
			Name: "unless-stopped",
		},
	}

	if _, err := e.client.ContainerCreate(
		context.Background(),
		conf,
		hostConf,
		nil,
		nil,
		ingressContainerName(e.Id),
	); err != nil {
		return errors.Wrap(err, "environment/docker: failed to create ingress sidecar")
	}

	if err := e.client.ContainerStart(
		context.Background(),
		ingressContainerName(e.Id),
		container.StartOptions{},
	); err != nil {
		return errors.Wrap(err, "environment/docker: failed to start ingress sidecar")
	}

	return nil
}
