package agent

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
)

// IPCMessage wraps a message for inter-process communication.
type IPCMessage struct {
	Type    string          `json:"type"` // "message", "task_assign", "task_complete", "shutdown", "heartbeat", "heartbeat_ack"
	From    string          `json:"from"` // sender agent name
	To      string          `json:"to"`   // recipient agent name
	Payload json.RawMessage `json:"payload"`
	ID      string          `json:"id"` // unique message ID for tracking
}

// IPCBridge manages Unix domain socket communication between processes.
type IPCBridge struct {
	socketDir string
	listener  net.Listener
	conns     map[string]net.Conn // agentName -> connection
	mu        sync.RWMutex
	onMessage func(IPCMessage)
	msgCh     chan IPCMessage
	closed    atomic.Bool
}

// NewIPCBridge creates a new IPCBridge rooted at socketDir.
func NewIPCBridge(socketDir string) *IPCBridge {
	return &IPCBridge{
		socketDir: socketDir,
		conns:     make(map[string]net.Conn),
		msgCh:     make(chan IPCMessage, 256),
	}
}

// socketPath returns the Unix domain socket path for a given agent name.
func (b *IPCBridge) socketPath(agentName string) string {
	return filepath.Join(b.socketDir, fmt.Sprintf("iroha-%s.sock", agentName))
}

// Start creates the socket directory and begins listening on the parent socket.
// The parent listens on a socket named "iroha-parent".
func (b *IPCBridge) Start() error {
	_ = os.MkdirAll(b.socketDir, 0755)

	sockPath := b.socketPath("parent")
	// Clean up any stale socket file
	_ = os.Remove(sockPath)

	l, err := net.Listen("unix", sockPath)
	if err != nil {
		return fmt.Errorf("ipc listen failed: %w", err)
	}
	b.listener = l

	// Accept connections in the background
	go b.acceptLoop()

	LogInfo(CatSystem, "ipc_started", "IPC bridge listening on socket", map[string]any{
		"socket": sockPath,
	})

	return nil
}

// acceptLoop accepts incoming connections and reads messages from them.
func (b *IPCBridge) acceptLoop() {
	for {
		conn, err := b.listener.Accept()
		if err != nil {
			if b.closed.Load() {
				return
			}
			LogError(CatSystem, "ipc_accept_failed", "IPC accept error", err, nil)
			return
		}

		// Read messages from this connection in a goroutine
		go b.readLoop(conn)
	}
}

// readLoop reads length-prefixed JSON messages from a connection.
func (b *IPCBridge) readLoop(conn net.Conn) {
	defer func() { _ = conn.Close() }()

	for {
		if b.closed.Load() {
			return
		}

		msg, err := readMessage(conn)
		if err != nil {
			if b.closed.Load() {
				return
			}
			// Connection closed or error — clean up
			b.mu.Lock()
			for name, c := range b.conns {
				if c == conn {
					delete(b.conns, name)
					LogInfo(CatSystem, "ipc_conn_closed", "IPC connection closed", map[string]any{"agent": name})
					break
				}
			}
			b.mu.Unlock()
			return
		}

		// Track connection by agent name
		if msg.From != "" {
			b.mu.Lock()
			b.conns[msg.From] = conn
			b.mu.Unlock()
		}

		// Dispatch message
		b.mu.RLock()
		handler := b.onMessage
		b.mu.RUnlock()
		if handler != nil {
			handler(*msg)
		}
		select {
		case b.msgCh <- *msg:
		default:
			LogWarn(CatSystem, "ipc_channel_full", "IPC message channel full, dropping message", map[string]any{
				"msg_id": msg.ID,
				"from":   msg.From,
			})
		}
	}
}

// Connect dials the parent's Unix socket as a teammate child process.
func (b *IPCBridge) Connect(agentName string) error {
	sockPath := b.socketPath("parent")

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return fmt.Errorf("ipc connect failed: %w", err)
	}

	b.mu.Lock()
	b.conns[agentName] = conn
	b.mu.Unlock()

	// Start reading from this connection
	go b.readLoop(conn)

	LogInfo(CatSystem, "ipc_connected", "IPC connected to parent", map[string]any{
		"agent":  agentName,
		"socket": sockPath,
	})

	return nil
}

// Send writes a length-prefixed JSON message to the recipient's connection.
func (b *IPCBridge) Send(msg IPCMessage) error {
	b.mu.RLock()
	conn, ok := b.conns[msg.To]
	b.mu.RUnlock()

	if !ok {
		return fmt.Errorf("no connection for agent %q", msg.To)
	}

	return writeMessage(conn, msg)
}

// SendToParent sends a message via the connection to the parent (used by child processes).
func (b *IPCBridge) SendToParent(msg IPCMessage) error {
	b.mu.RLock()
	// Child has a single connection keyed by its own name, or by "parent"
	conn, ok := b.conns[msg.From]
	if !ok {
		// Try any available connection
		for _, c := range b.conns {
			conn = c
			ok = true
			break
		}
	}
	b.mu.RUnlock()

	if !ok {
		return fmt.Errorf("no connection to parent")
	}

	return writeMessage(conn, msg)
}

// Receive returns a channel that yields incoming IPC messages.
func (b *IPCBridge) Receive() <-chan IPCMessage {
	return b.msgCh
}

// SetOnMessage sets a callback invoked for each incoming message.
func (b *IPCBridge) SetOnMessage(fn func(IPCMessage)) {
	b.mu.Lock()
	b.onMessage = fn
	b.mu.Unlock()
}

// Close shuts down the listener and all connections.
func (b *IPCBridge) Close() {
	b.closed.Store(true)

	if b.listener != nil {
		_ = b.listener.Close()
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	for name, conn := range b.conns {
		_ = conn.Close()
		delete(b.conns, name)
	}

	// Remove socket file
	_ = os.Remove(b.socketPath("parent"))

	close(b.msgCh)
}

// readMessage reads a single length-prefixed JSON message from a connection.
func readMessage(conn net.Conn) (*IPCMessage, error) {
	// Read 4-byte length prefix
	var lenBuf [4]byte
	if _, err := conn.Read(lenBuf[:]); err != nil {
		return nil, err
	}

	msgLen := binary.BigEndian.Uint32(lenBuf[:])
	if msgLen > 10*1024*1024 { // 10MB safety limit
		return nil, fmt.Errorf("message too large: %d bytes", msgLen)
	}

	buf := make([]byte, msgLen)
	if _, err := conn.Read(buf); err != nil {
		return nil, err
	}

	var msg IPCMessage
	if err := json.Unmarshal(buf, &msg); err != nil {
		return nil, fmt.Errorf("json unmarshal failed: %w", err)
	}

	return &msg, nil
}

// writeMessage writes a single length-prefixed JSON message to a connection.
func writeMessage(conn net.Conn, msg IPCMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("json marshal failed: %w", err)
	}

	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(data)))

	if _, err := conn.Write(lenBuf[:]); err != nil {
		return err
	}
	if _, err := conn.Write(data); err != nil {
		return err
	}

	return nil
}
