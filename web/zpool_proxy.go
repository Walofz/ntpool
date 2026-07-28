package web

import (
	"crypto/subtle"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"ntpool/config"
)

type ZpoolProxyServer struct {
	cfg    *config.Config
	client *http.Client
}

func NewZpoolProxyServer(cfg *config.Config) *ZpoolProxyServer {
	return &ZpoolProxyServer{
		cfg: cfg,
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (s *ZpoolProxyServer) Start() error {
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.Dir("./public/zpool")))
	mux.HandleFunc("/api/zpool/status", s.handleStatus)
	mux.HandleFunc("/api/zpool/currencies", s.handleCurrencies)
	mux.HandleFunc("/api/zpool/wallet", s.handleWallet)

	handler := loopbackOnly(s.localAuth(mux))

	addr := fmt.Sprintf(":%d", s.cfg.WebPort)
	log.Printf("[Zpool Proxy] Dashboard running on http://localhost:%d", s.cfg.WebPort)
	return http.ListenAndServe(addr, handler)
}

func (s *ZpoolProxyServer) localAuth(next http.Handler) http.Handler {
	if s.cfg.DashboardUsername == "" && s.cfg.DashboardPassword == "" {
		return next
	}

	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok || !s.matchesDashboardAuth(username, password) {
			rw.Header().Set("WWW-Authenticate", `Basic realm="zpool-dashboard", charset="UTF-8"`)
			http.Error(rw, "unauthorized", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(rw, r)
	})
}

func (s *ZpoolProxyServer) matchesDashboardAuth(username, password string) bool {
	usernameMatch := subtle.ConstantTimeCompare([]byte(username), []byte(s.cfg.DashboardUsername)) == 1
	passwordMatch := subtle.ConstantTimeCompare([]byte(password), []byte(s.cfg.DashboardPassword)) == 1
	return usernameMatch && passwordMatch
}

func loopbackOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if !isLoopbackClient(r.RemoteAddr) {
			http.Error(rw, "forbidden", http.StatusForbidden)
			return
		}

		next.ServeHTTP(rw, r)
	})
}

func isLoopbackClient(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}

	ip := net.ParseIP(strings.TrimSpace(host))
	if ip == nil {
		return false
	}

	return ip.IsLoopback()
}

func (s *ZpoolProxyServer) handleStatus(rw http.ResponseWriter, r *http.Request) {
	s.proxyJSON(rw, r, "/status", nil)
}

func (s *ZpoolProxyServer) handleCurrencies(rw http.ResponseWriter, r *http.Request) {
	s.proxyJSON(rw, r, "/currencies", nil)
}

func (s *ZpoolProxyServer) handleWallet(rw http.ResponseWriter, r *http.Request) {
	address := strings.TrimSpace(r.URL.Query().Get("address"))
	if address == "" {
		address = strings.TrimSpace(s.cfg.ZpoolWalletAddress)
	}

	if address == "" {
		http.Error(rw, "missing wallet address (set ZPOOL_WALLET_ADDRESS or use ?address=...)", http.StatusBadRequest)
		return
	}

	s.proxyJSON(rw, r, "/wallet", map[string]string{
		"address": address,
	})
}

func (s *ZpoolProxyServer) proxyJSON(rw http.ResponseWriter, r *http.Request, endpoint string, query map[string]string) {
	upstreamURL, err := s.buildUpstreamURL(endpoint, query)
	if err != nil {
		http.Error(rw, fmt.Sprintf("invalid upstream URL: %v", err), http.StatusInternalServerError)
		return
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, upstreamURL, nil)
	if err != nil {
		http.Error(rw, fmt.Sprintf("failed to create request: %v", err), http.StatusInternalServerError)
		return
	}

	if s.cfg.ZpoolAPIUsername != "" || s.cfg.ZpoolAPIPassword != "" {
		req.SetBasicAuth(s.cfg.ZpoolAPIUsername, s.cfg.ZpoolAPIPassword)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		http.Error(rw, fmt.Sprintf("upstream request failed: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(resp.StatusCode)
	if _, copyErr := io.Copy(rw, resp.Body); copyErr != nil {
		log.Printf("[Zpool Proxy] failed to stream upstream response: %v", copyErr)
	}
}

func (s *ZpoolProxyServer) buildUpstreamURL(endpoint string, query map[string]string) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(s.cfg.ZpoolAPIBaseURL), "/")
	if base == "" {
		return "", fmt.Errorf("empty ZPOOL_API_BASE_URL")
	}

	if !strings.HasPrefix(endpoint, "/") {
		return "", fmt.Errorf("endpoint must start with '/'")
	}

	fullURL, err := url.Parse(base + endpoint)
	if err != nil {
		return "", err
	}

	values := fullURL.Query()
	for key, value := range query {
		values.Set(key, value)
	}
	fullURL.RawQuery = values.Encode()

	return fullURL.String(), nil
}
