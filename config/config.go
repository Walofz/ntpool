package config

import (
	"os"
	"strconv"
)

type Config struct {
	StratumPort         int
	WebPort             int
	ZpoolAPIBaseURL     string
	ZpoolAPIUsername    string
	ZpoolAPIPassword    string
	ZpoolWalletAddress  string
	DashboardUsername   string
	DashboardPassword   string
	ZpoolNotifyPayout   bool
	ZpoolPollSeconds    int
	ZpoolStratumHost    string
	ZpoolStratumPort    int
	ZpoolStratumUsername string
	ZpoolStratumPassword string
	NtfyServer          string
	NtfyTopic           string
	NtfyUser            string
	NtfyPassword        string
}

func getEnv(key, defaultVal string) string {
	if val, ok := os.LookupEnv(key); ok && val != "" {
		return val
	}
	return defaultVal
}

func getEnvInt(key string, defaultVal int) int {
	if valStr, ok := os.LookupEnv(key); ok && valStr != "" {
		if val, err := strconv.Atoi(valStr); err == nil {
			return val
		}
	}
	return defaultVal
}

func getEnvBool(key string, defaultVal bool) bool {
	if valStr, ok := os.LookupEnv(key); ok && valStr != "" {
		if val, err := strconv.ParseBool(valStr); err == nil {
			return val
		}
	}
	return defaultVal
}

func LoadConfig() *Config {
	return &Config{
		StratumPort:          getEnvInt("STRATUM_PORT", 3333),
		WebPort:              getEnvInt("WEB_PORT", 8080),
		ZpoolAPIBaseURL:      getEnv("ZPOOL_API_BASE_URL", "https://www.zpool.ca/api"),
		ZpoolAPIUsername:     getEnv("ZPOOL_API_USERNAME", ""),
		ZpoolAPIPassword:     getEnv("ZPOOL_API_PASSWORD", ""),
		ZpoolWalletAddress:   getEnv("ZPOOL_WALLET_ADDRESS", ""),
		DashboardUsername:    getEnv("DASHBOARD_USERNAME", ""),
		DashboardPassword:    getEnv("DASHBOARD_PASSWORD", ""),
		ZpoolNotifyPayout:    getEnvBool("ZPOOL_NOTIFY_PAYOUT", true),
		ZpoolPollSeconds:     getEnvInt("ZPOOL_POLL_SECONDS", 60),
		ZpoolStratumHost:     getEnv("ZPOOL_STRATUM_HOST", "sha256.mine.zpool.ca"),
		ZpoolStratumPort:     getEnvInt("ZPOOL_STRATUM_PORT", 3256),
		ZpoolStratumUsername: getEnv("ZPOOL_STRATUM_USERNAME", ""),
		ZpoolStratumPassword: getEnv("ZPOOL_STRATUM_PASSWORD", "c=BTC"),
		NtfyServer:          getEnv("NTFY_SERVER", "http://192.168.1.250:18080"),
		NtfyTopic:           getEnv("NTFY_TOPIC", "zpool-proxy-blocks"),
		NtfyUser:            getEnv("NTFY_USER", "user"),
		NtfyPassword:        getEnv("NTFY_PASSWORD", "pass"),
	}
}
