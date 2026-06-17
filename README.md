# BastionRoute (v0.1.0-alpha)

> **An outbound-initiated WebSocket relay fabric for UDP datagram binary streams that operates with zero-inbound port architecture.**

## This is the official and original repository for BastionRoute maintained by klauscam")

BastionRoute is an outbound-only UDP datagram binary stream relay fabric designed to route binary traffic over a stateful Layer-7 WebSocket transport.

By initiating all data pipelines via outbound-only websocket connections, BastionRoute requires no open port exposure and does not interpret payload semantics. It only provides deterministic routing of data streams between identified peers over an outbound WebSocket relay fabric. BastionRoute is a transport-agnostic relay fabric for routing binary streams between outbound-connected peers. BastionRoute is resposible for a single function: routing UDP datagram binary streams between outbound-connected peers over WebSocket connections via a web accessable relay.

---

## ⚡ Architectural Core

BastionRoute leverages a decoupled, multi-shim architecture that separates the data plane from the control plane to preserve payload as-is.

* **Zero-Inbound Footprint:** The target server establishes a persistent, outbound-initiated WebSocket control link to a Relay. It does not require inbound ports under normal deployment configurations.
* **Double-Wrapper Encapsulation:** The binary payload is transparently ingested by the shim, packed into Layer-7 WebSockets frames (the use of TLS via nginx or other reverse proxies is highly recommended). The payload is never altered. 
* **Relay Brokerage:** The relay functions as a payload-agnostic relay broker and does not interpret application payload semantics. It routes traffic using room and peer identifiers established during connection setup and maintained in transient memory structures. The payload contents injested in the architecture, remain unaltered throughout its lifecycle.

BastionRoute intentionally does not define encryption, authentication, authorization, payload schemas, or application semantics. These responsibilities remain with the applications utilizing the relay fabric.
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
Deploy the relay binary on a web accessable server or localized DMZ boundary. This acts as a payload-agnostic relay broker that maintains transient routing state in memory:

```
./bin/bastionroute-relay --port=8080
```

### Running the Server-Side Control Plane
Run the shim in server mode behind your private infrastructure to establish the outbound control link to the public relay broker:

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

BastionRoute is payload agnostic and can transport arbitrary UDP datagram binary streams between connected peers.

Potential applications include:

* WireGuard VPN transport
* Generic UDP stream transport
* Binary message buses
* RPC transports
* Telemetry and sensor streams
* Multiplayer game state synchronization
* File transfer pipelines
* Custom application protocols

No relay or shim modifications are required.

---

## (Usage Example) WireGuard Network over BastionRoute

---

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

>* Note: Results are workload and environment-dependent and are not guaranteed. iperf3 was used for benchmarking unless otherwise stated

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

### Security Model (for WireGuard example)

#### BastionRoute inherits security properties from WireGuard. Specifically:

* Payload encryption is handled entirely by WireGuard
* BastionRoute does not decrypt or inspect payload data
* Relay nodes do not require access to cryptographic keys

### Threat Considerations

This system does not provide anonymity guarantees. Traffic metadata such as timing, volume, and connection relationships may still be observable at the transport layer.

---

## Security Notice

BastionRoute provides transport relaying of UDP datagram binary streams over WebSocket using outbound initiated connections.
Authentication, authorization, encryption, and access control remain the responsibility of the underlying data initiator configuration and deployment.

## Experimental Status

This software is currently alpha-quality software and should be evaluated thoroughly before production deployment.

## ⚠️ Legal & Usage Notice

BastionRoute is provided for legitimate network administration, research, and authorized deployment scenarios only.

Users are solely responsible for ensuring compliance with applicable laws, regulations, and organizational policies when deploying or using this software.

This software does not include mechanisms for enforcing usage restrictions and should not be deployed in environments where its use would violate applicable rules or agreements.

## License

This project is licensed under the Apache License 2.0.

The Apache License governs use, modification, and distribution of this software and includes its own limitation of liability and warranty disclaimers.

See the LICENSE file for full terms.
