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

	"ntpool/bitcoin"
	"ntpool/config"
	"ntpool/crypto"
	"ntpool/pool"
	"ntpool/stratum"
)

type WebDashboardServer struct {
	cfg           *config.Config
	stratumServer *stratum.StratumServer
	jobManager    *pool.JobManager
	bitcoinRpc    *bitcoin.BitcoinRpcClient
	zmqSub        *bitcoin.ZmqBlockSubscriber
	upgrader      websocket.Upgrader
	clients       map[*websocket.Conn]bool
	healthHistory []map[string]interface{}
	historyMu     sync.Mutex
	mu            sync.Mutex
}

func buildHealthTimelineEntry(rpcHealthy, zmqHealthy bool, connectedWorkers, alertCount int) map[string]interface{} {
	overall := "online"
	switch {
	case !rpcHealthy && !zmqHealthy:
		overall = "offline"
	case !rpcHealthy || !zmqHealthy || alertCount > 0:
		overall = "degraded"
	}

	return map[string]interface{}{
		"ts":               time.Now().Format(time.RFC3339),
		"overall":          overall,
		"rpcHealthy":       rpcHealthy,
		"zmqHealthy":       zmqHealthy,
		"connectedWorkers": connectedWorkers,
		"alerts":           alertCount,
	}
}

func buildPoolActivityEvent(severity, title, detail string) map[string]interface{} {
	return map[string]interface{}{
		"ts":       time.Now().Format(time.RFC3339),
		"severity": severity,
		"title":    title,
		"detail":   detail,
	}
}

func (w *WebDashboardServer) recordHealthSnapshot(entry map[string]interface{}) {
	w.historyMu.Lock()
	defer w.historyMu.Unlock()

	w.healthHistory = append(w.healthHistory, entry)
	if len(w.healthHistory) > 12 {
		w.healthHistory = w.healthHistory[len(w.healthHistory)-12:]
	}
}

func (w *WebDashboardServer) recentHealthHistory() []map[string]interface{} {
	w.historyMu.Lock()
	defer w.historyMu.Unlock()

	history := make([]map[string]interface{}, len(w.healthHistory))
	copy(history, w.healthHistory)
	return history
}

func NewWebDashboardServer(cfg *config.Config, stratumServer *stratum.StratumServer, jm *pool.JobManager, rpc *bitcoin.BitcoinRpcClient, zmq *bitcoin.ZmqBlockSubscriber) *WebDashboardServer {
	return &WebDashboardServer{
		cfg:           cfg,
		stratumServer: stratumServer,
		jobManager:    jm,
		bitcoinRpc:    rpc,
		zmqSub:        zmq,
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
		uptimeSeconds := int64(time.Since(s.ConnectedAt).Seconds())

		poolHashrate1m += h1m
		poolHashrate5m += h5m

		workersList = append(workersList, map[string]interface{}{
			"sessionId":      s.ID,
			"address":        s.MinerAddress,
			"workerName":     s.WorkerName,
			"difficulty":     s.CurrentDiff,
			"hashrate1m":     h1m,
			"hashrate5m":     h5m,
			"asicboost":      s.VersionRollingEnabled,
			"bestShareDiff":  s.BestShareDiff,
			"acceptedShares": s.AcceptedShares,
			"rejectedShares": s.RejectedShares,
			"uptimeSeconds":  uptimeSeconds,
			"status":         s.GetSessionStatus(),
			"disabledReason": s.DisabledReason,
			"bannedReason":   s.BannedReason,
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

	rpcHealth := map[string]interface{}{"healthy": false, "status": "offline"}
	if w.bitcoinRpc != nil {
		rpcHealth = w.bitcoinRpc.HealthStatus()
	}

	zmqHealth := map[string]interface{}{"healthy": false, "status": "offline"}
	if w.zmqSub != nil {
		zmqHealth = w.zmqSub.HealthStatus()
	}

	alerts := []map[string]interface{}{}
	if rpcHealth["healthy"] != true {
		alerts = append(alerts, map[string]interface{}{
			"severity": "warning",
			"title":    "Bitcoin RPC is stale",
			"detail": func() string {
				if msg, ok := rpcHealth["lastError"].(string); ok && msg != "" {
					return msg
				}
				return "No recent RPC heartbeat received"
			}(),
		})
	}
	if zmqHealth["healthy"] != true {
		alerts = append(alerts, map[string]interface{}{
			"severity": "warning",
			"title":    "ZMQ block feed is offline",
			"detail": func() string {
				if msg, ok := zmqHealth["lastError"].(string); ok && msg != "" {
					return msg
				}
				return "No recent ZMQ connection or message activity"
			}(),
		})
	}
	activityLog := []map[string]interface{}{}
	if rpcHealth["healthy"] != true {
		activityLog = append(activityLog, buildPoolActivityEvent("warning", "Bitcoin RPC is stale", func() string {
			if msg, ok := rpcHealth["lastError"].(string); ok && msg != "" {
				return msg
			}
			return "No recent RPC heartbeat received"
		}()))
	}
	if zmqHealth["healthy"] != true {
		activityLog = append(activityLog, buildPoolActivityEvent("warning", "ZMQ block feed is offline", func() string {
			if msg, ok := zmqHealth["lastError"].(string); ok && msg != "" {
				return msg
			}
			return "No recent ZMQ connection or message activity"
		}()))
	}
	if len(sessions) == 0 {
		alerts = append(alerts, map[string]interface{}{
			"severity": "info",
			"title":    "No connected workers",
			"detail":   "The pool is active but no miners are currently connected",
		})
		activityLog = append(activityLog, buildPoolActivityEvent("info", "No connected workers", "The pool is active but no miners are currently connected"))
	} else if len(sessions) > 0 {
		activityLog = append(activityLog, buildPoolActivityEvent("success", "Workers connected", fmt.Sprintf("%d workers are currently connected and reporting hashrate", len(sessions))))
	}

	healthEntry := buildHealthTimelineEntry(
		rpcHealth["healthy"] == true,
		zmqHealth["healthy"] == true,
		len(sessions),
		len(alerts),
	)
	w.recordHealthSnapshot(healthEntry)

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
		"rpcHealth":         rpcHealth,
		"zmqHealth":         zmqHealth,
		"alerts":            alerts,
		"activityLog":       activityLog,
		"healthTimeline":    w.recentHealthHistory(),
		"poolHealth": map[string]interface{}{
			"overall": func() string {
				if rpcHealth["healthy"] == true && len(alerts) == 0 {
					return "online"
				}
				return "degraded"
			}(),
		},
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

	mux.HandleFunc("/api/admin/worker", func(rw http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var payload struct {
			SessionID string `json:"sessionId"`
			Action    string `json:"action"`
			Reason    string `json:"reason"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(rw, "invalid payload", http.StatusBadRequest)
			return
		}

		ok := false
		switch payload.Action {
		case "disable":
			ok = w.stratumServer.DisableSession(payload.SessionID, payload.Reason)
		case "ban":
			ok = w.stratumServer.BanSession(payload.SessionID, payload.Reason)
		case "resume":
			ok = w.stratumServer.ResumeSession(payload.SessionID)
		default:
			http.Error(rw, "unsupported action", http.StatusBadRequest)
			return
		}

		if !ok {
			http.Error(rw, "worker not found", http.StatusNotFound)
			return
		}

		rw.Header().Set("Content-Type", "application/json")
		json.NewEncoder(rw).Encode(map[string]interface{}{"ok": true, "action": payload.Action})
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
