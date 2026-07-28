package main

import (
	"log"

	"zpoolproxy/config"
	"zpoolproxy/web"
)

func main() {
	cfg := config.LoadConfig()

	log.Printf("==========================================================")
	log.Printf("   🔁 zpool proxy")
	log.Printf("==========================================================")

	stratumProxy := web.NewZpoolStratumProxy(cfg)
	go func() {
		if err := stratumProxy.Start(); err != nil {
			log.Fatalf("Failed to start zpool stratum proxy: %v", err)
		}
	}()

	dashboard := web.NewZpoolProxyServer(cfg)
	if err := dashboard.Start(); err != nil {
		log.Fatalf("Failed to start zpool proxy dashboard: %v", err)
	}
}
