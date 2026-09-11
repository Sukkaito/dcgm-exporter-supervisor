# DCGM Exporter Supervisor

A lightweight supervisor service written in Go to orchestrate and manage multiple [`dcgm-exporter`](https://github.com/NVIDIA/dcgm-exporter) instances for Virtual Machines on a host hypervisor.

---

## Motivation

In GPU virtualization setups (such as PCI GPU passthrough or vGPU on KVM/QEMU, Libvirt, or OpenStack Nova), the NVIDIA GPU driver and `nv-hostengine` run **inside each guest VM**.

However, NVIDIA's `dcgm-exporter` (via `go-dcgm` and `libdcgm.so`) only supports connecting to **a single host engine at a time per exporter process** (`dcgm.Init`). To monitor all VMs from the host machine without having to expose Prometheus scrape endpoints directly from every guest VM, the host must run a separate `dcgm-exporter` instance for each target VM.

`dcgm-exporter-supervisor` automates this entire lifecycle:
- **Dynamic VM Discovery**: Automatically detects running VMs with GPU assignments via Libvirt/KVM (default), OpenStack Nova API, static YAML configuration, or a directory of YAML files (`targets.d/`).
- **Child Process Supervision**: Spawns and supervises local `dcgm-exporter` child instances using **UNIX domain sockets** via native systemd socket activation (`--web-systemd-socket`), eliminating port collisions, port pool limits, and loopback socket exposure on the host.
- **Unified Prometheus Scraping (Strictly TCP/IP)**: The supervisor HTTP server listens strictly on TCP/IP (`:9400`), aggregates metrics across all child exporters into a single `/metrics` endpoint with injected `vm_name` and custom target labels, preventing metric collision.
- **Prometheus HTTP Service Discovery**: Exposes `/targets` formatted for Prometheus `http_sd_configs` to scrape per-target `/probe` endpoints.
- **Multi-Target Probing**: Exposes `/probe?target=<name>` for Blackbox-style Prometheus setups.
- **Source & Ecosystem Compatibility**: Built on the exact same Go stack and conventions as NVIDIA's `dcgm-exporter` (`urfave/cli/v2`, `gorilla/mux`, `prometheus/exporter-toolkit`, `log/slog`).

---

## Architecture

```
                        +------------------------------------+
                        |        Prometheus Server           |
                        +-----------------+------------------+
                                          |
                        Scrapes /metrics  | (TCP/IP HTTP :9400)
                                          v
+-----------------------------------------------------------------------------------+
| Host Machine: dcgm-exporter-supervisor                                            |
|                                                                                   |
|  +-----------------------------------------------------------------------------+  |
|  | HTTP Server (TCP/IP on :9400, exporter-toolkit/web)                         |  |
|  |  - /metrics: Aggregates & enriches metrics from all instances with VM labels|  |
|  |  - /targets: Prometheus HTTP Service Discovery (http_sd_configs)            |  |
|  |  - /probe?target=vm1: Multi-target scrape endpoint                          |  |
|  |  - /health: Supervisor & child instance health status                       |  |
|  +-------------------------------------+---------------------------------------+  |
|                                        |                                          |
|  +---------------------------+         | Concurrently scrapes                     |
|  | Target Discovery Manager  |         | UNIX domain sockets                      |
|  |  - Libvirt/KVM (Default)  |         v                                          |
|  |  - OpenStack Nova API     |   +-------------------+  +-------------------+     |
|  |  - Static Config (YAML)   |   | dcgm-exporter #1  |  | dcgm-exporter #2  | ... |
|  |  - File Watcher Provider  |   | (vm1.sock - FD 3) |  | (vm2.sock - FD 3) |     |
|  +-------------+-------------+   +---------+---------+  +---------+---------+     |
|                |                           |                      |               |
|                v                           |                      |               |
|  +---------------------------+             |                      |               |
|  | Process Lifecycle Manager |             |                      |               |
|  |  - Socket Activation (FD3)|             |                      |               |
|  |  - Process Supervision    |             |                      |               |
|  |  - Crash Backoff Restart  |             |                      |               |
|  +---------------------------+             |                      |               |
+--------------------------------------------|----------------------|---------------+
                                             |                      |
                    vsock://3:5555 or        |                      | vsock://4:5555 or
                    tcp://192.168.122.10:5555|                      | unix:///var/run/vm2.sock
                                             v                      v
                                    +-----------------+    +-----------------+
                                    | VM 1 (Guest)    |    | VM 2 (Guest)    |
                                    | nv-hostengine   |    | nv-hostengine   |
                                    | NVIDIA Driver   |    | NVIDIA Driver   |
                                    | GPU 0 (vfio)    |    | GPU 1 (vfio)    |
                                    +-----------------+    +-----------------+
```

---

## Features

### 1. Discovery Providers
- **Libvirt/KVM Provider (Default)**: Inspects running domains via `qemu:///system`. Detects PCI GPU passthrough or mediated device (mdev vGPU) devices. Automatically extracts the guest VSOCK CID (`vsock://<CID>:5555`) or guest IP address (`tcp://<IP>:5555`).
- **OpenStack Nova API Provider**: Discovers active instances scheduled on the hypervisor host via OpenStack Nova compute API and Keystone authentication.
- **Static Configuration**: Directly define target VMs and endpoints in `config.yaml`.
- **File Watcher (`targets.d/`)**: Watches a directory for target definition YAML files, enabling external automation or GitOps without restarting the supervisor.

### 2. Protocol-Agnostic Host Engine Connections
Supports all DCGM remote connection formats:
- `vsock://<CID>:<PORT>` (e.g. `vsock://3:5555` - direct kernel VM socket, zero network overhead)
- `tcp://<IPv4>:<PORT>` (e.g. `tcp://192.168.122.10:5555`)
- `tcp://[<IPv6>]:<PORT>` (e.g. `tcp://[2001:db8::10]:5555` - Global Unicast IPv6)
- `tcp://[<IPv6>%<iface>]:<PORT>` (e.g. `tcp://[fe80::5054:ff:fe12:3456%vnet0]:5555` - Scoped Link-Local IPv6)
- `unix:///<PATH>` (e.g. `unix:///var/run/hostengine.sock`)

### 3. Comprehensive IPv6 Support
`dcgm-exporter-supervisor` follows NVIDIA `dcgm-exporter`'s IPv6 rules:
- **Bracket Notation**: All IPv6 host engine addresses with a port must use bracket notation (e.g. `[2001:db8::1]:5555` or `tcp://[2001:db8::1]:5555`). The supervisor automatically normalizes and validates brackets.
- **Link-Local Addresses (LLA) with Interface Scope**: Fully supported! When Libvirt discovers guest IPv6 link-local addresses (`fe80::/10`), it automatically appends the host's virtual interface name (e.g., `%vnet0`), producing `tcp://[fe80::5054:ff:fe12:3456%vnet0]:5555`.
- **Configurable Child Exporter Bind Host**: Child exporter instances bind to IPv4 localhost (`127.0.0.1`) by default, and can be switched to IPv6 localhost (`::1` or `[::1]`) via `--exporter-listen-host` or `exporter.listen_host`.
- **Configurable Discovery Preference**: Discovery providers (Libvirt and Nova) support `ip_version`:
  - `"ipv4"` (default): Discovers and prefers IPv4 addresses.
  - `"ipv6"`: Discovers IPv6 addresses (prefers global unicast, falls back to scoped LLA).
  - `"auto"`: Prioritizes IPv4, falling back to global IPv6 and scoped LLA IPv6.

### 4. Multi-Tenant Network Namespace (`netns`) Isolation
In cloud and virtualized environments (such as OpenStack Neutron, Kubernetes CNI, or KVM with tenant overlay networks), virtual machines belonging to different tenants often reside in isolated Linux network namespaces (e.g., `qrouter-<router-uuid>`, `tenant-<id>`, or custom namespace paths):
- **Per-Tenant Network Isolation**: Child `dcgm-exporter` instances for VMs of the same tenant are spawned inside the **same tenant network namespace**, while different tenants remain strictly isolated from one another.
- **Zero Port Allocation & Overlapping IPs**: Even if multiple tenants use the exact same private IP space (e.g., `10.0.0.5:5555`), child exporters communicate back to the supervisor via host filesystem UNIX domain sockets (`/run/dcgm-exporter-supervisor/<id>.sock`), entirely avoiding port collisions or port exhaustion.
- **Unified Resolution Across All Discovery Modes**:
  1. **Explicit Target Override**: `Target.NetNS` if set directly in static YAML or file target.
  2. **Tenant Match**: Discovered tenant identity (Nova `tenant_id`, Libvirt OpenStack metadata, or target `tenant:`) matched against `netns.items[].tenants`.
  3. **Dynamic Template**: `netns.template: "qrouter-{{.Tenant}}"`.
  4. **Subnet / CIDR Match**: Target endpoint IP tested against `netns.items[].subnets` (IPv4 & IPv6).
  5. **Auto-Detect Route**: Optional `netns.auto_detect_route: true` probes routing reachability via `ip route get <ip>`.
  6. **Fallback Default**: `netns.default_netns` or root namespace.
- **Metric Label Enrichment**: Injects `tenant="<name_or_uuid>"` and `netns="<namespace>"` labels into Prometheus metrics and `/targets`.
- **System Capabilities**: Running exporters inside another network namespace requires `CAP_NET_ADMIN` and `CAP_SYS_ADMIN` (or running as `root`).

### 5. Prometheus Scrape Methods

#### Method A: Unified Scrape (`GET /metrics`)
Prometheus scrapes a single endpoint on the supervisor. The supervisor fans out requests across all running instances concurrently, rewrites each metric line to inject `vm_name="<name>"`, `target_id="<id>"`, and custom labels, and streams the merged output.

```yaml
# prometheus.yml
scrape_configs:
  - job_name: 'gpu-vms'
    scrape_interval: 15s
    static_configs:
      - targets: ['hypervisor-host:9400']
```

#### Method B: Prometheus HTTP Service Discovery (`GET /targets`)
Allows Prometheus to discover and scrape each child instance directly using `http_sd_configs`:

```yaml
# prometheus.yml
scrape_configs:
  - job_name: 'gpu-vms-sd'
    http_sd_configs:
      - url: 'http://hypervisor-host:9400/targets'
        refresh_interval: 30s
```

#### Method C: Multi-Target Probe (`GET /probe?target=<vm_name>`)
For environments using Prometheus relabeling and the blackbox/probe pattern:

```yaml
# prometheus.yml
scrape_configs:
  - job_name: 'gpu-vm-probe'
    metrics_path: /probe
    params:
      target: ['vm-ai-01']
    static_configs:
      - targets: ['hypervisor-host:9400']
```

---

## Installation & Build

### Prerequisites
- Go 1.22+
- `dcgm-exporter` binary installed on the host (e.g. `/usr/bin/dcgm-exporter`)

### Building from Source
```bash
git clone https://github.com/Sukkaito/dcgm-exporter-supervisor.git
cd dcgm-exporter-supervisor
go build -o bin/dcgm-exporter-supervisor ./cmd/dcgm-exporter-supervisor
```

---

## Configuration

See [`config.example.yaml`](config.example.yaml) for full configuration options.

```yaml
# /etc/dcgm-exporter-supervisor/config.yaml
address: ":9400"
collect_interval: 30000
collectors_file: "/etc/dcgm-exporter/default-counters.csv"
log_format: "text"

exporter:
  binary_path: "/usr/bin/dcgm-exporter"
  socket_dir: "/run/dcgm-exporter-supervisor"

discovery:
  static:
    enabled: true
    targets:
      - name: "vm-ai-01"
        endpoint: "vsock://3:5555"
        labels:
          environment: "production"

  libvirt:
    enabled: true
    uri: "qemu:///system"
    poll_interval: 15s
    connection_mode: "auto"
    filter_gpu_only: true
```

### Running the Supervisor

```bash
./bin/dcgm-exporter-supervisor --config-file=/etc/dcgm-exporter-supervisor/config.yaml
```

---

## CLI Options

| Flag | Env Var | Default | Description |
| :--- | :--- | :--- | :--- |
| `--config-file` | `DCGM_SUPERVISOR_CONFIG_FILE` | `""` | Path to YAML config file |
| `-a, --address` | `DCGM_SUPERVISOR_LISTEN` | `":9400"` | Supervisor HTTP listen address as `<HOST>:<PORT>` or `"[<IPv6>]:<PORT>"` (e.g. `"[::]:9400"`) |
| `-c, --collect-interval` | `DCGM_SUPERVISOR_INTERVAL` | `30000` | Child collection interval (ms) |
| `-f, --collectors` | `DCGM_SUPERVISOR_COLLECTORS` | `/etc/dcgm-exporter/default-counters.csv` | DCGM fields CSV counters file |
| `--dcgm-exporter-bin` | `DCGM_EXPORTER_BINARY` | `"dcgm-exporter"` | Path or command for dcgm-exporter binary |
| `--socket-dir` | `DCGM_SUPERVISOR_SOCKET_DIR` | `"/run/dcgm-exporter-supervisor"` | Directory for internal child UNIX domain sockets |
| `--web-config-file` | `DCGM_SUPERVISOR_WEB_CONFIG_FILE` | `""` | Exporter-toolkit web config for TLS/auth |
| `--log-format` | `DCGM_SUPERVISOR_LOG_FORMAT` | `"text"` | Log format (`text` or `json`) |
| `--debug` | `DCGM_SUPERVISOR_DEBUG` | `false` | Enable verbose debug logging |
| `--shutdown-timeout` | `DCGM_SUPERVISOR_SHUTDOWN_TIMEOUT` | `5s` | Timeout before SIGKILL is sent |
| `--netns-default` | `DCGM_SUPERVISOR_NETNS_DEFAULT` | `""` | Default fallback network namespace |
| `--netns-template` | `DCGM_SUPERVISOR_NETNS_TEMPLATE` | `""` | Dynamic template for tenant network namespace (e.g. `"qrouter-{{.Tenant}}"`) |

---

## Running as a Systemd Service

1. Copy binary:
   ```bash
   sudo cp bin/dcgm-exporter-supervisor /usr/local/bin/
   ```
2. Copy configuration:
   ```bash
   sudo mkdir -p /etc/dcgm-exporter-supervisor
   sudo cp config.example.yaml /etc/dcgm-exporter-supervisor/config.yaml
   ```
3. Install systemd service:
   ```bash
   sudo cp systemd/dcgm-exporter-supervisor.service /etc/systemd/system/
   sudo systemctl daemon-reload
   sudo systemctl enable --now dcgm-exporter-supervisor
   ```

---

## License

Apache License 2.0. See LICENSE for details.

