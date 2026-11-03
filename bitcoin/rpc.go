package bitcoin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"ntpool/config"
)

type BitcoinRpcClient struct {
	cfg             *config.Config
	httpClient      *http.Client
	url             string
	mu              sync.RWMutex
	lastHealthyAt   time.Time
	lastCheckAt     time.Time
	lastError       string
	lastSuccessText string
}

func NewBitcoinRpcClient(cfg *config.Config) *BitcoinRpcClient {
	return &BitcoinRpcClient{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		url: fmt.Sprintf("http://%s:%d", cfg.RpcHost, cfg.RpcPort),
	}
}

func (c *BitcoinRpcClient) setHealthState(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.lastCheckAt = time.Now()
	if err == nil {
		c.lastHealthyAt = c.lastCheckAt
		c.lastError = ""
		c.lastSuccessText = "RPC responding"
		return
	}

	c.lastError = err.Error()
	c.lastSuccessText = ""
}

func (c *BitcoinRpcClient) HealthStatus() map[string]interface{} {
	c.mu.RLock()
	defer c.mu.RUnlock()

	healthy := !c.lastHealthyAt.IsZero() && time.Since(c.lastHealthyAt) <= 30*time.Second
	return map[string]interface{}{
		"healthy":       healthy,
		"lastCheckAt":   c.lastCheckAt,
		"lastHealthyAt": c.lastHealthyAt,
		"lastError":     c.lastError,
		"status": func() string {
			if healthy {
				return "online"
			}
			return "degraded"
		}(),
	}
}

func (c *BitcoinRpcClient) Call(method string, params []interface{}) (map[string]interface{}, error) {
	reqBody := map[string]interface{}{
		"jsonrpc": "1.0",
		"id":      "ntpool-go",
		"method":  method,
		"params":  params,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		c.setHealthState(err)
		return nil, err
	}

	req, err := http.NewRequest("POST", c.url, bytes.NewBuffer(bodyBytes))
	if err != nil {
		c.setHealthState(err)
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(c.cfg.RpcUser, c.cfg.RpcPassword)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.setHealthState(err)
		return nil, err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		err = fmt.Errorf("RPC %s (HTTP %s): read response: %w", method, resp.Status, err)
		c.setHealthState(err)
		return nil, err
	}

	switch resp.StatusCode {
	case http.StatusUnauthorized:
		err = fmt.Errorf("RPC %s: HTTP %s: check RPC_USER and RPC_PASSWORD", method, resp.Status)
	case http.StatusForbidden:
		err = fmt.Errorf("RPC %s: HTTP %s: check node rpcallowip and RPC access rules", method, resp.Status)
	}
	if err != nil {
		c.setHealthState(err)
		return nil, err
	}
	if len(bytes.TrimSpace(respBytes)) == 0 {
		err = fmt.Errorf("RPC %s (HTTP %s): empty response body; check RPC_HOST, RPC_PORT and node/proxy configuration", method, resp.Status)
		c.setHealthState(err)
		return nil, err
	}

	var jsonResp map[string]interface{}
	if err = json.Unmarshal(respBytes, &jsonResp); err != nil {
		err = fmt.Errorf("RPC %s (HTTP %s): invalid JSON response: %w; check RPC endpoint and node/proxy configuration", method, resp.Status, err)
		c.setHealthState(err)
		return nil, err
	}

	if errObj, ok := jsonResp["error"]; ok && errObj != nil {
		err = fmt.Errorf("RPC %s (HTTP %s): RPC Error: %v", method, resp.Status, errObj)
		c.setHealthState(err)
		return nil, err
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		err = fmt.Errorf("RPC %s: HTTP %s", method, resp.Status)
		c.setHealthState(err)
		return nil, err
	}

	if result, ok := jsonResp["result"].(map[string]interface{}); ok {
		c.setHealthState(nil)
		return result, nil
	}

	c.setHealthState(nil)
	return map[string]interface{}{"result": jsonResp["result"]}, nil
}

// GetBlockTemplate asks the node for a block template using the parameters the
// configured coin profile expects. Requests get simpler on every failure:
//
//  1. the profile rules plus the mining algorithm (multi-algo nodes such as
//     DigiByte accept the algorithm as a second parameter)
//  2. the profile rules only (single-algo nodes reject the extra parameter)
//  3. an empty request (pre-segwit forks reject the segwit rule)
//
// A node may also demand explicit client support for an active deployment
// ("Support for 'x' rule requires explicit client support"). That rule only
// describes what the pool understands, so it is added and the request is retried
// instead of leaving the pool without work.
func (c *BitcoinRpcClient) GetBlockTemplate() (map[string]interface{}, error) {
	rules := append([]string{}, c.cfg.CoinProfile.GbtRules...)
	if len(rules) == 0 {
		rules = []string{"segwit"}
	}
	algo := c.templateAlgo()

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		res, err := c.requestBlockTemplate(rules, algo)
		if err == nil {
			return res, nil
		}
		lastErr = err

		rule := missingClientRule(err)
		if rule == "" || containsString(rules, rule) {
			break
		}
		rules = append(rules, rule)
	}

	// Fallback for altcoin SHA-256 nodes (BCH, BSV, Pepecoin, Luckycoin, etc.)
	// that reject the segwit rule.
	res, fallbackErr := c.Call("getblocktemplate", []interface{}{map[string]interface{}{}})
	if fallbackErr == nil {
		return res, nil
	}
	if lastErr != nil {
		return nil, fmt.Errorf("%v (plain request: %w)", lastErr, fallbackErr)
	}
	return nil, fallbackErr
}

// requestBlockTemplate sends one getblocktemplate request, dropping the
// algorithm parameter when the node does not know it.
func (c *BitcoinRpcClient) requestBlockTemplate(rules []string, algo string) (map[string]interface{}, error) {
	ruleValues := make([]interface{}, 0, len(rules))
	for _, rule := range rules {
		ruleValues = append(ruleValues, rule)
	}
	params := []interface{}{map[string]interface{}{"rules": ruleValues}}

	if algo == "" || !c.cfg.CoinProfile.GbtAlgoParam {
		return c.Call("getblocktemplate", params)
	}

	if res, err := c.Call("getblocktemplate", []interface{}{params[0], algo}); err == nil {
		return res, nil
	}
	// The node does not take an algorithm argument: retry without it.
	return c.Call("getblocktemplate", params)
}

// templateAlgo returns the algorithm to request from multi-algo nodes.
func (c *BitcoinRpcClient) templateAlgo() string {
	if !c.cfg.CoinProfile.GbtAlgoParam {
		return ""
	}
	algo := strings.TrimSpace(c.cfg.RpcAlgo)
	if algo == "" {
		algo = c.cfg.CoinProfile.Algo
	}
	return algo
}

var clientRulePattern = regexp.MustCompile(`Support for '([^']+)' rule requires explicit client support`)

// missingClientRule extracts the rule name a node wants the client to declare.
func missingClientRule(err error) string {
	if err == nil {
		return ""
	}
	match := clientRulePattern.FindStringSubmatch(err.Error())
	if len(match) != 2 {
		return ""
	}
	return match[1]
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if strings.EqualFold(value, want) {
			return true
		}
	}
	return false
}

func (c *BitcoinRpcClient) SubmitBlock(blockHex string) (interface{}, error) {
	resp, err := c.Call("submitblock", []interface{}{blockHex})
	if err != nil {
		return nil, err
	}
	return resp["result"], nil
}
