package web

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"ntpool/config"
)

type ZpoolProxyServer struct {
	cfg           *config.Config
	client        *http.Client
	lastTotalPaid float64
	haveTotalPaid bool
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

	handler := lanOnly(s.localAuth(mux))
	go s.startPayoutMonitor()

	addr := fmt.Sprintf(":%d", s.cfg.WebPort)
	log.Printf("[Zpool Proxy] Dashboard running on http://localhost:%d", s.cfg.WebPort)
	return http.ListenAndServe(addr, handler)
}

func (s *ZpoolProxyServer) startPayoutMonitor() {
	if !s.cfg.ZpoolNotifyPayout {
		return
	}

	address := strings.TrimSpace(s.cfg.ZpoolWalletAddress)
	if address == "" {
		log.Printf("[zpool notify] skip payout monitor: ZPOOL_WALLET_ADDRESS is empty")
		return
	}

	pollSeconds := s.cfg.ZpoolPollSeconds
	if pollSeconds < 10 {
		pollSeconds = 10
	}

	check := func() {
		totalPaid, err := s.fetchWalletTotalPaid(address)
		if err != nil {
			log.Printf("[zpool notify] wallet poll failed: %v", err)
			return
		}

		if !s.haveTotalPaid {
			s.lastTotalPaid = totalPaid
			s.haveTotalPaid = true
			log.Printf("[zpool notify] baseline totalpaid=%.8f", totalPaid)
			return
		}

		if totalPaid <= s.lastTotalPaid {
			return
		}

		paidDelta := totalPaid - s.lastTotalPaid
		s.lastTotalPaid = totalPaid
		s.notifyPayout(address, paidDelta)
	}

	check()
	ticker := time.NewTicker(time.Duration(pollSeconds) * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		check()
	}
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

func (s *ZpoolProxyServer) fetchWalletTotalPaid(address string) (float64, error) {
	upstreamURL, err := s.buildUpstreamURL("/wallet", map[string]string{
		"address": address,
	})
	if err != nil {
		return 0, err
	}

	req, err := http.NewRequest(http.MethodGet, upstreamURL, nil)
	if err != nil {
		return 0, err
	}
	if s.cfg.ZpoolAPIUsername != "" || s.cfg.ZpoolAPIPassword != "" {
		req.SetBasicAuth(s.cfg.ZpoolAPIUsername, s.cfg.ZpoolAPIPassword)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("wallet upstream %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var payload map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return 0, err
	}

	root := payload
	if nestedRaw, ok := payload["getuserbalance"]; ok {
		nested, ok := nestedRaw.(map[string]interface{})
		if !ok {
			return 0, fmt.Errorf("unexpected getuserbalance shape")
		}
		root = nested
	}

	totalPaid, ok := getNumericField(root, "totalpaid", "total", "paid")
	if !ok {
		return 0, fmt.Errorf("totalpaid/total not found in wallet response")
	}

	return totalPaid, nil
}

func getNumericField(source map[string]interface{}, keys ...string) (float64, bool) {
	for _, key := range keys {
		value, ok := source[key]
		if !ok || value == nil {
			continue
		}

		switch typed := value.(type) {
		case float64:
			return typed, true
		case string:
			parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
			if err == nil {
				return parsed, true
			}
		case json.Number:
			parsed, err := typed.Float64()
			if err == nil {
				return parsed, true
			}
		}
	}
	return 0, false
}

func (s *ZpoolProxyServer) notifyPayout(address string, paidDelta float64) {
	if s.cfg.NtfyServer == "" || s.cfg.NtfyTopic == "" {
		return
	}

	body := fmt.Sprintf(
		"ZPOOL PAYOUT DETECTED\nAddress %s\nNew payout %.8f",
		address,
		paidDelta,
	)

	endpoint := strings.TrimRight(s.cfg.NtfyServer, "/") + "/" + strings.TrimLeft(s.cfg.NtfyTopic, "/")
	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(body))
	if err != nil {
		log.Printf("[zpool notify] failed to create request: %v", err)
		return
	}

	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	req.Header.Set("Title", "ZPOOL PAYOUT")
	req.Header.Set("Priority", "default")
	req.Header.Set("Tags", "money_with_wings,receipt")
	if s.cfg.NtfyUser != "" || s.cfg.NtfyPassword != "" {
		req.SetBasicAuth(s.cfg.NtfyUser, s.cfg.NtfyPassword)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("[zpool notify] failed to send payout notification: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		log.Printf("[zpool notify] notification rejected: %s %s", resp.Status, strings.TrimSpace(string(respBody)))
		return
	}

	log.Printf("[zpool notify] payout notification sent to %s", endpoint)
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
