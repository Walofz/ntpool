package stratum

import (
	"bufio"
	"bytes"
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

type StratumServer struct {
	mu             sync.RWMutex
	cfg            *config.Config
	jobManager     *pool.JobManager
	bitcoinRpc     *bitcoin.BitcoinRpcClient
	sessions       map[string]*StratumSession
	sessionCounter int64
	FoundBlocks    []FoundBlock
	StatsUpdated   chan struct{}
	blocksFilePath string
}

func NewStratumServer(cfg *config.Config, jm *pool.JobManager, rpc *bitcoin.BitcoinRpcClient) *StratumServer {
	s := &StratumServer{
		cfg:            cfg,
		jobManager:     jm,
		bitcoinRpc:     rpc,
		sessions:       make(map[string]*StratumSession),
		StatsUpdated:   make(chan struct{}, 100),
		blocksFilePath: filepath.Join(".", "data", "found_blocks.json"),
	}
	s.loadFoundBlocks()
	return s
}

func (s *StratumServer) loadFoundBlocks() {
	s.mu.Lock()
	defer s.mu.Unlock()

	dir := filepath.Dir(s.blocksFilePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return
	}

	if data, err := os.ReadFile(s.blocksFilePath); err == nil {
		var blocks []FoundBlock
		if err := json.Unmarshal(data, &blocks); err == nil {
			s.FoundBlocks = blocks
			log.Printf("[Stratum] Loaded %d persisted found blocks from disk.", len(blocks))
			return
		}
	}

	s.FoundBlocks = []FoundBlock{}
	initialData, _ := json.MarshalIndent(s.FoundBlocks, "", "  ")
	_ = os.WriteFile(s.blocksFilePath, initialData, 0644)
	log.Printf("[Stratum] Initialized new found_blocks.json file at %s", s.blocksFilePath)
}

func (s *StratumServer) saveFoundBlocks() {
	s.mu.RLock()
	defer s.mu.RUnlock()

	dir := filepath.Dir(s.blocksFilePath)
	_ = os.MkdirAll(dir, 0755)

	data, err := json.MarshalIndent(s.FoundBlocks, "", "  ")
	if err == nil {
		_ = os.WriteFile(s.blocksFilePath, data, 0644)
	}
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

func (s *StratumServer) notifyBlockFound(block FoundBlock) {
	if s.cfg.NtfyServer == "" || s.cfg.NtfyTopic == "" {
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

	endpoint := strings.TrimRight(s.cfg.NtfyServer, "/") + "/" + strings.TrimLeft(s.cfg.NtfyTopic, "/")
	req, err := http.NewRequest("POST", endpoint, strings.NewReader(body))
	if err != nil {
		log.Printf("[ntfy] Failed to create request: %v", err)
		return
	}

	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	req.Header.Set("Title", fmt.Sprintf("FOUND SOLO %s", symbol))
	req.Header.Set("Priority", "urgent")
	req.Header.Set("Tags", "pickaxe,rotating_light")
	if s.cfg.NtfyUser != "" || s.cfg.NtfyPassword != "" {
		req.SetBasicAuth(s.cfg.NtfyUser, s.cfg.NtfyPassword)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("[ntfy] Failed to send block notification: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		log.Printf("[ntfy] Notification rejected: %s %s", resp.Status, strings.TrimSpace(string(respBody)))
		return
	}

	log.Printf("[ntfy] Block notification sent to %s", endpoint)
}

func (s *StratumServer) isSubmitBlockAccepted(result interface{}, err error) bool {
	if err != nil {
		return false
	}

	if result == nil {
		return true
	}

	if resultStr, ok := result.(string); ok {
		return strings.TrimSpace(resultStr) == ""
	}

	return false
}

func (s *StratumServer) Start() error {
	addr := fmt.Sprintf(":%d", s.cfg.StratumPort)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	log.Printf("[Stratum Go] Server listening on %s", addr)

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				continue
			}
			go s.handleConnection(conn)
		}
	}()

	return nil
}

func (s *StratumServer) handleConnection(conn net.Conn) {
	s.mu.Lock()
	s.sessionCounter++
	sessionId := fmt.Sprintf("%08x", s.sessionCounter)
	extranonce1 := fmt.Sprintf("%08x", s.sessionCounter&0xffffffff)
	session := NewStratumSession(sessionId, conn, extranonce1, s.cfg.DefaultDiff)
	s.sessions[sessionId] = session
	s.mu.Unlock()

	defer func() {
		conn.Close()
		s.mu.Lock()
		delete(s.sessions, sessionId)
		s.mu.Unlock()
		s.notifyStats()
	}()

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if len(line) > 0 {
			s.handleMessage(session, line)
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("[Stratum] Session %s scanner error: %v", session.ID, err)
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
	case "mining.suggest_difficulty", "mining.suggest_target", "mining.extranonce.subscribe":
		s.sendResponse(session, id, true, nil)
	default:
		s.sendResponse(session, id, nil, map[string]interface{}{"code": -32601, "message": "Method not found"})
	}
}

func (s *StratumServer) handleConfigure(session *StratumSession, id interface{}, params []interface{}) {
	result := make(map[string]interface{})
	session.VersionRollingEnabled = true
	result["version-rolling"] = true
	result["version-rolling.mask"] = session.VersionRollingMask
	s.sendResponse(session, id, result, nil)
}

func (s *StratumServer) handleSubscribe(session *StratumSession, id interface{}, params []interface{}) {
	session.IsSubscribed = true
	subscriptions := []interface{}{
		[]interface{}{"mining.set_difficulty", fmt.Sprintf("%s_diff", session.ID)},
		[]interface{}{"mining.notify", fmt.Sprintf("%s_notify", session.ID)},
	}
	s.sendResponse(session, id, []interface{}{subscriptions, session.Extranonce1, session.Extranonce2Size}, nil)
	s.sendNotification(session, "mining.set_difficulty", []interface{}{session.CurrentDiff})
}

func (s *StratumServer) handleAuthorize(session *StratumSession, id interface{}, params []interface{}) {
	fullUser := "unknown"
	if len(params) > 0 {
		fullUser, _ = params[0].(string)
	}

	parts := strings.SplitN(fullUser, ".", 2)
	if len(parts) == 2 && parts[1] != "" {
		session.WorkerName = parts[1]
	} else if fullUser != "" {
		session.WorkerName = fullUser
	} else {
		session.WorkerName = "default"
	}
	session.MinerAddress = s.cfg.WalletAddress

	session.IsAuthorized = true
	s.sendResponse(session, id, true, nil)
	s.notifyStats()

	currentJob := s.jobManager.GetCurrentJob()
	if currentJob != nil {
		s.sendJobToSession(session, currentJob, true)
	}
}

func (s *StratumServer) handleSubmit(session *StratumSession, id interface{}, params []interface{}) {
	if !session.IsAuthorized || len(params) < 5 {
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

	job := s.jobManager.GetJob(jobId)
	if job == nil {
		session.RejectedShares++
		log.Printf("[Share REJECTED] Worker: %s, Reason: stale job, JobID: %s", session.WorkerName, jobId)
		s.sendResponse(session, id, false, map[string]interface{}{"code": 21, "message": "Stale / Job not found"})
		return
	}

	requiredDiff, currentDiff := session.EffectiveSubmitDiff(vardiffSubmitGraceWindow)
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
		session.RejectedShares++
		log.Printf("[Share REJECTED] Worker: %s, Reason: low diff, Achieved: %.2f, Required: %.2f (Current: %.2f)", session.WorkerName, finalShareDiff, requiredDiff, currentDiff)
		s.sendResponse(session, id, false, map[string]interface{}{
			"code":    23,
			"message": fmt.Sprintf("Low difficulty share (Achieved diff %.2f < required %.2f)", finalShareDiff, requiredDiff),
		})
		return
	}

	log.Printf("[Share ACCEPTED] Worker: %s, Achieved Diff: %.2f, Required Diff: %.2f (Current: %.2f)", session.WorkerName, finalShareDiff, requiredDiff, currentDiff)

	newDiff, diffChanged := session.RecordShare(s.cfg, requiredDiff, finalShareDiff)

	s.sendResponse(session, id, true, nil)
	s.notifyStats()

	if diffChanged {
		s.sendNotification(session, "mining.set_difficulty", []interface{}{newDiff})
		s.sendJobToSession(session, job, false)
	}

	if finalHashBigInt != nil && finalHashBigInt.Cmp(networkTarget) <= 0 {
		blockHashHex := hex.EncodeToString(finalHashBE)
		log.Printf("  [BLOCK FOUND] Miner %s FOUND BLOCK #%d! Hash: %s", session.MinerAddress, job.BlockHeight, blockHashHex)

		blockRecord := FoundBlock{
			Height:    job.BlockHeight,
			Hash:      blockHashHex,
			Miner:     session.MinerAddress,
			Worker:    session.WorkerName,
			Timestamp: time.Now(),
			Reward:    float64(job.CoinbaseValue) / 1e8,
			Symbol:    s.cfg.CoinSymbol,
		}

		s.mu.Lock()
		s.FoundBlocks = append([]FoundBlock{blockRecord}, s.FoundBlocks...)
		s.mu.Unlock()
		s.saveFoundBlocks()

		blockHex := s.buildFullBlockHex(finalHeader, matchedCoinbaseTxHex, job.TxsData)
		if s.bitcoinRpc != nil {
			res, err := s.bitcoinRpc.SubmitBlock(blockHex)
			log.Printf("[RPC submitblock] Result: %v (err: %v)", res, err)
			if s.isSubmitBlockAccepted(res, err) {
				s.resetAllBestShares()
				s.notifyStats()
				go s.notifyBlockFound(blockRecord)
			}
		}
	}
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
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, session := range s.sessions {
		if session.IsAuthorized {
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
