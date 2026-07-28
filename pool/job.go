package pool

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"sync"
	"time"

	"zpoolproxy/config"
	"zpoolproxy/crypto"
)

type MiningJob struct {
	JobId                    string   `json:"jobId"`
	BlockHeight              int64    `json:"blockHeight"`
	CoinbaseValue            int64    `json:"coinbaseValue"`
	PrevHashStratum          string   `json:"prevHashStratum"`
	PrevHashRaw              string   `json:"prevHashRaw"`
	VersionHex               string   `json:"versionHex"`
	VersionMaskHex           string   `json:"versionMaskHex"`
	NBitsHex                 string   `json:"nBitsHex"`
	NTimeHex                 string   `json:"nTimeHex"`
	MerkleBranchHex          []string `json:"merkleBranchHex"`
	TxsData                  []string `json:"txsData"`
	DefaultWitnessCommitment string   `json:"defaultWitnessCommitment,omitempty"`
	TargetHex                string   `json:"targetHex,omitempty"`
	Coinb1                   string   `json:"coinb1"`
	Coinb2                   string   `json:"coinb2"`
	CreatedTime              int64    `json:"createdTime"`
}

type JobManager struct {
	mu         sync.RWMutex
	cfg        *config.Config
	currentJob *MiningJob
	jobMap     map[string]*MiningJob
	jobCounter int64
}

func NewJobManager(cfg *config.Config) *JobManager {
	return &JobManager{
		cfg:    cfg,
		jobMap: make(map[string]*MiningJob),
	}
}

func (jm *JobManager) FormatPrevHashForStratum(prevHashHex string) string {
	b, err := hex.DecodeString(prevHashHex)
	if err != nil {
		return prevHashHex
	}
	bLE := crypto.ReverseBytes(b)
	for i := 0; i < len(bLE); i += 4 {
		t0 := bLE[i]
		t1 := bLE[i+1]
		bLE[i] = bLE[i+3]
		bLE[i+1] = bLE[i+2]
		bLE[i+2] = t1
		bLE[i+3] = t0
	}
	return hex.EncodeToString(bLE)
}

func (jm *JobManager) CalculateMerkleBranch(txs []map[string]interface{}) []string {
	if len(txs) == 0 {
		return []string{}
	}

	var hashes [][]byte
	for _, tx := range txs {
		if hashStr, ok := tx["hash"].(string); ok && hashStr != "" {
			b, err := hex.DecodeString(hashStr)
			if err == nil {
				hashes = append(hashes, crypto.ReverseBytes(b))
				continue
			}
		} else if txidStr, ok := tx["txid"].(string); ok && txidStr != "" {
			b, err := hex.DecodeString(txidStr)
			if err == nil {
				hashes = append(hashes, crypto.ReverseBytes(b))
				continue
			}
		} else if dataStr, ok := tx["data"].(string); ok && dataStr != "" {
			b, err := hex.DecodeString(dataStr)
			if err == nil {
				hashes = append(hashes, crypto.Sha256d(b))
				continue
			}
		}
	}

	var branch []string

	for len(hashes) > 0 {
		branch = append(branch, hex.EncodeToString(hashes[0]))

		if len(hashes) == 1 {
			break
		}

		// Correct Merkle Branch slicing fix: slice out index 0 (which was added to branch)
		remaining := hashes[1:]
		var nextLevel [][]byte
		for i := 0; i < len(remaining); i += 2 {
			left := remaining[i]
			right := left
			if i+1 < len(remaining) {
				right = remaining[i+1]
			}
			combined := append(left, right...)
			nextLevel = append(nextLevel, crypto.Sha256d(combined))
		}
		hashes = nextLevel
	}

	return branch
}

func (jm *JobManager) CreateJob(template map[string]interface{}) *MiningJob {
	jm.mu.Lock()
	defer jm.mu.Unlock()

	jm.jobCounter++
	jobId := fmt.Sprintf("%04x", jm.jobCounter)

	height, _ := template["height"].(float64)
	coinbaseval, _ := template["coinbasevalue"].(float64)
	prevblockhash, _ := template["previousblockhash"].(string)
	version, _ := template["version"].(float64)
	bits, _ := template["bits"].(string)
	curtime, _ := template["curtime"].(float64)
	target, _ := template["target"].(string)
	witnessCommitment, _ := template["default_witness_commitment"].(string)

	var txsData []string
	var rawTxs []map[string]interface{}

	if txList, ok := template["transactions"].([]interface{}); ok {
		for _, item := range txList {
			if txMap, ok := item.(map[string]interface{}); ok {
				rawTxs = append(rawTxs, txMap)
				if data, ok := txMap["data"].(string); ok {
					txsData = append(txsData, data)
				}
			}
		}
	}

	prevHashStratum := jm.FormatPrevHashForStratum(prevblockhash)
	merkleBranchHex := jm.CalculateMerkleBranch(rawTxs)
	versionHex := fmt.Sprintf("%08x", uint32(version))
	nBitsHex := bits
	nTimeHex := fmt.Sprintf("%08x", uint32(curtime))

	minerAddr := jm.cfg.WalletAddress
	if minerAddr == "" {
		minerAddr = jm.cfg.PoolFeeAddress
	}

	cbParts := BuildCoinbaseTransaction(
		jm.cfg,
		int64(height),
		int64(coinbaseval),
		minerAddr,
		4,
		4,
		witnessCommitment,
	)

	job := &MiningJob{
		JobId:                    jobId,
		BlockHeight:              int64(height),
		CoinbaseValue:            int64(coinbaseval),
		PrevHashStratum:          prevHashStratum,
		PrevHashRaw:              prevblockhash,
		VersionHex:               versionHex,
		VersionMaskHex:           "1fffe000",
		NBitsHex:                 nBitsHex,
		NTimeHex:                 nTimeHex,
		MerkleBranchHex:          merkleBranchHex,
		TxsData:                  txsData,
		DefaultWitnessCommitment: witnessCommitment,
		TargetHex:                target,
		Coinb1:                   cbParts.Coinb1,
		Coinb2:                   cbParts.Coinb2,
		CreatedTime:              time.Now().UnixMilli(),
	}

	jm.currentJob = job
	jm.jobMap[jobId] = job

	for len(jm.jobMap) > 200 {
		oldestID := ""
		oldestCounter := int64(1<<63 - 1)

		for k := range jm.jobMap {
			if n, err := strconv.ParseInt(k, 16, 64); err == nil {
				if n < oldestCounter {
					oldestCounter = n
					oldestID = k
				}
			}
		}

		if oldestID == "" {
			break
		}
		delete(jm.jobMap, oldestID)
	}

	return job
}

func (jm *JobManager) GetCurrentJob() *MiningJob {
	jm.mu.RLock()
	defer jm.mu.RUnlock()
	return jm.currentJob
}

func (jm *JobManager) GetJob(jobId string) *MiningJob {
	jm.mu.RLock()
	defer jm.mu.RUnlock()
	return jm.jobMap[jobId]
}
