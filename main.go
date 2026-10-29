package main

import (
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"ntpool/bitcoin"
	"ntpool/config"
	"ntpool/pool"
	"ntpool/stratum"
	"ntpool/web"
)

func main() {
	cfg := config.LoadConfig()

	log.Printf("==========================================================")
	log.Printf("   🚀 %s (Go High-Performance Engine)", cfg.PoolName)
	log.Printf("==========================================================")

	if !pool.IsValidAddress(cfg.WalletAddress) {
		log.Printf("[Config] ERROR: WALLET_ADDRESS %q does not decode to a spendable output script - block rewards would be sent to a burn address. Fix WALLET_ADDRESS in .env", cfg.WalletAddress)
	}
	if strings.TrimSpace(cfg.PoolFeeAddress) != "" && !pool.IsValidAddress(cfg.PoolFeeAddress) {
		log.Printf("[Config] ERROR: POOL_FEE_ADDRESS %q does not decode to a spendable output script - the pool fee would be burned. Fix POOL_FEE_ADDRESS in .env", cfg.PoolFeeAddress)
	}

	jobManager := pool.NewJobManager(cfg)
	bitcoinRpc := bitcoin.NewBitcoinRpcClient(cfg)
	stratumServer := stratum.NewStratumServer(cfg, jobManager, bitcoinRpc)

	// Helper to fetch and broadcast new block template
	var templateMu sync.Mutex
	var lastTemplateFingerprint string
	updateJob := func(reason string) {
		templateMu.Lock()
		defer templateMu.Unlock()

		tmpl, err := bitcoinRpc.GetBlockTemplate()
		if err != nil || tmpl == nil {
			return
		}

		fingerprint := pool.TemplateFingerprint(tmpl)
		currentJob := jobManager.GetCurrentJob()
		if currentJob != nil && fingerprint == lastTemplateFingerprint {
			return
		}

		heightFloat, _ := tmpl["height"].(float64)
		newHeight := int64(heightFloat)
		coinbaseval, _ := tmpl["coinbasevalue"].(float64)
		prevHash, _ := tmpl["previousblockhash"].(string)

		// cleanJobs invalidates work in flight, so only set it when the previous
		// block actually changed. A same-height fee refresh must keep old jobs valid.
		cleanJobs := currentJob == nil || currentJob.BlockHeight != newHeight || currentJob.PrevHashRaw != prevHash

		if cleanJobs {
			log.Printf("[JobEngine Go] New block template (%s) Height #%d", reason, newHeight)
		} else {
			log.Printf("[JobEngine Go] Refreshed template (%s) Height #%d coinbase %.8f", reason, newHeight, coinbaseval/1e8)
		}

		job := jobManager.CreateJob(tmpl)
		stratumServer.BroadcastJob(job, cleanJobs)
		lastTemplateFingerprint = fingerprint
	}

	// Instant ZMQ Block Subscriber
	zmqSub := bitcoin.NewZmqBlockSubscriber(cfg, func(blockHash string) {
		updateJob("ZMQ Instant Notification")
	})
	webServer := web.NewWebDashboardServer(cfg, stratumServer, jobManager, bitcoinRpc, zmqSub)

	// Start Stratum Server
	if err := stratumServer.Start(); err != nil {
		log.Fatalf("Failed to start Stratum server: %v", err)
	}

	zmqSub.Start()

	// Backup Poller (Every 3 seconds)
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()

		for {
			updateJob("3s Backup Poller")
			<-ticker.C
		}
	}()

	// Start Web Dashboard
	go func() {
		if err := webServer.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("[Web Error] %v", err)
		}
	}()

	// Wait for OS shutdown signals
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Printf("[ntpool Go] Shutting down pool gracefully...")
	zmqSub.Stop()
	stratumServer.Stop()
	webServer.Stop()
	log.Printf("[ntpool Go] Shutdown complete")
}
