# Secure Deployment Guide: gVisor + Wings on Shared Nodes

This guide describes the supported secure deployment model for this fork of Wings when you need to run untrusted tenant workloads on shared nodes.

The model is:

- Docker Engine runs on the host.
- gVisor (`runsc`) is installed on the host and registered as a Docker runtime.
- Wings runs directly on the host as a systemd service.
- Wings launches tenant workloads and installer jobs with the `runsc` runtime.
- Tenant workloads use per-server bridge networks and explicit published ports.

This is intentionally different from the containerized `docker-compose.example.yml` deployment. That example is not suitable for secure multi-tenant use.

## Security Model

This repository now enforces the following tenant workload restrictions:

- Tenant containers and installer containers use the configured Docker runtime, which defaults to `runsc`.
- Tenant containers use read-only root filesystems, `no-new-privileges`, `cap_drop=ALL`, and a private cgroup namespace.
- Tenant images may use tags or digests. Digest pinning is recommended for provenance and reproducibility, but is not required for sandboxing.
- Custom host mounts are rejected.
- `force_outgoing_ip` is rejected.
- Tenant-provided runtime labels are ignored.

If `runsc` is not registered with Docker, Wings fails to start.

## Host Topology

Use this topology for secure shared-node deployments:

- Linux host
- Docker Engine installed directly on the host
- Wings binary installed directly on the host
- systemd managing both Docker and Wings

Do not use this topology for secure shared-node deployments:

- Wings inside Docker with `/var/run/docker.sock` mounted
- Shared tenant bridge networks
- Host networking or `macvlan` for tenant workloads

## Prerequisites

- Linux host with `systemd`
- Docker Engine installed from the official Docker Engine instructions for your distro
- Root access on the host
- Network connectivity from the node to your Pelican Panel

Official references:

- Docker Engine install: <https://docs.docker.com/engine/install/>
- gVisor installation: <https://gvisor.dev/docs/user_guide/install/>
- gVisor Docker quick start: <https://gvisor.dev/docs/user_guide/quick_start/docker/>
- gVisor platform guidance: <https://gvisor.dev/docs/user_guide/platforms/>
- gVisor production guide: <https://gvisor.dev/docs/user_guide/production/>

## Step 1: Install Docker Engine

Install Docker Engine on the host using the official Docker documentation for your Linux distribution:

- <https://docs.docker.com/engine/install/>

After installation, verify Docker is healthy:

```bash
sudo systemctl enable --now docker
sudo docker version
sudo docker info
```

## Step 2: Install gVisor (`runsc`)

This section uses the `apt` repository path published by the gVisor project for Debian and Ubuntu hosts.

Install prerequisites:

```bash
sudo apt-get update
sudo apt-get install -y apt-transport-https ca-certificates curl gnupg
```

Add the gVisor package repository:

```bash
curl -fsSL https://gvisor.dev/archive.key \
  | sudo gpg --dearmor -o /usr/share/keyrings/gvisor-archive-keyring.gpg

echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/gvisor-archive-keyring.gpg] https://storage.googleapis.com/gvisor/releases release main" \
  | sudo tee /etc/apt/sources.list.d/gvisor.list >/dev/null
```

Install `runsc`:

```bash
sudo apt-get update
sudo apt-get install -y runsc
```

Verify the binary is present:

```bash
runsc --version
command -v runsc
```

If you are not on an `apt`-based distro, use the manual install flow from the official gVisor installation guide:

- <https://gvisor.dev/docs/user_guide/install/>

## Step 3: Choose the gVisor Platform

Use the platform that matches your node type:

- Bare-metal shared nodes: use `kvm`
- Virtual-machine shared nodes: use `systrap`

The gVisor documentation recommends `kvm` on bare metal for best performance, and recommends `systrap` instead of `kvm` inside VMs unless you have a strong reason to use nested virtualization.

### KVM checks for bare-metal hosts

Before using `kvm`, verify that `/dev/kvm` exists and is accessible:

```bash
ls -l /dev/kvm
groups | grep -qw kvm && echo "kvm group present"
```

If `/dev/kvm` is missing, enable virtualization in the host BIOS/firmware and confirm the KVM kernel modules are available.

## Step 4: Register `runsc` with Docker

Wings selects the runtime per container, so you do not need to make `runsc` Docker's default runtime. Register the runtime under the name `runsc` and leave the Docker default runtime unchanged.

### Preferred: use `runsc install`

For bare-metal hosts:

```bash
sudo runsc install -- --platform=kvm
sudo systemctl restart docker
```

For VM-based hosts:

```bash
sudo runsc install -- --platform=systrap
sudo systemctl restart docker
```

### Manual `daemon.json` equivalent

If you manage `/etc/docker/daemon.json` yourself, use one of these runtime definitions and then restart Docker.

Bare-metal example:

```json
{
  "runtimes": {
    "runsc": {
      "path": "/usr/bin/runsc",
      "runtimeArgs": [
        "--platform=kvm"
      ]
    }
  }
}
```

VM example:

```json
{
  "runtimes": {
    "runsc": {
      "path": "/usr/bin/runsc",
      "runtimeArgs": [
        "--platform=systrap"
      ]
    }
  }
}
```

Restart Docker after editing `daemon.json`:

```bash
sudo systemctl restart docker
```

## Step 5: Verify Docker Can Launch gVisor Containers

Check that Docker sees the runtime:

```bash
sudo docker info --format '{{json .Runtimes}}'
```

Run a test container with `runsc`:

```bash
sudo docker run --rm --runtime=runsc hello-world
```

Optional compatibility check:

```bash
sudo docker run --rm --runtime=runsc -it ubuntu dmesg
```

If Docker reports that the runtime is unknown, the runtime registration step did not complete successfully.

## Step 6: Install Wings on the Host

Download the Wings binary and place it on the host:

```bash
sudo curl -L https://github.com/pelican-dev/wings/releases/latest/download/wings_linux_amd64 \
  -o /usr/local/bin/wings
sudo chmod +x /usr/local/bin/wings
sudo mkdir -p /etc/pelican
```

Generate or place the node configuration:

```bash
sudo /usr/local/bin/wings configure --config-path /etc/pelican/config.yml
```

Or place the node configuration from the Panel at:

```text
/etc/pelican/config.yml
```

## Step 7: Configure Wings for Secure Shared-Node Operation

Start with the Panel-generated config and then set the secure runtime fields explicitly.

Minimum secure runtime settings:

```yaml
docker:
  runtime: runsc
  apparmor_profile: docker-default
  seccomp_profile: ""
  tmpfs_size: 100
  container_pid_limit: 512
  network:
    dns:
      - 1.1.1.1
      - 1.0.0.1
    network_mtu: 1500
```

Recommended system settings:

```yaml
system:
  root_directory: /var/lib/pelican
  log_directory: /var/log/pelican
  data: /var/lib/pelican/volumes
  archive_directory: /var/lib/pelican/archives
  backup_directory: /var/lib/pelican/backups
  tmp_directory: /tmp/pelican
```

Important deployment notes:

- Keep `docker.runtime` set to `runsc`.
- Do not run Wings inside Docker for this deployment model.
- Do not rely on `allowed_mounts` for tenant workloads. Secure mode rejects custom server mounts.
- Do not use `force_outgoing_ip`.
- Digest pinning is recommended if you want reproducible image selection, but it is not required by Wings.

## Step 8: Install the systemd Service

Create `/etc/systemd/system/wings.service`:

```ini
[Unit]
Description=Pelican Wings Daemon
After=docker.service
Requires=docker.service
PartOf=docker.service

[Service]
User=root
WorkingDirectory=/etc/pelican
LimitNOFILE=4096
PIDFile=/var/run/wings/daemon.pid
ExecStart=/usr/local/bin/wings --config /etc/pelican/config.yml
Restart=on-failure
StartLimitInterval=180
StartLimitBurst=30
RestartSec=5s

[Install]
WantedBy=multi-user.target
```

Enable and start Wings:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now wings
sudo systemctl status wings
```

## Step 9: Configure Tenant Workloads Correctly

Your server definitions must follow the secure runtime rules now enforced by the code:

- Use a valid registry image reference:
  - `ghcr.io/example/server:stable`
  - `ghcr.io/example/server@sha256:...`
- Do not configure custom server mounts.
- Do not configure `force_outgoing_ip`.
- Expect each tenant workload to run on its own Docker bridge network.

If a tenant definition violates those rules, Wings rejects it when syncing configuration.

## Step 10: Verify Wings Is Using gVisor

Check the Wings service logs:

```bash
sudo journalctl -u wings -f
```

Run Wings diagnostics:

```bash
sudo /usr/local/bin/wings diagnostics
```

After starting a server through the Panel, verify the runtime and network directly from Docker:

```bash
sudo docker inspect <container_id> --format '{{.HostConfig.Runtime}}'
sudo docker inspect <container_id> --format '{{.HostConfig.NetworkMode}}'
```

Expected results:

- Runtime: `runsc`
- Network mode: a per-server bridge such as `pelican-<server_uuid>`

## Troubleshooting

### `required runtime "runsc" is not available on this node`

Docker does not see the `runsc` runtime. Re-run the runtime registration step and restart Docker.

### `hello-world` works with Docker but not with `--runtime=runsc`

This usually means the runtime registration or platform selection is wrong. Confirm:

- `docker info --format '{{json .Runtimes}}'`
- `runsc --version`
- `journalctl -u docker -n 100`

### `kvm` is slow or unstable inside a VM

Use `systrap` instead. gVisor recommends `systrap` in virtualized environments unless you have a deliberate nested virtualization setup.

### A tenant image starts with `runc` elsewhere but fails under `runsc`

That is usually an application compatibility issue with gVisor. Keep the secure deployment model fail-closed and fix the image or workload profile rather than falling back to `runc`.

## Operational Checklist

- Docker Engine installed on the host
- `runsc` installed on the host
- `runsc` registered with Docker
- Docker restarted successfully
- `docker run --runtime=runsc hello-world` succeeds
- Wings installed on the host, not in Docker
- `docker.runtime: runsc` set in `/etc/pelican/config.yml`
- Tenant image references reviewed for your own provenance requirements
- No custom server mounts
- Wings starts successfully and `wings diagnostics` is clean
