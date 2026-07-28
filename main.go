package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"zpoolproxy/bitcoin"
	"zpoolproxy/config"
	"zpoolproxy/pool"
	"zpoolproxy/stratum"
	"zpoolproxy/web"
)

func main() {
	cfg := config.LoadConfig()

	log.Printf("==========================================================")
	log.Printf("   🚀 %s (Go High-Performance Engine)", cfg.PoolName)
	log.Printf("==========================================================")

	jobManager := pool.NewJobManager(cfg)
	bitcoinRpc := bitcoin.NewBitcoinRpcClient(cfg)
	stratumServer := stratum.NewStratumServer(cfg, jobManager, bitcoinRpc)
	webServer := web.NewWebDashboardServer(cfg, stratumServer, jobManager)

	// Start Stratum Server
	if err := stratumServer.Start(); err != nil {
		log.Fatalf("Failed to start Stratum server: %v", err)
	}

	// Helper to fetch and broadcast new block template
	updateJob := func(reason string) {
		tmpl, err := bitcoinRpc.GetBlockTemplate()
		if err == nil && tmpl != nil {
			currentJob := jobManager.GetCurrentJob()
			heightFloat, _ := tmpl["height"].(float64)
			newHeight := int64(heightFloat)

			if currentJob == nil || currentJob.BlockHeight != newHeight {
				log.Printf("[JobEngine Go] New block template (%s) Height #%d", reason, newHeight)
				job := jobManager.CreateJob(tmpl)
				stratumServer.BroadcastJob(job, true)
			}
		}
	}

	// Instant ZMQ Block Subscriber
	zmqSub := bitcoin.NewZmqBlockSubscriber(cfg, func(blockHash string) {
		updateJob("ZMQ Instant Notification")
	})
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
		if err := webServer.Start(); err != nil {
			log.Printf("[Web Error] %v", err)
		}
	}()

	// Wait for OS shutdown signals
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Printf("[zpool proxy Go] Shutting down pool gracefully...")
}
