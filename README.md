# BastionRoute (v0.1.0-alpha)

> **An outbound-initiated WebSocket relay fabric for UDP datagram binary streams that operates with zero-inbound port architecture.**

## This is the official and original repository for BastionRoute maintained by klauscam

BastionRoute is a stateless, outbound-only UDP datagram binary stream relay fabric designed to route binary traffic over a stateful Layer-7 WebSocket transport.

By initiating all data pipelines via outbound-only connections, BastionRoute eliminates the concept of an internet-facing listening socket at your network edge. It provides deterministic routing of raw data streams between identified peers over an outbound WebSocket relay fabric, acting as a payload-agnostic transport layer.

---

## Core Intent & Design Philosophy

What is BastionRoute for?

BastionRoute is a specialized, stateless transport utility built to complement a strict zero-inbound listening port policy on your edge firewall while maintaining real-time remote access to private network resources (like native WireGuard).

It acts as low-level digital plumbing designed to plug seamlessly behind existing public web infrastructure (like Cloudflare Proxy, Nginx, Caddy, or Traefik). This lets you bridge network segments over untrusted public space without exposing your private infrastructure to the internet.

---

## Architectural Pillars

BastionRoute leverages a decoupled, multi-shim architecture that completely separates the data plane from the control plane to preserve payloads as-is.

   Attack Surface Reduction (Zero-Inbound): Traditional topologies require an edge gateway to "listen" on a public port. BastionRoute reverses this logic. Both your isolated Server Shim and remote Client Shim initiate standard outbound-only Layer-7 connections to an external, transient Relay Broker. Because your gateway firewall maintains zero listening ports, your network edge remains dark to internet scans.

  Deterministic "UDP Physics" Over TCP: Wrapping real-time UDP streams inside stateful TCP tunnels traditionally causes Head-of-Line blocking ("TCP Meltdown"). When a packet drops, standard proxies stall the pipeline to force a retransmission, introducing devastating latency spikes that break VPN handshakes. BastionRoute’s concurrent data engine fixes this by leveraging non-blocking Go channel selectors to explicitly drop frames at the application layer during network congestion. By perfectly emulating real-world, lossy UDP physics rather than backing up an internal queue, interactive streams stay completely fluid and lag-free.

   Blind Transport Brokerage (Zero-Trust): BastionRoute is entirely payload-agnostic and does not interpret application payload semantics. The external Relay functions entirely in transient memory, terminates no internal VPN encryption, and requires no cryptographic keys. Security remains entirely end-to-end at the application layer (e.g., WireGuard).

---

## 📦 Deployment Mechanics

### Prerequisites
* **Go 1.21+ compiler toolchain**
* `make` utility installed (standard on Linux/macOS)

### Installation & Compilation (Ubuntu)

BastionRoute utilizes a standard multi-binary `cmd/` architecture. The compilation step automatically leverages the `Makefile` to pull down required dependencies (including `github.com/gorilla/websocket`) and verify the Go environment. 

To download dependencies and compile all binaries into a localized execution folder simultaneously, run:

```
git clone https://github.com/klauscam/BastionRoute.git
cd BastionRoute
make
```

Once completed, both production-ready binaries will be available inside the local target execution directory:
* `bin/bastionroute-shim`
* `bin/bastionroute-relay`

To clean up build artifacts and purge compiled binaries from your workspace at any time, run:

```
make clean
```

---

## 🚀 Execution Guide

### Running the Relay
Deploy the relay binary on a web accessable server or localized DMZ boundary. This acts as a payload-agnostic relay broker that maintains transient routing state in memory. It is highly recommended to deploy this behind an edge reverse proxy (Nginx/Caddy) or Cloudflare Proxy:

```
./bin/bastionroute-relay --port=8080
```

### Running the Server-Side Control Plane
Run the shim in server mode behind your private infrastructure to establish the outbound control link to the public relay broker, pointing it to your local application (e.g., WireGuard listening locally on 51820):

```
./bastionroute-shim --wg-role=server --uri="wss://relay.yourdomain.com" --room="room-id" --wg-ip="127.0.0.1" --wg-port=51820
```

### Running the Client-Side User Pipeline
Run the shim in client mode on your remote device. The user-space supervisor loop will spawn a local UDP interface to bridge your native application:

```
./bastionroute-shim --wg-role=client --uri="wss://relay.yourdomain.com" --room="room-id" --peer-id="remote-peer-01" --wg-ip="127.0.0.1" --wg-port=51820
```

### OpenWrt
```bash
git clone https://github.com/klauscam/bastionroute.git
cd bastionroute
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bastionroute-shim
```

### Termux (Android)
```bash
git clone https://github.com/klauscam/bastionroute.git
cd bastionroute
CGO_ENABLED=0 GOOS=android GOARCH=arm64 go build -o bastionroute-shim
```
---

## Example Applications

Because BastionRoute acts strictly as low-level digital plumbing, it can transport any arbitrary UDP datagram binary stream between connected peers without any modification to the relay or shim source code:

* WireGuard VPN transport
* Generic UDP stream transport
* Binary message buses
* Telemetry and sensor streams
* Multiplayer game state synchronization
* Low-overhead file transfer pipelines

---

## Layered Security Model (WireGuard Example Deployment)

When deployed optimally behind production edge infrastructure, BastionRoute allows you to build a 3-Layer Defense-in-Depth perimeter over untrusted networks:

### Architectural Diagram

![Architectural Diagram](images/arch_diagram.png)

---

### 🚀 Performance Notes

#### Performance characteristics depend heavily on:
* underlying network conditions
* WebSocket implementation (e.g. TLS termination)
* MTU configuration
* relay latency and placement

### Observed Test Conditions (Example Setup)

#### The following results were observed under controlled testing conditions:

* High-latency mobile network (~165 ms RTT)
* Standard Linux kernel networking stack
* Single relay instance

#### UDP Throughput (through tunnel)
* Peak throughput: ~80 Mbps
* Jitter: ~0.145 ms

#### TCP Throughput (through tunnel)
* Sustained throughput: ~45–65 Mbps
* Stable under moderate packet loss conditions

>* Note: Results are workload and environment-dependent and are not guaranteed. iperf3 was used for benchmarking unless otherwise stated.

---

### 🛠️ Engine Configuration & Tuning Matrix

To achieve optimal performance across lossy or highly latent WAN links, the following operating system and network settings are natively utilized:

#### 1. Loopback MTU Stabilization Matrix
To prevent catastrophic packet fragmentation at physical gateway boundaries, the underlying virtual WireGuard interface must be clamped to account for encapsulation overhead:

$$\text{MTU} = 1280 \text{ bytes}$$ (recommended baseline)

#### 2. Linux TCP Optimization (Relay Node)
BBR congestion control may improve performance under high RTT conditions:

```bash
# Enable BBR Congestion Control on the host system
sudo sysctl -w net.core.default_qdisc=fq
sudo sysctl -w net.ipv4.tcp_congestion_control=bbr
```

## Threat Considerations & Notices

### Metadata Notice

This system does not provide network anonymity guarantees. Traffic metadata such as frame timing, transmission volume, and peer connection relationships remain observable at the transport layer by upstream network entities.

### Security Notice

Authentication, authorization, stateful firewall rules, and encryption remain the sole responsibility of the underlying applications utilizing the fabric. BastionRoute should always be paired with structurally secure end-to-end protocols like WireGuard.

### Experimental Status

This software is currently alpha-quality and should be thoroughly audited and evaluated in isolated test environments before consideration for production deployment.

---

## ⚠️ Legal & Usage Notice

BastionRoute is provided for legitimate network administration, research, and authorized deployment scenarios only.

Users are solely responsible for ensuring compliance with applicable laws, regulations, and organizational policies when deploying or using this software.

This software does not include mechanisms for enforcing usage restrictions and should not be deployed in environments where its use would violate applicable rules or agreements.

## License

This project is licensed under the Apache License 2.0.

The Apache License governs use, modification, and distribution of this software and includes its own limitation of liability and warranty disclaimers.

See the LICENSE file for full terms.
