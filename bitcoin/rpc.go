package bitcoin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
		c.setHealthState(err)
		return nil, err
	}

	var jsonResp map[string]interface{}
	if err := json.Unmarshal(respBytes, &jsonResp); err != nil {
		c.setHealthState(err)
		return nil, err
	}

	if errObj, ok := jsonResp["error"]; ok && errObj != nil {
		err = fmt.Errorf("RPC Error: %v", errObj)
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

func (c *BitcoinRpcClient) GetBlockTemplate() (map[string]interface{}, error) {
	rules := []interface{}{"segwit"}
	params := []interface{}{
		map[string]interface{}{
			"rules": rules,
		},
	}
	res, err := c.Call("getblocktemplate", params)
	if err != nil {
		// Fallback for altcoin SHA-256 nodes (DigiByte, BCH, BSV, Pepecoin, Luckycoin, etc.)
		return c.Call("getblocktemplate", []interface{}{map[string]interface{}{}})
	}
	return res, nil
}

func (c *BitcoinRpcClient) SubmitBlock(blockHex string) (interface{}, error) {
	resp, err := c.Call("submitblock", []interface{}{blockHex})
	if err != nil {
		return nil, err
	}
	return resp["result"], nil
}
