package bitcoin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"ntpool/config"
)

type BitcoinRpcClient struct {
	cfg        *config.Config
	httpClient *http.Client
	url        string
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

func (c *BitcoinRpcClient) Call(method string, params []interface{}) (map[string]interface{}, error) {
	reqBody := map[string]interface{}{
		"jsonrpc": "1.0",
		"id":      "ntpool-go",
		"method":  method,
		"params":  params,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", c.url, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(c.cfg.RpcUser, c.cfg.RpcPassword)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var jsonResp map[string]interface{}
	if err := json.Unmarshal(respBytes, &jsonResp); err != nil {
		return nil, err
	}

	if errObj, ok := jsonResp["error"]; ok && errObj != nil {
		return nil, fmt.Errorf("RPC Error: %v", errObj)
	}

	if result, ok := jsonResp["result"].(map[string]interface{}); ok {
		return result, nil
	}

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
