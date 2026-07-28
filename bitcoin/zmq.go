package bitcoin

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net"
	"time"

	"zpoolproxy/config"
)

type ZmqBlockSubscriber struct {
	cfg          *config.Config
	onBlockFound func(blockHash string)
	conn         net.Conn
	stopChan     chan struct{}
}

func NewZmqBlockSubscriber(cfg *config.Config, onBlockFound func(blockHash string)) *ZmqBlockSubscriber {
	return &ZmqBlockSubscriber{
		cfg:          cfg,
		onBlockFound: onBlockFound,
		stopChan:     make(chan struct{}),
	}
}

func (z *ZmqBlockSubscriber) Start() {
	if z.cfg.ZmqHost == "" || z.cfg.ZmqPort <= 0 {
		log.Printf("[ZMQ Go] ZMQ host or port not set, skipping ZMQ connection.")
		return
	}

	go z.connectLoop()
}

func (z *ZmqBlockSubscriber) connectLoop() {
	addr := fmt.Sprintf("%s:%d", z.cfg.ZmqHost, z.cfg.ZmqPort)
	lastConnectAt := time.Time{}

	for {
		select {
		case <-z.stopChan:
			return
		default:
		}

		log.Printf("[ZMQ Go] Connecting to Bitcoin Node ZMQ at %s...", addr)
		conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			log.Printf("[ZMQ Go] Failed to connect to ZMQ (%v), retrying in 5 seconds...", err)
			time.Sleep(5 * time.Second)
			continue
		}
		lastConnectAt = time.Now()

		z.conn = conn
		log.Printf("[ZMQ Go] Connected to ZMQ socket at %s! Listening for instant block notifications...", addr)

		if err := z.handshakeAndSubscribe(conn); err != nil {
			log.Printf("[ZMQ Go] ZMQ handshake error: %v, reconnecting...", err)
			conn.Close()
			time.Sleep(3 * time.Second)
			continue
		}

		if err := z.readLoop(conn); err != nil {
			log.Printf("[ZMQ Go] Read loop ended: %v. Reconnecting...", err)
		}
		conn.Close()

		// If the socket is closed almost immediately by peer (e.g., bad handshake/sub frame),
		// wait a bit longer to avoid log spam and connection storm.
		if !lastConnectAt.IsZero() && time.Since(lastConnectAt) < 2*time.Second {
			time.Sleep(8 * time.Second)
			continue
		}
		time.Sleep(3 * time.Second)
	}
}

// ZMTP 3.0 Greeting & Handshake
func (z *ZmqBlockSubscriber) handshakeAndSubscribe(conn net.Conn) error {
	// 1. ZMTP 3.0 Greeting
	greeting := make([]byte, 64)
	greeting[0] = 0xff
	greeting[9] = 0x7f
	greeting[10] = 0x03 // Version 3.0
	greeting[11] = 0x00
	copy(greeting[12:16], []byte("NULL"))

	if _, err := conn.Write(greeting); err != nil {
		return err
	}

	recvGreeting := make([]byte, 64)
	if _, err := io.ReadFull(conn, recvGreeting); err != nil {
		return err
	}

	// 2. Send READY Command (Socket-Type SUB)
	readyMetadata := []byte("\x05READY\x0bSocket-Type\x00\x00\x00\x03SUB")
	readyFrame := z.buildZmtpFrame(readyMetadata, false)
	if _, err := conn.Write(readyFrame); err != nil {
		return err
	}

	// Read server READY command/message frame(s)
	if _, err := z.readZmtpMessage(conn); err != nil {
		return err
	}

	// 3. Send Subscribe command for "hashblock" topic
	subTopic := append([]byte{0x01}, []byte("hashblock")...)
	subFrame := z.buildZmtpFrame(subTopic, false)
	if _, err := conn.Write(subFrame); err != nil {
		return err
	}

	return nil
}

func (z *ZmqBlockSubscriber) buildZmtpFrame(data []byte, hasMore bool) []byte {
	var flag byte = 0
	if hasMore {
		flag |= 0x01
	}

	if len(data) < 255 {
		frame := make([]byte, 2+len(data))
		frame[0] = flag
		frame[1] = byte(len(data))
		copy(frame[2:], data)
		return frame
	}

	frame := make([]byte, 9+len(data))
	frame[0] = flag | 0x02 // Long frame
	binary.BigEndian.PutUint64(frame[1:9], uint64(len(data)))
	copy(frame[9:], data)
	return frame
}

func (z *ZmqBlockSubscriber) readZmtpFrame(conn net.Conn) ([]byte, bool, error) {
	flagBuf := make([]byte, 1)
	if _, err := io.ReadFull(conn, flagBuf); err != nil {
		return nil, false, err
	}

	flags := flagBuf[0]
	hasMore := (flags & 0x01) != 0
	isLong := (flags & 0x02) != 0

	var dataLen uint64
	if isLong {
		lenBuf := make([]byte, 8)
		if _, err := io.ReadFull(conn, lenBuf); err != nil {
			return nil, false, err
		}
		dataLen = binary.BigEndian.Uint64(lenBuf)
	} else {
		lenBuf := make([]byte, 1)
		if _, err := io.ReadFull(conn, lenBuf); err != nil {
			return nil, false, err
		}
		dataLen = uint64(lenBuf[0])
	}

	data := make([]byte, dataLen)
	if _, err := io.ReadFull(conn, data); err != nil {
		return nil, false, err
	}

	return data, hasMore, nil
}

func (z *ZmqBlockSubscriber) readZmtpMessage(conn net.Conn) ([][]byte, error) {
	frames := make([][]byte, 0, 3)
	for {
		frame, hasMore, err := z.readZmtpFrame(conn)
		if err != nil {
			return nil, err
		}
		frames = append(frames, frame)
		if !hasMore {
			break
		}
	}
	return frames, nil
}

func (z *ZmqBlockSubscriber) readLoop(conn net.Conn) error {
	for {
		msgFrames, err := z.readZmtpMessage(conn)
		if err != nil {
			return err
		}

		if len(msgFrames) < 2 {
			continue
		}

		topic := string(msgFrames[0])
		if topic != "hashblock" {
			continue
		}

		hashBytes := msgFrames[1]
		if len(hashBytes) != 32 {
			continue
		}

		hashHex := hex.EncodeToString(hashBytes)
		log.Printf("⚡ [ZMQ Instant Notification] New Block Detected on Network! (Hash: %s)", hashHex)
		if z.onBlockFound != nil {
			z.onBlockFound(hashHex)
		}
	}
}

func (z *ZmqBlockSubscriber) Stop() {
	close(z.stopChan)
	if z.conn != nil {
		z.conn.Close()
	}
}
