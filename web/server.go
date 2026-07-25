package web

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

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
	clients       map[*websocket.Conn]bool
	mu            sync.Mutex
}

func NewWebDashboardServer(cfg *config.Config, stratumServer *stratum.StratumServer, jm *pool.JobManager) *WebDashboardServer {
	return &WebDashboardServer{
		cfg:           cfg,
		stratumServer: stratumServer,
		jobManager:    jm,
		clients:       make(map[*websocket.Conn]bool),
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				origin := r.Header.Get("Origin")
				if origin == "" {
					return true
				}

				u, err := url.Parse(origin)
				if err != nil {
					return false
				}

				return strings.EqualFold(u.Host, r.Host)
			},
		},
	}
}

func isLANClient(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}

	ip := net.ParseIP(strings.TrimSpace(host))
	if ip == nil {
		return false
	}

	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
		return true
	}

	return false
}

func lanOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if !isLANClient(r.RemoteAddr) {
			http.Error(rw, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(rw, r)
	})
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

	// Sort ASIC Workers alphabetically by WorkerName (case-insensitive)
	sort.Slice(workersList, func(i, j int) bool {
		w1, _ := workersList[i]["workerName"].(string)
		w2, _ := workersList[j]["workerName"].(string)
		return strings.ToLower(w1) < strings.ToLower(w2)
	})

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

func (w *WebDashboardServer) startBroadcaster() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		statsData, err := json.Marshal(w.calculatePoolStats())
		if err != nil {
			continue
		}

		w.mu.Lock()
		for client := range w.clients {
			err := client.WriteMessage(websocket.TextMessage, statsData)
			if err != nil {
				client.Close()
				delete(w.clients, client)
			}
		}
		w.mu.Unlock()
	}
}

func (w *WebDashboardServer) Start() error {
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.Dir("./public")))

	mux.HandleFunc("/api/stats", func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		json.NewEncoder(rw).Encode(w.calculatePoolStats())
	})

	mux.HandleFunc("/ws", func(rw http.ResponseWriter, r *http.Request) {
		conn, err := w.upgrader.Upgrade(rw, r, nil)
		if err != nil {
			return
		}

		w.mu.Lock()
		w.clients[conn] = true
		w.mu.Unlock()

		defer func() {
			w.mu.Lock()
			delete(w.clients, conn)
			w.mu.Unlock()
			conn.Close()
		}()

		// Send initial stats
		initialData, _ := json.Marshal(w.calculatePoolStats())
		_ = conn.WriteMessage(websocket.TextMessage, initialData)

		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				break
			}
		}
	})

	go w.startBroadcaster()

	addr := fmt.Sprintf(":%d", w.cfg.WebPort)
	log.Printf("[Web Dashboard Go] Server running on LAN at http://<LAN_IP>:%d", w.cfg.WebPort)
	return http.ListenAndServe(addr, lanOnly(mux))
}
