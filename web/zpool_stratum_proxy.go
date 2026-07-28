package web

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"zpoolproxy/config"
)

type MinerConnection struct {
	ID          string    `json:"id"`
	RemoteAddr  string    `json:"remoteAddr"`
	Username    string    `json:"username"`
	WorkerName  string    `json:"workerName"`
	ConnectedAt time.Time `json:"connectedAt"`
}

type ZpoolStratumProxy struct {
	cfg            *config.Config
	mu             sync.RWMutex
	sessionCounter int64
	sessions       map[string]*MinerConnection
}

func NewZpoolStratumProxy(cfg *config.Config) *ZpoolStratumProxy {
	return &ZpoolStratumProxy{
		cfg:      cfg,
		sessions: make(map[string]*MinerConnection),
	}
}

func (p *ZpoolStratumProxy) Start() error {
	addr := fmt.Sprintf(":%d", p.cfg.StratumPort)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	log.Printf("[Zpool Stratum Proxy] Listening on %s -> %s:%d",
		addr, p.cfg.ZpoolStratumHost, p.cfg.ZpoolStratumPort)

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("[Zpool Stratum Proxy] accept error: %v", err)
			continue
		}

		sessionID := p.addSession(conn.RemoteAddr().String())
		go p.handleMiner(sessionID, conn)
	}
}

func (p *ZpoolStratumProxy) handleMiner(sessionID string, miner net.Conn) {
	defer miner.Close()
	defer p.removeSession(sessionID)

	upstreamAddr := fmt.Sprintf("%s:%d", p.cfg.ZpoolStratumHost, p.cfg.ZpoolStratumPort)
	upstream, err := net.DialTimeout("tcp", upstreamAddr, 15*time.Second)
	if err != nil {
		log.Printf("[Zpool Stratum Proxy] failed to connect upstream %s: %v", upstreamAddr, err)
		return
	}
	defer upstream.Close()

	log.Printf("[Zpool Stratum Proxy] miner %s connected -> %s", miner.RemoteAddr(), upstreamAddr)

	done := make(chan struct{}, 2)

	go func() {
		defer func() { done <- struct{}{} }()
		p.forwardMinerToUpstream(sessionID, miner, upstream)
	}()

	go func() {
		defer func() { done <- struct{}{} }()
		io.Copy(miner, upstream) //nolint:errcheck
	}()

	<-done
	log.Printf("[Zpool Stratum Proxy] miner %s disconnected", miner.RemoteAddr())
}

func (p *ZpoolStratumProxy) forwardMinerToUpstream(sessionID string, miner net.Conn, upstream net.Conn) {
	scanner := bufio.NewScanner(miner)
	scanner.Buffer(make([]byte, 64*1024), 64*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		rewritten := p.rewriteLine(sessionID, line)

		if _, err := upstream.Write(append(rewritten, '\n')); err != nil {
			return
		}
	}
}

func (p *ZpoolStratumProxy) rewriteLine(sessionID string, line []byte) []byte {
	var msg map[string]interface{}
	if err := json.Unmarshal(line, &msg); err != nil {
		return line
	}

	method, _ := msg["method"].(string)
	if method != "mining.authorize" {
		return line
	}

	params, ok := msg["params"].([]interface{})
	if !ok || len(params) < 1 {
		return line
	}

	injectedUsername := strings.TrimSpace(p.cfg.ZpoolStratumUsername)
	if injectedUsername != "" {
		workerSuffix := ""
		if original, ok := params[0].(string); ok {
			if idx := strings.LastIndex(original, "."); idx >= 0 {
				workerSuffix = original[idx:]
			}
		}
		params[0] = injectedUsername + workerSuffix
	}

	injectedPassword := strings.TrimSpace(p.cfg.ZpoolStratumPassword)
	if injectedPassword != "" {
		if len(params) < 2 {
			params = append(params, injectedPassword)
		} else {
			params[1] = injectedPassword
		}
	}

	if effectiveUsername, ok := params[0].(string); ok {
		p.updateSessionAuthorize(sessionID, effectiveUsername)
	}

	msg["params"] = params
	rewritten, err := json.Marshal(msg)
	if err != nil {
		return line
	}
	return rewritten
}

func (p *ZpoolStratumProxy) addSession(remoteAddr string) string {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.sessionCounter++
	sessionID := strconv.FormatInt(p.sessionCounter, 10)
	p.sessions[sessionID] = &MinerConnection{
		ID:          sessionID,
		RemoteAddr:  remoteAddr,
		WorkerName:  "-",
		ConnectedAt: time.Now(),
	}
	return sessionID
}

func (p *ZpoolStratumProxy) removeSession(sessionID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.sessions, sessionID)
}

func (p *ZpoolStratumProxy) updateSessionAuthorize(sessionID, username string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	session, ok := p.sessions[sessionID]
	if !ok {
		return
	}

	session.Username = username
	workerName := username
	if idx := strings.LastIndex(username, "."); idx >= 0 && idx+1 < len(username) {
		workerName = username[idx+1:]
	}
	session.WorkerName = strings.TrimSpace(workerName)
	if session.WorkerName == "" {
		session.WorkerName = "-"
	}
}

func (p *ZpoolStratumProxy) SnapshotMiners() []MinerConnection {
	p.mu.RLock()
	defer p.mu.RUnlock()

	miners := make([]MinerConnection, 0, len(p.sessions))
	for _, s := range p.sessions {
		miners = append(miners, *s)
	}
	return miners
}
