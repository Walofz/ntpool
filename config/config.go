package config

import (
	"os"
	"strconv"
)

type Config struct {
	AppMode             string
	StratumPort         int
	WebPort             int
	DefaultDiff         float64
	EnableVardiff       bool
	MinDiff             float64
	MaxDiff             float64
	VardiffTargetShares int
	RpcHost             string
	RpcPort             int
	RpcUser             string
	RpcPassword         string
	RpcNetwork          string
	RpcAlgo             string
	ZmqHost             string
	ZmqPort             int
	PoolName            string
	CoinSymbol          string
	CoinbaseText        string
	PoolFeePercent      float64
	PoolFeeAddress      string
	WalletAddress       string
	NtfyServer          string
	NtfyTopic           string
	NtfyUser            string
	NtfyPassword        string
	ZpoolAPIBaseURL     string
	ZpoolAPIUsername    string
	ZpoolAPIPassword    string
	ZpoolWalletAddress  string
	DashboardUsername   string
	DashboardPassword   string
	ZpoolNotifyPayout    bool
	ZpoolPollSeconds     int
	ZpoolStratumHost     string
	ZpoolStratumPort     int
	ZpoolStratumPassword string
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

func getEnvFloat(key string, defaultVal float64) float64 {
	if valStr, ok := os.LookupEnv(key); ok && valStr != "" {
		if val, err := strconv.ParseFloat(valStr, 64); err == nil {
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
		AppMode:             getEnv("APP_MODE", "ntpool"),
		StratumPort:         getEnvInt("STRATUM_PORT", 3333),
		WebPort:             getEnvInt("WEB_PORT", 8080),
		DefaultDiff:         getEnvFloat("DEFAULT_DIFF", 1024),
		EnableVardiff:       getEnvBool("ENABLE_VARDIFF", false),
		MinDiff:             getEnvFloat("MIN_DIFF", 64),
		MaxDiff:             getEnvFloat("MAX_DIFF", 1048576),
		VardiffTargetShares: getEnvInt("VARDIFF_TARGET_SHARES", 12),
		RpcHost:             getEnv("RPC_HOST", "127.0.0.1"),
		RpcPort:             getEnvInt("RPC_PORT", 8332),
		RpcUser:             getEnv("RPC_USER", "bitcoinrpc"),
		RpcPassword:         getEnv("RPC_PASSWORD", "rpcpassword"),
		RpcNetwork:          getEnv("RPC_NETWORK", "mainnet"),
		RpcAlgo:             getEnv("RPC_ALGO", "sha256d"),
		ZmqHost:             getEnv("ZMQ_HOST", "127.0.0.1"),
		ZmqPort:             getEnvInt("ZMQ_PORT", 28332),
		PoolName:            getEnv("POOL_NAME", "ntpool SHA-256 Solo Pool"),
		CoinSymbol:          getEnv("COIN_SYMBOL", "BTC"),
		CoinbaseText:        getEnv("COINBASE_TEXT", "/ntpool/"),
		PoolFeePercent:      getEnvFloat("POOL_FEE_PERCENT", 0.0),
		PoolFeeAddress:      getEnv("POOL_FEE_ADDRESS", ""),
		WalletAddress:       getEnv("WALLET_ADDRESS", "AWPuDcCymof8BRF9cfkxnLqmhn7ZPVPjEr"),
		NtfyServer:          getEnv("NTFY_SERVER", "http://192.168.1.250:18080"),
		NtfyTopic:           getEnv("NTFY_TOPIC", "ntpool-blocks"),
		NtfyUser:            getEnv("NTFY_USER", "user"),
		NtfyPassword:        getEnv("NTFY_PASSWORD", "pass"),
		ZpoolAPIBaseURL:     getEnv("ZPOOL_API_BASE_URL", "https://www.zpool.ca/api"),
		ZpoolAPIUsername:    getEnv("ZPOOL_API_USERNAME", ""),
		ZpoolAPIPassword:    getEnv("ZPOOL_API_PASSWORD", ""),
		ZpoolWalletAddress:  getEnv("ZPOOL_WALLET_ADDRESS", ""),
		DashboardUsername:   getEnv("DASHBOARD_USERNAME", ""),
		DashboardPassword:   getEnv("DASHBOARD_PASSWORD", ""),
		ZpoolNotifyPayout:    getEnvBool("ZPOOL_NOTIFY_PAYOUT", true),
		ZpoolPollSeconds:     getEnvInt("ZPOOL_POLL_SECONDS", 60),
		ZpoolStratumHost:     getEnv("ZPOOL_STRATUM_HOST", "sha256.mine.zpool.ca"),
		ZpoolStratumPort:     getEnvInt("ZPOOL_STRATUM_PORT", 3256),
		ZpoolStratumPassword: getEnv("ZPOOL_STRATUM_PASSWORD", "c=BTC"),
	}
}
