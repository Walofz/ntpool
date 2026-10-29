package stratum

import (
	"bufio"
	"bytes"
	crand "crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"ntpool/bitcoin"
	"ntpool/config"
	"ntpool/crypto"
	"ntpool/pool"
)

const vardiffSubmitGraceWindow = 15 * time.Second

type FoundBlock struct {
	Height    int64     `json:"height"`
	Hash      string    `json:"hash"`
	Miner     string    `json:"miner"`
	Worker    string    `json:"worker"`
	Timestamp time.Time `json:"timestamp"`
	Reward    float64   `json:"reward"`
	Symbol    string    `json:"symbol"`
}

// maxFoundBlocks caps the persisted found block history.
const maxFoundBlocks = 1000

type NotificationChannelStatus struct {
	Enabled       bool       `json:"enabled"`
	Configured    bool       `json:"configured"`
	LastAttemptAt *time.Time `json:"lastAttemptAt,omitempty"`
	LastResult    string     `json:"lastResult,omitempty"`
	LastError     string     `json:"lastError,omitempty"`
}

type StratumServer struct {
	mu                 sync.RWMutex
	blocksMu           sync.RWMutex
	saveMu             sync.Mutex
	notificationMu     sync.RWMutex
	listenMu           sync.Mutex
	stopOnce           sync.Once
	cfg                *config.Config
	jobManager         *pool.JobManager
	bitcoinRpc         *bitcoin.BitcoinRpcClient
	sessions           map[string]*StratumSession
	sessionCounter     int64
	foundBlocks        []FoundBlock
	StatsUpdated       chan struct{}
	blocksFilePath     string
	notificationStatus map[string]NotificationChannelStatus
	listener           net.Listener
	stopChan           chan struct{}
	activeConns        int64
}

func NewStratumServer(cfg *config.Config, jm *pool.JobManager, rpc *bitcoin.BitcoinRpcClient) *StratumServer {
	s := &StratumServer{
		cfg:            cfg,
		jobManager:     jm,
		bitcoinRpc:     rpc,
		sessions:       make(map[string]*StratumSession),
		StatsUpdated:   make(chan struct{}, 100),
		blocksFilePath: filepath.Join(".", "data", "found_blocks.json"),
		stopChan:       make(chan struct{}),
		notificationStatus: map[string]NotificationChannelStatus{
			"ntfy": {
				Enabled:    cfg.EnableNtfy,
				Configured: strings.TrimSpace(cfg.NtfyServer) != "" && strings.TrimSpace(cfg.NtfyTopic) != "",
			},
			"discord": {
				Enabled:    cfg.EnableDiscord,
				Configured: strings.TrimSpace(cfg.DiscordWebhookURL) != "",
			},
		},
	}
	s.loadFoundBlocks()
	return s
}

func (s *StratumServer) GetNotificationStatus() map[string]NotificationChannelStatus {
	s.notificationMu.RLock()
	defer s.notificationMu.RUnlock()

	statuses := make(map[string]NotificationChannelStatus, len(s.notificationStatus))
	for channel, status := range s.notificationStatus {
		statuses[channel] = status
	}
	return statuses
}

func (s *StratumServer) beginNotificationAttempt(channel string) {
	s.notificationMu.Lock()
	defer s.notificationMu.Unlock()

	status, ok := s.notificationStatus[channel]
	if !ok {
		return
	}
	now := time.Now()
	status.LastAttemptAt = &now
	status.LastResult = "sending"
	status.LastError = ""
	s.notificationStatus[channel] = status
}

func (s *StratumServer) finishNotificationAttempt(channel string, succeeded bool, message string) {
	s.notificationMu.Lock()
	defer s.notificationMu.Unlock()

	status, ok := s.notificationStatus[channel]
	if !ok {
		return
	}
	if succeeded {
		status.LastResult = "sent"
		status.LastError = ""
	} else {
		status.LastResult = "failed"
		status.LastError = message
	}
	s.notificationStatus[channel] = status
}

func (s *StratumServer) loadFoundBlocks() {
	path := s.blocksFilePath
	_ = os.MkdirAll(filepath.Dir(path), 0755)

	if data, err := os.ReadFile(path); err == nil {
		var blocks []FoundBlock
		if err := json.Unmarshal(data, &blocks); err == nil {
			if blocks == nil {
				blocks = []FoundBlock{}
			}
			blocks = trimFoundBlocks(blocks)
			s.blocksMu.Lock()
			s.foundBlocks = blocks
			s.blocksMu.Unlock()
			log.Printf("[Stratum] Loaded %d persisted found blocks from disk.", len(blocks))
			return
		}
		log.Printf("[Stratum] Failed to parse %s, starting with an empty history: %v", path, err)
	}

	s.blocksMu.Lock()
	s.foundBlocks = []FoundBlock{}
	s.blocksMu.Unlock()
	s.saveFoundBlocks()
	log.Printf("[Stratum] Initialized new found_blocks.json file at %s", path)
}

// GetFoundBlocks returns a copy of the found block history, newest first.
func (s *StratumServer) GetFoundBlocks() []FoundBlock {
	s.blocksMu.RLock()
	defer s.blocksMu.RUnlock()

	blocks := make([]FoundBlock, len(s.foundBlocks))
	copy(blocks, s.foundBlocks)
	return blocks
}

// recordFoundBlock prepends a block to the history and persists it.
func (s *StratumServer) recordFoundBlock(block FoundBlock) {
	s.blocksMu.Lock()
	s.foundBlocks = trimFoundBlocks(append([]FoundBlock{block}, s.foundBlocks...))
	s.blocksMu.Unlock()

	s.saveFoundBlocks()
}

func (s *StratumServer) saveFoundBlocks() {
	s.blocksMu.RLock()
	blocks := make([]FoundBlock, len(s.foundBlocks))
	copy(blocks, s.foundBlocks)
	path := s.blocksFilePath
	s.blocksMu.RUnlock()

	data, err := json.MarshalIndent(blocks, "", "  ")
	if err != nil {
		log.Printf("[Stratum] Failed to encode found blocks: %v", err)
		return
	}

	// Serialized so concurrent saves cannot interleave partial writes.
	s.saveMu.Lock()
	defer s.saveMu.Unlock()

	_ = os.MkdirAll(filepath.Dir(path), 0755)
	if err := os.WriteFile(path, data, 0644); err != nil {
		log.Printf("[Stratum] Failed to persist found blocks to %s: %v", path, err)
	}
}

func trimFoundBlocks(blocks []FoundBlock) []FoundBlock {
	if len(blocks) > maxFoundBlocks {
		return blocks[:maxFoundBlocks]
	}
	return blocks
}

func (s *StratumServer) resetAllBestShares() {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, session := range s.sessions {
		session.ResetBestShare()
	}
}

func (s *StratumServer) DisableSession(sessionID string, reason string) bool {
	s.mu.RLock()
	session := s.sessions[sessionID]
	s.mu.RUnlock()
	if session == nil {
		return false
	}
	session.Disable(reason)
	return true
}

func (s *StratumServer) BanSession(sessionID string, reason string) bool {
	s.mu.RLock()
	session := s.sessions[sessionID]
	s.mu.RUnlock()
	if session == nil {
		return false
	}
	session.Ban(reason)
	if session.Conn != nil {
		_ = session.Conn.Close()
	}
	return true
}

func (s *StratumServer) ResumeSession(sessionID string) bool {
	s.mu.RLock()
	session := s.sessions[sessionID]
	s.mu.RUnlock()
	if session == nil {
		return false
	}
	session.Resume()
	return true
}

func (s *StratumServer) isSubmitBlockAccepted(result interface{}, err error) bool {
	if err != nil {
		return false
	}
	if result == nil {
		return true
	}
	if resultString, ok := result.(string); ok {
		return strings.TrimSpace(resultString) == ""
	}
	return false
}

func (s *StratumServer) Start() error {
	addr := fmt.Sprintf(":%d", s.cfg.StratumPort)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	s.listenMu.Lock()
	s.listener = listener
	s.listenMu.Unlock()

	log.Printf("[Stratum Go] Server listening on %s", addr)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				if s.stopped() {
					return
				}
				log.Printf("[Stratum Go] Accept error: %v (retrying in 1s)", err)
				time.Sleep(time.Second)
				continue
			}
			go s.handleConnection(conn)
		}
	}()
	return nil
}

func (s *StratumServer) stopped() bool {
	select {
	case <-s.stopChan:
		return true
	default:
		return false
	}
}

// Stop closes the listener and every live session connection.
func (s *StratumServer) Stop() {
	s.stopOnce.Do(func() { close(s.stopChan) })

	s.listenMu.Lock()
	listener := s.listener
	s.listener = nil
	s.listenMu.Unlock()
	if listener != nil {
		_ = listener.Close()
	}

	for _, session := range s.GetActiveSessions() {
		if session.Conn != nil {
			_ = session.Conn.Close()
		}
	}
}

func (s *StratumServer) handleConnection(conn net.Conn) {
	active := atomic.AddInt64(&s.activeConns, 1)
	defer atomic.AddInt64(&s.activeConns, -1)
	if maxConns := s.cfg.StratumMaxConns; maxConns > 0 && active > int64(maxConns) {
		log.Printf("[Stratum Go] Rejecting %s: STRATUM_MAX_CONNS limit %d reached", conn.RemoteAddr(), maxConns)
		_ = conn.Close()
		return
	}

	s.mu.Lock()
	s.sessionCounter++
	sessionID := fmt.Sprintf("%08x", s.sessionCounter)
	extranonce1 := fmt.Sprintf("%08x", s.sessionCounter&0xffffffff)
	session := NewStratumSession(sessionID, conn, extranonce1, s.cfg.DefaultDiff)
	s.sessions[sessionID] = session
	s.mu.Unlock()

	defer func() {
		conn.Close()
		s.mu.Lock()
		delete(s.sessions, sessionID)
		s.mu.Unlock()
		s.notifyStats()
	}()

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 4096), 1024*1024)

	idleTimeout := time.Duration(s.cfg.StratumIdleTimeout) * time.Second
	for {
		if idleTimeout > 0 {
			if err := conn.SetReadDeadline(time.Now().Add(idleTimeout)); err != nil {
				break
			}
		}
		if !scanner.Scan() {
			break
		}
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			s.handleMessage(session, line)
		}
	}
	if err := scanner.Err(); err != nil {
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			log.Printf("[Stratum] Session %s closed after %s without data (STRATUM_IDLE_TIMEOUT)", session.ID, idleTimeout)
		} else {
			log.Printf("[Stratum] Session %s scanner error: %v", session.ID, err)
		}
	}
}

func (s *StratumServer) sendResponse(session *StratumSession, id interface{}, result interface{}, errObj interface{}) {
	resp := map[string]interface{}{
		"id":     id,
		"result": result,
		"error":  errObj,
	}
	if err := session.WriteJSON(resp); err != nil {
		log.Printf("[Stratum] Failed to send response to session %s: %v", session.ID, err)
	}
}

func (s *StratumServer) sendNotification(session *StratumSession, method string, params []interface{}) {
	notif := map[string]interface{}{
		"id":     nil,
		"method": method,
		"params": params,
	}
	if err := session.WriteJSON(notif); err != nil {
		log.Printf("[Stratum] Failed to send notification %s to session %s: %v", method, session.ID, err)
	}
}

func (s *StratumServer) notifyStats() {
	select {
	case s.StatsUpdated <- struct{}{}:
	default:
	}
}

func (s *StratumServer) handleMessage(session *StratumSession, rawLine string) {
	var msg map[string]interface{}
	if err := json.Unmarshal([]byte(rawLine), &msg); err != nil {
		s.sendResponse(session, nil, nil, map[string]interface{}{"code": -32700, "message": "Parse error"})
		return
	}

	id := msg["id"]
	method, _ := msg["method"].(string)
	params, _ := msg["params"].([]interface{})

	switch method {
	case "mining.configure":
		s.handleConfigure(session, id, params)
	case "mining.subscribe":
		s.handleSubscribe(session, id, params)
	case "mining.authorize":
		s.handleAuthorize(session, id, params)
	case "mining.submit":
		s.handleSubmit(session, id, params)
	case "mining.suggest_difficulty":
		s.handleSuggestDifficulty(session, id, params)
	case "mining.suggest_target", "mining.extranonce.subscribe":
		s.sendResponse(session, id, true, nil)
	default:
		s.sendResponse(session, id, nil, map[string]interface{}{"code": -32601, "message": "Method not found"})
	}
}

// handleSuggestDifficulty acknowledges the miner's requested difficulty. The
// pool keeps its own difficulty, but the request is logged because a miner that
// mines at a difficulty other than the assigned one submits shares the pool has
// to reject.
func (s *StratumServer) handleSuggestDifficulty(session *StratumSession, id interface{}, params []interface{}) {
	suggested := 0.0
	if len(params) > 0 {
		switch v := params[0].(type) {
		case float64:
			suggested = v
		case string:
			if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
				suggested = f
			}
		}
	}

	_, workerName := session.Identity()
	log.Printf("[Stratum] worker=%s ip=%s suggested diff %s while pool diff is %s - request acknowledged, pool keeps its own difficulty",
		fallbackWorker(workerName), session.IP, formatShareDiff(suggested), formatShareDiff(session.Diff()))
	s.sendResponse(session, id, true, nil)
}

func (s *StratumServer) handleConfigure(session *StratumSession, id interface{}, params []interface{}) {
	result := make(map[string]interface{})
	session.SetVersionRolling(true)
	result["version-rolling"] = true
	result["version-rolling.mask"] = session.VersionRollingMask
	s.sendResponse(session, id, result, nil)
}

func (s *StratumServer) notifyBlockFound(block FoundBlock) {
	if !s.cfg.EnableNtfy || s.cfg.NtfyServer == "" || s.cfg.NtfyTopic == "" {
		return
	}

	symbol := strings.TrimSpace(s.cfg.CoinSymbol)
	if symbol == "" {
		symbol = strings.TrimSpace(block.Symbol)
	}
	if symbol == "" {
		symbol = "COIN"
	}

	body := fmt.Sprintf(
		"FOUND SOLO %s\n%s #%d\nReward %.8f %s\nMiner %s\nWorker %s",
		symbol,
		strings.ToUpper(strings.TrimSpace(s.cfg.RpcNetwork)),
		block.Height,
		block.Reward,
		symbol,
		block.Miner,
		block.Worker,
	)
	if err := s.sendNtfyNotification(fmt.Sprintf("FOUND SOLO %s", symbol), body); err != nil {
		log.Printf("[ntfy] Failed to send block notification: %v", err)
	}
}

func (s *StratumServer) sendNtfyNotification(title, body string) error {
	if !s.cfg.EnableNtfy {
		return fmt.Errorf("ntfy is disabled")
	}
	if strings.TrimSpace(s.cfg.NtfyServer) == "" || strings.TrimSpace(s.cfg.NtfyTopic) == "" {
		return fmt.Errorf("ntfy server or topic is not configured")
	}
	s.beginNotificationAttempt("ntfy")

	endpoint := strings.TrimRight(s.cfg.NtfyServer, "/") + "/" + strings.TrimLeft(s.cfg.NtfyTopic, "/")
	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(body))
	if err != nil {
		s.finishNotificationAttempt("ntfy", false, "Invalid request configuration")
		return fmt.Errorf("invalid ntfy request")
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	req.Header.Set("Title", title)
	req.Header.Set("Priority", "urgent")
	req.Header.Set("Tags", "pickaxe,rotating_light")
	if s.cfg.NtfyUser != "" || s.cfg.NtfyPassword != "" {
		req.SetBasicAuth(s.cfg.NtfyUser, s.cfg.NtfyPassword)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		s.finishNotificationAttempt("ntfy", false, "HTTP request failed")
		return fmt.Errorf("ntfy HTTP request failed")
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		s.finishNotificationAttempt("ntfy", false, resp.Status)
		log.Printf("[ntfy] Notification rejected: %s %s", resp.Status, strings.TrimSpace(string(respBody)))
		return fmt.Errorf("ntfy rejected the notification: %s", resp.Status)
	}

	s.finishNotificationAttempt("ntfy", true, "")
	log.Printf("[ntfy] Notification sent to %s", endpoint)
	return nil
}

func (s *StratumServer) notifyDiscordBlockFound(block FoundBlock) {
	if !s.cfg.EnableDiscord || strings.TrimSpace(s.cfg.DiscordWebhookURL) == "" {
		return
	}

	if err := s.sendDiscordNotification(buildDiscordBlockEmbed(block, s.cfg.CoinSymbol)); err != nil {
		log.Printf("[Discord] Failed to send block notification: %v", err)
	}
}

func buildDiscordBlockEmbed(block FoundBlock, configuredSymbol string) map[string]interface{} {
	symbol := strings.TrimSpace(configuredSymbol)
	if symbol == "" {
		symbol = strings.TrimSpace(block.Symbol)
	}
	if symbol == "" {
		symbol = "COIN"
	}
	miner := strings.TrimSpace(block.Miner)
	if miner == "" {
		miner = "Unknown miner"
	} else if len(miner) > 17 {
		miner = miner[:8] + "..." + miner[len(miner)-6:]
	}
	worker := strings.TrimSpace(block.Worker)
	if worker == "" {
		worker = "Unknown worker"
	}
	hash := strings.TrimSpace(block.Hash)
	if hash == "" {
		hash = "Unknown hash"
	}
	embed := map[string]interface{}{
		"title": fmt.Sprintf("Block found: %s #%d", symbol, block.Height),
		"color": 0x2ECC71,
		"fields": []map[string]interface{}{
			{"name": "Worker", "value": worker, "inline": true},
			{"name": "Miner", "value": miner, "inline": true},
			{"name": "Reward", "value": fmt.Sprintf("%.5f %s", block.Reward, symbol), "inline": true},
			{"name": "Hash", "value": "`" + hash + "`", "inline": false},
		},
	}
	return embed
}

func (s *StratumServer) sendDiscordNotification(embed map[string]interface{}) error {
	if !s.cfg.EnableDiscord {
		return fmt.Errorf("Discord is disabled")
	}
	webhookURL := strings.TrimSpace(s.cfg.DiscordWebhookURL)
	if webhookURL == "" {
		return fmt.Errorf("Discord webhook URL is not configured")
	}
	s.beginNotificationAttempt("discord")

	payload := map[string]interface{}{
		"embeds":           []map[string]interface{}{embed},
		"allowed_mentions": map[string]interface{}{"parse": []string{}},
	}
	if username := strings.TrimSpace(s.cfg.DiscordUsername); username != "" {
		payload["username"] = username
	}
	if avatarURL := strings.TrimSpace(s.cfg.DiscordAvatarURL); avatarURL != "" {
		payload["avatar_url"] = avatarURL
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		s.finishNotificationAttempt("discord", false, "Could not encode request")
		return fmt.Errorf("could not encode Discord request")
	}

	req, err := http.NewRequest(http.MethodPost, webhookURL, bytes.NewReader(payloadBytes))
	if err != nil {
		s.finishNotificationAttempt("discord", false, "Invalid webhook configuration")
		return fmt.Errorf("invalid Discord webhook configuration")
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		s.finishNotificationAttempt("discord", false, "HTTP request failed")
		return fmt.Errorf("Discord HTTP request failed")
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		s.finishNotificationAttempt("discord", false, resp.Status)
		log.Printf("[Discord] Notification rejected: %s %s", resp.Status, strings.TrimSpace(string(respBody)))
		return fmt.Errorf("Discord rejected the notification: %s", resp.Status)
	}

	s.finishNotificationAttempt("discord", true, "")
	log.Printf("[Discord] Notification sent")
	return nil
}

func (s *StratumServer) SendTestNotification(channel string) error {
	switch channel {
	case "ntfy":
		return s.sendNtfyNotification("ntpool test", "Test notification from ntpool dashboard.")
	case "discord":
		block, err := newDiscordTestBlock(s.cfg.CoinSymbol)
		if err != nil {
			return fmt.Errorf("could not generate Discord test block: %w", err)
		}
		return s.sendDiscordNotification(buildDiscordBlockEmbed(block, s.cfg.CoinSymbol))
	default:
		return fmt.Errorf("unsupported notification channel")
	}
}

func newDiscordTestBlock(symbol string) (FoundBlock, error) {
	randomData := make([]byte, 73)
	if _, err := crand.Read(randomData); err != nil {
		return FoundBlock{}, err
	}

	const base58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
	minerAddress := make([]byte, 34)
	minerAddress[0] = '1'
	for index, value := range randomData[40:] {
		minerAddress[index+1] = base58Alphabet[int(value)%len(base58Alphabet)]
	}

	feeSats := binary.LittleEndian.Uint32(randomData[36:40]) % 5001
	return FoundBlock{
		Height: 999999,
		Hash:   hex.EncodeToString(randomData[:32]),
		Miner:  string(minerAddress),
		Worker: "S19-" + strings.ToUpper(hex.EncodeToString(randomData[32:36])),
		Reward: 3.125 + float64(feeSats)/100000,
		Symbol: symbol,
	}, nil
}

func (s *StratumServer) handleSubscribe(session *StratumSession, id interface{}, params []interface{}) {
	session.MarkSubscribed()
	subscriptions := []interface{}{
		[]interface{}{"mining.set_difficulty", fmt.Sprintf("%s_diff", session.ID)},
		[]interface{}{"mining.notify", fmt.Sprintf("%s_notify", session.ID)},
	}
	s.sendResponse(session, id, []interface{}{subscriptions, session.Extranonce1, session.Extranonce2Size}, nil)
	s.sendNotification(session, "mining.set_difficulty", []interface{}{session.Diff()})
}

func (s *StratumServer) handleAuthorize(session *StratumSession, id interface{}, params []interface{}) {
	fullUser := "unknown"
	if len(params) > 0 {
		fullUser, _ = params[0].(string)
	}

	workerName := "default"
	parts := strings.SplitN(fullUser, ".", 2)
	if len(parts) == 2 && parts[1] != "" {
		workerName = parts[1]
	} else if fullUser != "" {
		workerName = fullUser
	}

	session.Authorize(s.cfg.WalletAddress, workerName)
	s.sendResponse(session, id, true, nil)
	s.notifyStats()

	currentJob := s.jobManager.GetCurrentJob()
	if currentJob != nil {
		s.sendJobToSession(session, currentJob, true)
	}
}

func (s *StratumServer) handleSubmit(session *StratumSession, id interface{}, params []interface{}) {
	if !session.IsAuthorizedNow() || len(params) < 5 {
		s.sendResponse(session, id, false, map[string]interface{}{"code": 24, "message": "Unauthorized or invalid params"})
		return
	}

	jobId, _ := params[1].(string)
	extranonce2Hex, _ := params[2].(string)
	nTimeHex, _ := params[3].(string)
	nonceHex, _ := params[4].(string)
	var versionBitsHex interface{}
	if len(params) >= 6 {
		versionBitsHex = params[5]
	}

	if blocker := session.SubmitBlocker(); blocker != "" {
		session.RecordReject()
		s.logShareReject(session, jobId, nil, blocker, session.Diff(), 0, "")
		s.sendResponse(session, id, false, map[string]interface{}{"code": 24, "message": blocker})
		return
	}

	job := s.jobManager.GetJob(jobId)
	if job == nil {
		session.RecordReject()
		s.logShareReject(session, jobId, nil, "Stale / Job not found", session.Diff(), 0, "")
		s.sendResponse(session, id, false, map[string]interface{}{"code": 21, "message": "Stale / Job not found"})
		return
	}

	if session.IsDuplicateShare(shareKey(jobId, extranonce2Hex, nTimeHex, nonceHex, versionBitsHex)) {
		session.RecordReject()
		s.logShareReject(session, jobId, job, "Duplicate share", session.Diff(), 0, "")
		s.sendResponse(session, id, false, map[string]interface{}{"code": 22, "message": "Duplicate share"})
		return
	}

	requiredDiff, _ := session.EffectiveSubmitDiff(vardiffSubmitGraceWindow)
	minerTarget := crypto.DifficultyToTarget(requiredDiff)
	networkTarget := crypto.NbitsToTarget(job.NBitsHex)

	if job.TargetHex != "" {
		if t, err := hex.DecodeString(job.TargetHex); err == nil {
			networkTarget = new(big.Int).SetBytes(t)
		}
	}

	minerAddr := s.cfg.WalletAddress

	cb := pool.BuildCoinbaseTransaction(
		s.cfg,
		job.BlockHeight,
		job.CoinbaseValue,
		minerAddr,
		len(session.Extranonce1)/2,
		session.Extranonce2Size,
		job.DefaultWitnessCommitment,
	)

	ext2Candidates := s.getExt2Candidates(extranonce2Hex, session.Extranonce2Size)
	nTimeCandidates := s.getNTimeCandidates(nTimeHex, job.NTimeHex)
	nonceCandidates := s.getNonceCandidates(nonceHex)
	versionCandidates := s.getVersionCandidates(job.VersionHex, versionBitsHex, session.VersionRollingMask)

	accepted := false
	var finalShareDiff float64
	var matchedCoinbaseTxHex string
	var finalHeader []byte
	var finalHashBE []byte
	var finalHashBigInt *big.Int

primaryLoop:
	for _, ext2 := range ext2Candidates {
		cbTxHex := fmt.Sprintf("%s%s%s%s", cb.Coinb1, session.Extranonce1, ext2, cb.Coinb2)
		cbTxBytes, _ := hex.DecodeString(cbTxHex)
		cbTxIdLE := crypto.Sha256d(cbTxBytes)
		mRoot := crypto.CalculateMerkleRoot(cbTxIdLE, job.MerkleBranchHex)

		for _, ver := range versionCandidates {
			for _, nTime := range nTimeCandidates {
				for _, nonce := range nonceCandidates {
					header := crypto.BuildBlockHeader(ver, job.PrevHashRaw, mRoot, nTime, job.NBitsHex, nonce)
					headerHashLE := crypto.Sha256d(header)
					headerHashBE := crypto.ReverseBytes(headerHashLE)

					hashBigInt := new(big.Int).SetBytes(headerHashBE)

					if hashBigInt.Cmp(minerTarget) <= 0 {
						finalHeader = header
						finalHashBE = headerHashBE
						finalHashBigInt = hashBigInt
						finalShareDiff = crypto.HashToDifficulty(headerHashLE)
						accepted = true
						matchedCoinbaseTxHex = cbTxHex
						break primaryLoop
					}

					candDiff := crypto.HashToDifficulty(headerHashLE)
					if candDiff > finalShareDiff {
						finalHeader = header
						finalHashBE = headerHashBE
						finalHashBigInt = hashBigInt
						finalShareDiff = candDiff
					}
				}
			}
		}
	}

	if !accepted {
		session.RecordReject()
		s.logShareReject(session, jobId, job, "Low difficulty share", requiredDiff, finalShareDiff,
			fmt.Sprintf("candidates=ext2:%d ntime:%d nonce:%d version:%d",
				len(ext2Candidates), len(nTimeCandidates), len(nonceCandidates), len(versionCandidates)))
		s.sendResponse(session, id, false, map[string]interface{}{
			"code":    23,
			"message": fmt.Sprintf("Low difficulty share (Achieved diff %.2f < required %.2f)", finalShareDiff, requiredDiff),
		})
		return
	}

	newDiff, diffChanged := session.RecordShare(s.cfg, requiredDiff, finalShareDiff)
	s.logShareAccept(session, job, requiredDiff, finalShareDiff)

	s.sendResponse(session, id, true, nil)
	s.notifyStats()

	if diffChanged {
		s.sendNotification(session, "mining.set_difficulty", []interface{}{newDiff})
		s.sendJobToSession(session, job, false)
	}

	if finalHashBigInt != nil && finalHashBigInt.Cmp(networkTarget) <= 0 {
		blockHashHex := hex.EncodeToString(finalHashBE)
		minerAddr, workerName := session.Identity()
		log.Printf("  [BLOCK FOUND] Miner %s FOUND BLOCK #%d! Hash: %s", minerAddr, job.BlockHeight, blockHashHex)

		blockRecord := FoundBlock{
			Height:    job.BlockHeight,
			Hash:      blockHashHex,
			Miner:     minerAddr,
			Worker:    workerName,
			Timestamp: time.Now(),
			Reward:    float64(job.CoinbaseValue) / 1e8,
			Symbol:    s.cfg.CoinSymbol,
		}

		if s.bitcoinRpc == nil {
			log.Printf("[Block] RPC client is not configured, block #%d (%s) was not submitted and is not recorded", job.BlockHeight, blockHashHex)
			return
		}

		blockHex := s.buildFullBlockHex(finalHeader, matchedCoinbaseTxHex, job.TxsData)
		res, err := s.bitcoinRpc.SubmitBlock(blockHex)
		log.Printf("[RPC submitblock] Result: %v (err: %v)", res, err)

		if !s.isSubmitBlockAccepted(res, err) {
			log.Printf("[Block] Node did not accept block #%d (%s), result: %v err: %v - not recorded as found", job.BlockHeight, blockHashHex, res, err)
			return
		}

		s.recordFoundBlock(blockRecord)

		s.resetAllBestShares()
		s.notifyStats()
		go s.notifyBlockFound(blockRecord)
		go s.notifyDiscordBlockFound(blockRecord)
	}
}

// formatShareDiff renders a difficulty for logs: readable for fractional shares
// and free of noisy decimals for the large values a big farm runs at.
func formatShareDiff(diff float64) string {
	switch {
	case diff >= 1000:
		return fmt.Sprintf("%.0f", diff)
	case diff >= 1:
		return fmt.Sprintf("%.2f", diff)
	default:
		return fmt.Sprintf("%.6g", diff)
	}
}

// logShareReject records why a share was refused together with the difficulty
// the session required and the difficulty the share actually reached, so a
// rising reject rate can be traced to a worker, a job and a diff mismatch
// instead of only showing up as a percentage on the dashboard.
func (s *StratumServer) logShareReject(session *StratumSession, jobID string, job *pool.MiningJob, reason string, requiredDiff, achievedDiff float64, extra string) {
	_, workerName := session.Identity()
	workerName = fallbackWorker(workerName)
	jobAge := "n/a"
	height := int64(0)
	if job != nil {
		jobAge = time.Since(time.UnixMilli(job.CreatedTime)).Round(time.Millisecond).String()
		height = job.BlockHeight
	} else if stale := s.jobsBehind(jobID); stale != "" {
		extra = strings.TrimSpace(extra + " " + stale)
	}
	if extra != "" {
		extra = " " + extra
	}
	log.Printf("[Share] REJECT worker=%s ip=%s session=%s job=%s height=%d reason=%q requiredDiff=%s achievedDiff=%s jobAge=%s%s",
		workerName, session.IP, session.ID, jobID, height, reason,
		formatShareDiff(requiredDiff), formatShareDiff(achievedDiff), jobAge, extra)
}

// jobsBehind reports how many job generations a submission lags behind the
// current job, which exposes miners still hashing on outdated work.
func (s *StratumServer) jobsBehind(jobID string) string {
	current := s.jobManager.GetCurrentJob()
	if current == nil {
		return ""
	}
	submitted, err := strconv.ParseInt(jobID, 16, 64)
	if err != nil {
		return ""
	}
	latest, err := strconv.ParseInt(current.JobId, 16, 64)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("currentJob=%s jobsBehind=%d", current.JobId, latest-submitted)
}

// logShareAccept logs accepted shares with their difficulties when LOG_SHARES
// is enabled, which is enough to confirm what difficulty a miner really submits.
func (s *StratumServer) logShareAccept(session *StratumSession, job *pool.MiningJob, requiredDiff, achievedDiff float64) {
	if !s.cfg.ShareLog {
		return
	}
	_, workerName := session.Identity()
	workerName = fallbackWorker(workerName)
	log.Printf("[Share] accept worker=%s ip=%s session=%s job=%s height=%d requiredDiff=%s achievedDiff=%s",
		workerName, session.IP, session.ID, job.JobId, job.BlockHeight,
		formatShareDiff(requiredDiff), formatShareDiff(achievedDiff))
}

// fallbackWorker keeps log lines parseable before a session has authorized.
func fallbackWorker(name string) string {
	if name == "" {
		return "unknown"
	}
	return name
}

func (s *StratumServer) buildFullBlockHex(header []byte, coinbaseTxHex string, txsData []string) string {
	var buf bytes.Buffer
	buf.Write(header)

	txCount := uint64(1 + len(txsData))
	if txCount < 0xfd {
		buf.WriteByte(byte(txCount))
	} else if txCount <= 0xffff {
		buf.WriteByte(0xfd)
		b := make([]byte, 2)
		binary.LittleEndian.PutUint16(b, uint16(txCount))
		buf.Write(b)
	} else if txCount <= 0xffffffff {
		buf.WriteByte(0xfe)
		b := make([]byte, 4)
		binary.LittleEndian.PutUint32(b, uint32(txCount))
		buf.Write(b)
	} else {
		buf.WriteByte(0xff)
		b := make([]byte, 8)
		binary.LittleEndian.PutUint64(b, uint64(txCount))
		buf.Write(b)
	}

	cbBytes, _ := hex.DecodeString(coinbaseTxHex)
	buf.Write(cbBytes)

	for _, txHex := range txsData {
		txBytes, _ := hex.DecodeString(txHex)
		buf.Write(txBytes)
	}

	return hex.EncodeToString(buf.Bytes())
}

func (s *StratumServer) BroadcastJob(job *pool.MiningJob, cleanJobs bool) {
	// Snapshot the session list first so slow socket writes never block the
	// session map for the whole broadcast.
	for _, session := range s.GetActiveSessions() {
		if session.IsAuthorizedNow() {
			s.sendJobToSession(session, job, cleanJobs)
		}
	}
}

func (s *StratumServer) sendJobToSession(session *StratumSession, job *pool.MiningJob, cleanJobs bool) {
	minerAddr := s.cfg.WalletAddress

	cb := pool.BuildCoinbaseTransaction(
		s.cfg,
		job.BlockHeight,
		job.CoinbaseValue,
		minerAddr,
		len(session.Extranonce1)/2,
		session.Extranonce2Size,
		job.DefaultWitnessCommitment,
	)

	params := []interface{}{
		job.JobId,
		job.PrevHashStratum,
		cb.Coinb1,
		cb.Coinb2,
		job.MerkleBranchHex,
		job.VersionHex,
		job.NBitsHex,
		job.NTimeHex,
		cleanJobs,
	}

	s.sendNotification(session, "mining.notify", params)
}

// -------------------------------------------------------------------------
// Helper: สลับไบต์ (Byte Permutations) ให้ครอบคลุมการสลับแบบผิดปกติของ ASIC
// -------------------------------------------------------------------------
func (s *StratumServer) getBytePermutations(hexStr string) []string {
	if len(hexStr)%2 != 0 {
		return []string{hexStr}
	}

	candidatesMap := make(map[string]bool)
	candidatesMap[hexStr] = true

	b, err := hex.DecodeString(hexStr)
	if err == nil {
		candidatesMap[hex.EncodeToString(crypto.ReverseBytes(b))] = true
	}

	if len(hexStr) == 8 { // กรณี 4 bytes (8 ตัวอักษร)
		b0, b1, b2, b3 := hexStr[0:2], hexStr[2:4], hexStr[4:6], hexStr[6:8]
		candidatesMap[b3+b2+b1+b0] = true // Full Reverse
		candidatesMap[b2+b3+b0+b1] = true // Word Swap (Avalon)
		candidatesMap[b1+b0+b3+b2] = true // Byte swap in word (BM1370)
		candidatesMap[b0+b1+b3+b2] = true
		candidatesMap[b3+b2+b0+b1] = true
		candidatesMap[b1+b0+b2+b3] = true
	}

	var results []string
	for k := range candidatesMap {
		results = append(results, k)
	}
	return results
}

func (s *StratumServer) getVersionCandidates(jobVersionHex string, versionBitsHex interface{}, sessionMaskHex string) []uint32 {
	baseVersion64, _ := strconv.ParseUint(jobVersionHex, 16, 32)
	baseVersion := uint32(baseVersion64)

	mask64, err := strconv.ParseUint(sessionMaskHex, 16, 32)
	mask := uint32(0x1fffe000)
	if err == nil {
		mask = uint32(mask64)
	}

	candidatesMap := map[uint32]bool{baseVersion: true}

	if versionBitsHex != nil {
		var rawBitsNum uint32
		var parsed bool

		switch v := versionBitsHex.(type) {
		case float64:
			rawBitsNum = uint32(v)
			parsed = true
		case string:
			p64, err := strconv.ParseUint(strings.TrimSpace(v), 16, 32)
			if err == nil {
				rawBitsNum = uint32(p64)
				parsed = true
			}
		}

		if parsed {
			hexStr := fmt.Sprintf("%08x", rawBitsNum)
			for _, p := range s.getBytePermutations(hexStr) {
				if pVal64, err := strconv.ParseUint(p, 16, 32); err == nil {
					pVal := uint32(pVal64)
					candidatesMap[(baseVersion&^mask)|(pVal&mask)] = true
					candidatesMap[baseVersion|pVal] = true
					candidatesMap[baseVersion^pVal] = true
				}
			}
		}
	}

	var candidates []uint32
	for c := range candidatesMap {
		candidates = append(candidates, c)
	}
	return candidates
}

func (s *StratumServer) getExt2Candidates(ext2Hex string, ext2Size int) []string {
	targetLen := ext2Size * 2
	paddedHex := ext2Hex
	if len(ext2Hex) < targetLen {
		paddedHex = strings.Repeat("0", targetLen-len(ext2Hex)) + ext2Hex
	}

	candidatesMap := make(map[string]bool)
	for _, p := range s.getBytePermutations(paddedHex) {
		candidatesMap[p] = true
	}

	// เพิ่มแบบต่อ 0 ข้างหลังในกรณี Extranonce2 มาไม่ครบ
	if len(ext2Hex) < targetLen {
		rightPadded := ext2Hex + strings.Repeat("0", targetLen-len(ext2Hex))
		for _, p := range s.getBytePermutations(rightPadded) {
			candidatesMap[p] = true
		}
	}

	var candidates []string
	for c := range candidatesMap {
		candidates = append(candidates, c)
	}
	return candidates
}

func (s *StratumServer) getNTimeCandidates(nTimeHex string, jobNTimeHex string) []string {
	candidatesMap := make(map[string]bool)

	for _, p := range s.getBytePermutations(nTimeHex) {
		candidatesMap[p] = true
		// รองรับการ Rolling เวลา (nTime) ของแต่ละรูปแบบ
		if pInt, err := strconv.ParseUint(p, 16, 32); err == nil {
			for offset := -5; offset <= 5; offset++ {
				val := uint32(int64(pInt) + int64(offset))
				candidatesMap[fmt.Sprintf("%08x", val)] = true
			}
		}
	}

	for _, p := range s.getBytePermutations(jobNTimeHex) {
		candidatesMap[p] = true
	}

	var candidates []string
	for c := range candidatesMap {
		candidates = append(candidates, c)
	}
	return candidates
}

func (s *StratumServer) getNonceCandidates(nonceHex string) []string {
	padded := nonceHex
	if len(nonceHex) < 8 {
		padded = strings.Repeat("0", 8-len(nonceHex)) + nonceHex
	}
	candidatesMap := make(map[string]bool)

	for _, p := range s.getBytePermutations(padded) {
		candidatesMap[p] = true
	}

	var candidates []string
	for c := range candidatesMap {
		candidates = append(candidates, c)
	}
	return candidates
}

func (s *StratumServer) GetActiveSessions() []*StratumSession {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var list []*StratumSession
	for _, sess := range s.sessions {
		list = append(list, sess)
	}
	return list
}

// GetSessionSnapshots returns consistent value copies of every live session.
func (s *StratumServer) GetSessionSnapshots() []SessionSnapshot {
	sessions := s.GetActiveSessions()
	snapshots := make([]SessionSnapshot, 0, len(sessions))
	for _, session := range sessions {
		snapshots = append(snapshots, session.Snapshot())
	}
	return snapshots
}
