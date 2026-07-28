package web

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"time"

	"zpoolproxy/config"
)

type ZpoolStratumProxy struct {
	cfg *config.Config
}

func NewZpoolStratumProxy(cfg *config.Config) *ZpoolStratumProxy {
	return &ZpoolStratumProxy{cfg: cfg}
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
		go p.handleMiner(conn)
	}
}

func (p *ZpoolStratumProxy) handleMiner(miner net.Conn) {
	defer miner.Close()

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
		p.forwardMinerToUpstream(miner, upstream)
	}()

	go func() {
		defer func() { done <- struct{}{} }()
		io.Copy(miner, upstream) //nolint:errcheck
	}()

	<-done
	log.Printf("[Zpool Stratum Proxy] miner %s disconnected", miner.RemoteAddr())
}

func (p *ZpoolStratumProxy) forwardMinerToUpstream(miner net.Conn, upstream net.Conn) {
	scanner := bufio.NewScanner(miner)
	scanner.Buffer(make([]byte, 64*1024), 64*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		rewritten := p.rewriteLine(line)

		if _, err := upstream.Write(append(rewritten, '\n')); err != nil {
			return
		}
	}
}

func (p *ZpoolStratumProxy) rewriteLine(line []byte) []byte {
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

	msg["params"] = params
	rewritten, err := json.Marshal(msg)
	if err != nil {
		return line
	}
	return rewritten
}
