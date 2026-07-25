package stratum

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"net"
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

	// If file does not exist, initialize empty array file
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
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if len(line) > 0 {
			s.handleMessage(session, line)
		}
	}
}

func (s *StratumServer) sendResponse(session *StratumSession, id interface{}, result interface{}, errObj interface{}) {
	resp := map[string]interface{}{
		"id":     id,
		"result": result,
		"error":  errObj,
	}
	data, _ := json.Marshal(resp)
	data = append(data, '\n')
	session.Conn.Write(data)
}

func (s *StratumServer) sendNotification(session *StratumSession, method string, params []interface{}) {
	notif := map[string]interface{}{
		"id":     nil,
		"method": method,
		"params": params,
	}
	data, _ := json.Marshal(notif)
	data = append(data, '\n')
	session.Conn.Write(data)
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

	parts := strings.Split(fullUser, ".")
	session.MinerAddress = parts[0]
	if len(parts) > 1 {
		session.WorkerName = parts[1]
	} else {
		session.WorkerName = "default"
	}
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
		s.sendResponse(session, id, false, map[string]interface{}{"code": 21, "message": "Stale / Job not found"})
		return
	}

	minerTarget := crypto.DifficultyToTarget(session.CurrentDiff)

	networkTarget := crypto.NbitsToTarget(job.NBitsHex)
	if job.TargetHex != "" {
		if t, err := hex.DecodeString(job.TargetHex); err == nil {
			networkTarget = new(big.Int).SetBytes(t)
		}
	}

	minerAddr := session.MinerAddress
	if minerAddr == "" {
		minerAddr = s.cfg.WalletAddress
	}

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
		s.sendResponse(session, id, false, map[string]interface{}{
			"code":    23,
			"message": fmt.Sprintf("Low difficulty share (Achieved diff %.2f < required %.2f)", finalShareDiff, session.CurrentDiff),
		})
		return
	}

	log.Printf("[Share ACCEPTED] Worker: %s, Achieved Diff: %.2f, Required Diff: %.2f", session.WorkerName, finalShareDiff, session.CurrentDiff)

	newDiff, diffChanged := session.RecordShare(s.cfg, session.CurrentDiff, finalShareDiff)
	s.sendResponse(session, id, true, nil)
	s.notifyStats()

	if diffChanged {
		s.sendNotification(session, "mining.set_difficulty", []interface{}{newDiff})
	}

	// CHECK IF THIS SHARE FOUND A NETWORK BLOCK! 🎉
	if finalHashBigInt != nil && finalHashBigInt.Cmp(networkTarget) <= 0 {
		blockHashHex := hex.EncodeToString(finalHashBE)
		log.Printf("🎉🎉🎉 [BLOCK FOUND] Miner %s FOUND BLOCK #%d! Hash: %s", session.MinerAddress, job.BlockHeight, blockHashHex)

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
	minerAddr := session.MinerAddress
	if minerAddr == "" {
		minerAddr = s.cfg.WalletAddress
	}

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
			candidatesMap[(baseVersion&^mask)|(rawBitsNum&mask)] = true
			candidatesMap[(baseVersion&^mask)|rawBitsNum] = true
			candidatesMap[baseVersion|rawBitsNum] = true
			candidatesMap[baseVersion^rawBitsNum] = true

			buf32 := make([]byte, 4)
			binary.BigEndian.PutUint32(buf32, rawBitsNum)
			swapped32 := binary.LittleEndian.Uint32(buf32)
			candidatesMap[(baseVersion&^mask)|(swapped32&mask)] = true
		}
	}

	var candidates []uint32
	for c := range candidatesMap {
		candidates = append(candidates, c)
	}
	return candidates
}

func (s *StratumServer) getExt2Candidates(ext2Hex string, ext2Size int) []string {
	candidatesMap := map[string]bool{ext2Hex: true}
	targetLen := ext2Size * 2
	candidatesMap[fmt.Sprintf("%0*s", targetLen, ext2Hex)] = true

	b, err := hex.DecodeString(ext2Hex)
	if err == nil {
		candidatesMap[hex.EncodeToString(crypto.ReverseBytes(b))] = true
	}

	if len(ext2Hex) == 8 {
		wordSwapped := ext2Hex[4:] + ext2Hex[:4]
		candidatesMap[wordSwapped] = true
	}

	var candidates []string
	for c := range candidatesMap {
		candidates = append(candidates, c)
	}
	return candidates
}

func (s *StratumServer) getNTimeCandidates(nTimeHex string, jobNTimeHex string) []string {
	candidatesMap := map[string]bool{nTimeHex: true, jobNTimeHex: true}

	b1, err1 := hex.DecodeString(nTimeHex)
	if err1 == nil {
		candidatesMap[hex.EncodeToString(crypto.ReverseBytes(b1))] = true
	}
	b2, err2 := hex.DecodeString(jobNTimeHex)
	if err2 == nil {
		candidatesMap[hex.EncodeToString(crypto.ReverseBytes(b2))] = true
	}

	nTimeInt, err := strconv.ParseUint(nTimeHex, 16, 32)
	if err == nil {
		for offset := -5; offset <= 5; offset++ {
			val := uint32(int64(nTimeInt) + int64(offset))
			candidatesMap[fmt.Sprintf("%08x", val)] = true
		}
	}

	var candidates []string
	for c := range candidatesMap {
		candidates = append(candidates, c)
	}
	return candidates
}

func (s *StratumServer) getNonceCandidates(nonceHex string) []string {
	padded := fmt.Sprintf("%08s", nonceHex)
	candidatesMap := map[string]bool{padded: true, nonceHex: true}

	b, err := hex.DecodeString(padded)
	if err == nil {
		candidatesMap[hex.EncodeToString(crypto.ReverseBytes(b))] = true
	}

	if len(padded) == 8 {
		// 16-bit word swap (first 2 bytes <-> last 2 bytes) for Avalon Nano 3 / Canaan
		wordSwapped := padded[4:] + padded[:4]
		candidatesMap[wordSwapped] = true
		wsBytes, err := hex.DecodeString(wordSwapped)
		if err == nil {
			candidatesMap[hex.EncodeToString(crypto.ReverseBytes(wsBytes))] = true
		}
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
