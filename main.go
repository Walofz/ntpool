package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
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

	jobManager := pool.NewJobManager(cfg)
	bitcoinRpc := bitcoin.NewBitcoinRpcClient(cfg)
	stratumServer := stratum.NewStratumServer(cfg, jobManager, bitcoinRpc)
	webServer := web.NewWebDashboardServer(cfg, stratumServer, jobManager)

	// Start Stratum Server
	if err := stratumServer.Start(); err != nil {
		log.Fatalf("Failed to start Stratum server: %v", err)
	}

	// Start Block Template Poller / Job Engine
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()

		for {
			tmpl, err := bitcoinRpc.GetBlockTemplate()
			if err == nil && tmpl != nil {
				currentJob := jobManager.GetCurrentJob()
				heightFloat, _ := tmpl["height"].(float64)
				newHeight := int64(heightFloat)

				if currentJob == nil || currentJob.BlockHeight != newHeight {
					log.Printf("[JobEngine Go] New block template received from Bitcoin Core (Height #%d)", newHeight)
					job := jobManager.CreateJob(tmpl)
					stratumServer.BroadcastJob(job, true)
				}
			}
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

	log.Printf("[ntpool Go] Shutting down pool gracefully...")
}
