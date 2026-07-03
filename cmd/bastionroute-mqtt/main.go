package main
 
import (
        "context"
        "crypto/aes"
        "crypto/cipher"
        "crypto/hmac"
        "crypto/rand"
        "crypto/sha256"
        "encoding/hex"
        "flag"
        "fmt"
        "io"
        "log"
        "net"
        "os"
        "os/signal"
        "strconv"
        "strings"
        "sync"
        "sync/atomic"
        "syscall"
        "time"

        mqtt "github.com/eclipse/paho.mqtt.golang"
)

const (
        WindowSec   = 300 // 5-minute rolling window for topic blindness
        DriftBuffer = 30  // 30-second boundary dual-publish window
        PeerTimeout = 45  // Seconds before pruning an inactive connection
        ClockSkew   = 30  // Allowed clock skew in seconds for replay protection
)

var (
        activePeers     sync.Map // Map[string]*PeerTracker
        handledMessages sync.Map // Map[string]time.Time (Replay Cache Window Matrix)
)

type PeerTracker struct {
        Cancel     context.CancelFunc
        LastSeenAm int64 // Atomic Unix timestamp
}

// ============================================================================
// 
// ============================================================================

func RandomClientID(prefix string) string {
        b := make([]byte, 12)
        if _, err := io.ReadFull(rand.Reader, b); err != nil {
                return fmt.Sprintf("%s-fallback-%d", prefix, time.Now().UnixNano())
        }
        return fmt.Sprintf("%s-%s", prefix, hex.EncodeToString(b))
}

func CheckAndRecordReplay(nonceKey string) bool {
        now := time.Now()
        if _, loaded := handledMessages.LoadOrStore(nonceKey, now); loaded {
                return true
        }
        return false
}

func StartReplayJanitor(ctx context.Context) {
        ticker := time.NewTicker(10 * time.Second)
        defer ticker.Stop()
        for {
                select {
                case <-ctx.Done():
                        return
                case <-ticker.C:
                        now := time.Now()
                        handledMessages.Range(func(key, value interface{}) bool {
                                t := value.(time.Time)
                                if now.Sub(t) > (time.Duration(ClockSkew) * time.Second) {
                                        handledMessages.Delete(key)
                                }
                                return true
                        })
                }
        }
}

// GenerateTopic outputs a completely flat 32-character root-level hex string
func GenerateTopic(base, secret []byte, suffix string, timestamp int64) string {
        timeBlock := timestamp / WindowSec
        h := hmac.New(sha256.New, secret)
        h.Write([]byte(fmt.Sprintf("%s-%d-%s", string(base), timeBlock, suffix)))
        return hex.EncodeToString(h.Sum(nil))[:32]
}

func EncryptGCM(plainText []byte, secret []byte) ([]byte, error) {
        key := sha256.Sum256(secret)
        block, err := aes.NewCipher(key[:])
        if err != nil {
                return nil, err
        }
        gcm, err := cipher.NewGCM(block)
        if err != nil {
                return nil, err
        }
        nonce := make([]byte, gcm.NonceSize())
        if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
                return nil, err
        }
        return gcm.Seal(nonce, nonce, plainText, nil), nil
}

func DecryptGCM(cipherText []byte, secret []byte) ([]byte, error) {
        key := sha256.Sum256(secret)
        block, err := aes.NewCipher(key[:])
        if err != nil {
                return nil, err
        }
        gcm, err := cipher.NewGCM(block)
        if err != nil {
                return nil, err
        }
        ns := gcm.NonceSize()
        if len(cipherText) < ns {
                return nil, fmt.Errorf("ciphertext payload too short")
        }
        nonce, actualCipher := cipherText[:ns], cipherText[ns:]
        return gcm.Open(nil, nonce, actualCipher, nil)
}

// ============================================================================
//
// ============================================================================

func runServerPeerPipeline(ctx context.Context, tracker *PeerTracker, brokerURL, room, peerID string, secret []byte, wgAddr string) {
        log.Printf("[TUNNEL-SERVER][%s] Spawning isolated crypto datapath...", peerID[:12])

        rAddr, err := net.ResolveUDPAddr("udp", wgAddr)
        if err != nil {
                log.Printf("[UDP ERROR][%s] Failed to resolve target WireGuard core address: %v", peerID[:12], err)
                return
        }

        udpConn, err := net.DialUDP("udp", nil, rAddr)
        if err != nil {
                log.Printf("[UDP ERROR][%s] Failed to allocate kernel socket: %v", peerID[:12], err)
                return
        }
        defer udpConn.Close()

        opts := mqtt.NewClientOptions().AddBroker(brokerURL)
        opts.SetClientID(RandomClientID("br-srv"))
        opts.SetCleanSession(true)
        opts.SetOrderMatters(false)
        opts.SetAutoReconnect(true)
        opts.SetStore(mqtt.NewMemoryStore())

        outboundChan := make(chan []byte, 4096)
        var activeSubs sync.Map

        syncPeerSubs := func(client mqtt.Client) {
                now := time.Now().Unix()
                targets := map[string]bool{
                        GenerateTopic([]byte(peerID), secret, "up", now):           true,
                        GenerateTopic([]byte(peerID), secret, "up", now+WindowSec): true,
                }

                for topic := range targets {
                        if _, exists := activeSubs.Load(topic); !exists {
                                token := client.Subscribe(topic, 0, func(c mqtt.Client, m mqtt.Message) {
                                        atomic.StoreInt64(&tracker.LastSeenAm, time.Now().Unix())
                                        _, _ = udpConn.Write(m.Payload())
                                })
                                if token.WaitTimeout(time.Second*2) && token.Error() == nil {
                                        activeSubs.Store(topic, true)
                                }
                        }
                }

                activeSubs.Range(func(key, value interface{}) bool {
                        topic := key.(string)
                        if !targets[topic] {
                                client.Unsubscribe(topic)
                                activeSubs.Delete(topic)
                        }
                        return true
                })
        }

        opts.SetOnConnectHandler(func(client mqtt.Client) {
                syncPeerSubs(client)
        })

        mqttClient := mqtt.NewClient(opts)
        if token := mqttClient.Connect(); token.Wait() && token.Error() != nil {
                log.Printf("[MQTT ERROR][%s] Data worker connection failed: %v", peerID[:12], token.Error())
                return
        }
        defer mqttClient.Disconnect(250)

        go func() {
                <-ctx.Done()
                udpConn.Close()
        }()

        // LOOP 2: Fixed using Read() for connected UDP sockets
        go func() {
                buf := make([]byte, 2048)
                for {
                        n, err := udpConn.Read(buf)
                        if err != nil {
                                return
                        }
                        select {
                        case outboundChan <- append([]byte(nil), buf[:n]...):
                        default:
                        }
                }
        }()

        go func() {
                for {
                        select {
                        case <-ctx.Done():
                                return
                        case packet := <-outboundChan:
                                if !mqttClient.IsConnected() {
                                        continue
                                }
                                now := time.Now().Unix()
                                mqttClient.Publish(GenerateTopic([]byte(peerID), secret, "down", now), 0, false, packet)
                                if (now % WindowSec) > (WindowSec - DriftBuffer) {
                                        mqttClient.Publish(GenerateTopic([]byte(peerID), secret, "down", now+WindowSec), 0, false, packet)
                                }
                        }
                }
        }()

        ticker := time.NewTicker(5 * time.Second)
        defer ticker.Stop()
        for {
                select {
                case <-ctx.Done():
                        log.Printf("[TUNNEL-SERVER][%s] Route context closed cleanly.", peerID[:12])
                        return
                case <-ticker.C:
                        if mqttClient.IsConnected() {
                                syncPeerSubs(mqttClient)
                        }
                }
        }
}

func runServerControlPlane(ctx context.Context, brokerURL, room string, secret []byte, wgAddr string) {
        opts := mqtt.NewClientOptions().AddBroker(brokerURL)
        opts.SetClientID(RandomClientID("br-ctrl"))
        opts.SetCleanSession(true)
        opts.SetAutoReconnect(true)
        opts.SetStore(mqtt.NewMemoryStore())

        var activeCtrlSubs sync.Map

        syncControlSubs := func(client mqtt.Client) {
                now := time.Now().Unix()
                targets := map[string]bool{
                        GenerateTopic([]byte(room), secret, "control", now):           true,
                        GenerateTopic([]byte(room), secret, "control", now+WindowSec): true,
                }

                for topic := range targets {
                        if _, exists := activeCtrlSubs.Load(topic); !exists {
                                token := client.Subscribe(topic, 0, func(c mqtt.Client, m mqtt.Message) {
                                        decrypted, err := DecryptGCM(m.Payload(), secret)
                                        if err != nil {
                                                return // Drop unauthenticated payloads quietly
                                        }

                                        parts := strings.Split(string(decrypted), " ")
                                        if len(parts) != 4 {
                                                return
                                        }
                                        tsStr, msgNonce, event, peerID := parts[0], parts[1], parts[2], parts[3]

                                        replayKey := fmt.Sprintf("%s:%s", peerID, msgNonce)
                                        if CheckAndRecordReplay(replayKey) {
                                                log.Printf("[SECURITY WARNING] Intercepted replayed token: %s", replayKey)
                                                return
                                        }

                                        msgTime, err := strconv.ParseInt(tsStr, 10, 64)
                                        if err != nil {
                                                return
                                        }

                                        nowTime := time.Now().Unix()
                                        timeDiff := nowTime - msgTime
                                        if timeDiff < -ClockSkew || timeDiff > ClockSkew {
                                                log.Printf("[SECURITY ALERT] Out-of-sync control message from peer [%s]. Diff: %ds", peerID[:12], timeDiff)
                                                return
                                        }

                                        if event == "peer_connected" {
                                                peerCtx, peerCancel := context.WithCancel(ctx)
                                                newTracker := &PeerTracker{
                                                        Cancel:     peerCancel,
                                                        LastSeenAm: nowTime,
                                                }

                                                actual, loaded := activePeers.LoadOrStore(peerID, newTracker)
                                                if loaded {
                                                        tracker := actual.(*PeerTracker)
                                                        atomic.StoreInt64(&tracker.LastSeenAm, nowTime)
                                                        peerCancel()
                                                } else {
                                                        go func(pID string, pCtx context.Context, trk *PeerTracker) {
                                                                runServerPeerPipeline(pCtx, trk, brokerURL, room, pID, secret, wgAddr)
                                                                activePeers.Delete(pID)
                                                        }(peerID, peerCtx, newTracker)
                                                }
                                        }
                                })
                                if token.WaitTimeout(time.Second*2) && token.Error() == nil {
                                        activeCtrlSubs.Store(topic, true)
                                }
                        }
                }

                activeCtrlSubs.Range(func(key, value interface{}) bool {
                        topic := key.(string)
                        if !targets[topic] {
                                client.Unsubscribe(topic)
                                activeCtrlSubs.Delete(topic)
                        }
                        return true
                })
        }

        opts.SetOnConnectHandler(func(client mqtt.Client) {
                log.Println("[CONTROL] Encrypted orchestration plane attached. Awaiting secured signals...")
                syncControlSubs(client)
        })

        client := mqtt.NewClient(opts)
        if token := client.Connect(); token.Wait() && token.Error() != nil {
                log.Fatalf("[CONTROL FATAL] Connection failed: %v", token.Error())
        }

        reaperTicker := time.NewTicker(5 * time.Second)
        defer reaperTicker.Stop()

        for {
                select {
                case <-ctx.Done():
                        client.Disconnect(250)
                        activePeers.Range(func(key, value interface{}) bool {
                                value.(*PeerTracker).Cancel()
                                return true
                        })
                        return
                case <-reaperTicker.C:
                        nowTime := time.Now().Unix()
                        activePeers.Range(func(key, value interface{}) bool {
                                peerID := key.(string)
                                tracker := value.(*PeerTracker)
                                lastSeen := atomic.LoadInt64(&tracker.LastSeenAm)

                                if nowTime-lastSeen > PeerTimeout {
                                        log.Printf("[GC REAPER] Inactivity drop for client [%s]. Cleared.", peerID[:12])
                                        tracker.Cancel()
                                        activePeers.Delete(peerID)
                                }
                                return true
                        })

                        if client.IsConnected() {
                                syncControlSubs(client)
                        }
                }
        }
}

// ============================================================================
// 
// ============================================================================

func runClientPipeline(ctx context.Context, brokerURL, room, peerID string, secret []byte, listenAddr string) {
        log.Printf("[TUNNEL-CLIENT] Mounting system bind link onto %s...", listenAddr)

        rAddr, err := net.ResolveUDPAddr("udp", listenAddr)
        if err != nil {
                log.Fatalf("[FATAL] Parsing target socket configuration failed: %v", err)
        }

        udpConn, err := net.ListenUDP("udp", rAddr)
        if err != nil {
                log.Fatalf("[FATAL] Port allocation error: %v", err)
        }
        defer udpConn.Close()

        // Dynamic tracker mapping out the local WireGuard application source port
        var (
                wgAddrMutex sync.RWMutex
                lastWgAddr  *net.UDPAddr
        )

        opts := mqtt.NewClientOptions().AddBroker(brokerURL)
        opts.SetClientID(RandomClientID("br-cli"))
        opts.SetCleanSession(true)
        opts.SetOrderMatters(false)
        opts.SetAutoReconnect(true)
        opts.SetStore(mqtt.NewMemoryStore())

        outboundChan := make(chan []byte, 4096)
        var activeSubs sync.Map

        syncClientSubs := func(client mqtt.Client) {
                now := time.Now().Unix()
                targets := map[string]bool{
                        GenerateTopic([]byte(peerID), secret, "down", now):           true,
                        GenerateTopic([]byte(peerID), secret, "down", now+WindowSec): true,
                }

                for topic := range targets {
                        if _, exists := activeSubs.Load(topic); !exists {
                                token := client.Subscribe(topic, 0, func(c mqtt.Client, m mqtt.Message) {
                                        wgAddrMutex.RLock()
                                        targetAddr := lastWgAddr
                                        wgAddrMutex.RUnlock()

                                        // Route downstream traffic back to the detected WireGuard source interface port
                                        if targetAddr != nil {
                                                _, _ = udpConn.WriteToUDP(m.Payload(), targetAddr)
                                        }
                                })
                                if token.WaitTimeout(time.Second*2) && token.Error() == nil {
                                        activeSubs.Store(topic, true)
                                }
                        }
                }

                activeSubs.Range(func(key, value interface{}) bool {
                        topic := key.(string)
                        if !targets[topic] {
                                client.Unsubscribe(topic)
                                activeSubs.Delete(topic)
                        }
                        return true
                })
        }

        opts.SetOnConnectHandler(func(client mqtt.Client) {
                log.Println("[CLIENT] Active network channel opened. Synchronizing pipelines...")
                syncClientSubs(client)
        })

        mqttClient := mqtt.NewClient(opts)
        if token := mqttClient.Connect(); token.Wait() && token.Error() != nil {
                log.Fatalf("[CLIENT FATAL] Handshake connection failure: %v", token.Error())
        }
        defer mqttClient.Disconnect(250)

        go func() {
                <-ctx.Done()
                udpConn.Close()
        }()

        // Secure Control Plane Heartbeat Loop
        go func() {
                ticker := time.NewTicker(10 * time.Second)
                defer ticker.Stop()
                for {
                        select {
                        case <-ctx.Done():
                                return
                        case <-ticker.C:
                                if mqttClient.IsConnected() {
                                        now := time.Now().Unix()
                                        nonceBytes := make([]byte, 8)
                                        if _, err := io.ReadFull(rand.Reader, nonceBytes); err != nil {
                                                continue
                                        }
                                        nonceStr := hex.EncodeToString(nonceBytes)

                                        plainTextMsg := fmt.Sprintf("%d %s peer_connected %s", now, nonceStr, peerID)
                                        encryptedPayload, err := EncryptGCM([]byte(plainTextMsg), secret)
                                        if err != nil {
                                                continue
                                        }
                                        mqttClient.Publish(GenerateTopic([]byte(room), secret, "control", now), 0, false, encryptedPayload)
                                }
                        }
                }
        }()

        // LOOP 2: Fixed using ReadFromUDP to grab dynamic dynamic local source ports
        go func() {
                buf := make([]byte, 2048)
                for {
                        n, remoteAddr, err := udpConn.ReadFromUDP(buf)
                        if err != nil {
                                return
                        }

                        // Thread-safe update of the dynamic WireGuard destination port
                        wgAddrMutex.Lock()
                        lastWgAddr = remoteAddr
                        wgAddrMutex.Unlock()

                        select {
                        case outboundChan <- append([]byte(nil), buf[:n]...):
                        default:
                        }
                }
        }()

        go func() {
                for {
                        select {
                        case <-ctx.Done():
                                return
                        case packet := <-outboundChan:
                                if !mqttClient.IsConnected() {
                                        continue
                                }
                                now := time.Now().Unix()
                                mqttClient.Publish(GenerateTopic([]byte(peerID), secret, "up", now), 0, false, packet)
                                if (now % WindowSec) > (WindowSec - DriftBuffer) {
                                        mqttClient.Publish(GenerateTopic([]byte(peerID), secret, "up", now+WindowSec), 0, false, packet)
                                }
                        }
                }
        }()

        ticker := time.NewTicker(5 * time.Second)
        defer ticker.Stop()
        for {
                select {
                case <-ctx.Done():
                        return
                case <-ticker.C:
                        if mqttClient.IsConnected() {
                                syncClientSubs(mqttClient)
                        }
                }
        }
}

// ============================================================================
// 
// ============================================================================

func main() {
        role := flag.String("role", "client", "Execution context profile matrix: 'server' or 'client'")
        room := flag.String("room", "default-room", "Shared control room context space identifier")
        secret := flag.String("secret", "", "Shared master secret hex token (Required for client role)")
        addr := flag.String("addr", "127.0.0.1:51820", "Target system network local wire layout link address socket mapping")
        broker := flag.String("broker", "ssl://broker.example.com:8883", "MQTT cluster cloud URI link (Prefer TLS endpoint port)")
        flag.Parse()

        ctx, cancel := context.WithCancel(context.Background())
        sigChan := make(chan os.Signal, 1)
        signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

        go func() {
                <-sigChan
                log.Println("\n[⏹️ EXIT SIGNAL]: Tearing down structures and contexts safely...")
                cancel()
        }()

        go StartReplayJanitor(ctx)

        var workingSecret []byte

        if *role == "server" {
                rawKey := make([]byte, 32)
                if _, err := io.ReadFull(rand.Reader, rawKey); err != nil {
                        log.Fatalf("[CRITICAL ERROR] Failed to access system entropy engine: %v", err)
                }
                generatedSecretStr := hex.EncodeToString(rawKey)
                workingSecret = []byte(generatedSecretStr)

                fmt.Println("\n======================================================================")
                fmt.Println("🔒 SERVER INITIALIZED - FLAT ROOT TOPIC ARCHITECTURE ACTIVATED")
                fmt.Printf("👉 KEY: %s\n", generatedSecretStr)
                fmt.Println("Copy the string signature above and provide it to your client components.")
                fmt.Println("======================================================================")

                log.Printf("[INIT] Spawning Control Operator Gateway Room: %s", *room)
                runServerControlPlane(ctx, *broker, *room, workingSecret, *addr)

        } else {
                if *secret == "" {
                        log.Fatal("[FATAL] Initialization configuration mismatch: Client requires a valid '-secret' flag string.")
                }
                workingSecret = []byte(*secret)

                rawPeer := make([]byte, 16)
                if _, err := io.ReadFull(rand.Reader, rawPeer); err != nil {
                        log.Fatalf("[FATAL] Failed to generate local node context identification signature: %v", err)
                }
                autoPeerID := hex.EncodeToString(rawPeer)

                log.Printf("[INIT] Spawning Node Instance Gateway with Auto-Generated ID: %s", autoPeerID)
                runClientPipeline(ctx, *broker, *room, autoPeerID, workingSecret, *addr)
        }

        time.Sleep(200 * time.Millisecond)
}
