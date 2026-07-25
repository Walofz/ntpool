package web

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"path/filepath"

	"github.com/gorilla/websocket"

	"ntpool/config"
	"ntpool/crypto"
	"ntpool/pool"
	"ntpool/stratum"
)

type WebDashboardServer struct {
	cfg           *config.Config
	stratumServer *stratum.StratumServer
	jobManager    *pool.JobManager
	upgrader      websocket.Upgrader
}

func NewWebDashboardServer(cfg *config.Config, stratumServer *stratum.StratumServer, jm *pool.JobManager) *WebDashboardServer {
	return &WebDashboardServer{
		cfg:           cfg,
		stratumServer: stratumServer,
		jobManager:    jm,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}
}

func (w *WebDashboardServer) calculatePoolStats() map[string]interface{} {
	sessions := w.stratumServer.GetActiveSessions()
	currentJob := w.jobManager.GetCurrentJob()

	uniqueMiners := make(map[string]bool)
	var workersList []map[string]interface{}

	var poolHashrate1m float64
	var poolHashrate5m float64

	for _, s := range sessions {
		uniqueMiners[s.MinerAddress] = true
		h1m := s.GetHashrate(60)
		h5m := s.GetHashrate(300)

		poolHashrate1m += h1m
		poolHashrate5m += h5m

		workersList = append(workersList, map[string]interface{}{
			"address":       s.MinerAddress,
			"workerName":    s.WorkerName,
			"difficulty":    s.CurrentDiff,
			"hashrate1m":    h1m,
			"hashrate5m":    h5m,
			"asicboost":     s.VersionRollingEnabled,
			"bestShareDiff": s.BestShareDiff,
		})
	}

	blockHeight := int64(0)
	netDiff := 0.0
	if currentJob != nil {
		blockHeight = currentJob.BlockHeight
		if currentJob.NBitsHex != "" {
			netDiff = crypto.NbitsToDifficulty(currentJob.NBitsHex)
		}
	}

	return map[string]interface{}{
		"poolName":          w.cfg.PoolName,
		"stratumPort":       w.cfg.StratumPort,
		"network":           w.cfg.RpcNetwork,
		"coinSymbol":        w.cfg.CoinSymbol,
		"blockHeight":       blockHeight,
		"networkDifficulty": netDiff,
		"activeMiners":      len(uniqueMiners),
		"connectedWorkers":  len(sessions),
		"poolHashrate1m":    poolHashrate1m,
		"poolHashrate5m":    poolHashrate5m,
		"blocksFound":       w.stratumServer.FoundBlocks,
		"workers":           workersList,
	}
}

func (w *WebDashboardServer) Start() error {
	http.Handle("/", http.FileServer(http.Dir("./public")))

	http.HandleFunc("/api/stats", func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		json.NewEncoder(rw).Encode(w.calculatePoolStats())
	})

	http.HandleFunc("/ws", func(rw http.ResponseWriter, r *http.Request) {
		conn, err := w.upgrader.Upgrade(rw, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Send initial stats
		initialData, _ := json.Marshal(w.calculatePoolStats())
		conn.WriteMessage(websocket.TextMessage, initialData)

		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				break
			}
		}
	})

	addr := fmt.Sprintf(":%d", w.cfg.WebPort)
	log.Printf("[Web Dashboard Go] Server running at http://localhost:%d", w.cfg.WebPort)
	return http.ListenAndServe(addr, nil)
}
