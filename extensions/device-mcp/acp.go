package main

// 最小 ACP 客户端：initialize → SSE → session/new → session/prompt。
// 只为实现 run_on_device，够用即可；不做重连/取消等完整能力。

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	headerConnection = "Acp-Connection-Id"
	headerSession    = "Acp-Session-Id"
)

type acpClient struct {
	endpoint string
	headers  map[string]string
	post     *http.Client
	stream   *http.Client

	connID  string
	nextID  int64
	mu      sync.Mutex
	pending map[int64]chan acpResult
	streams map[string]bool

	// onNotify 收到服务端通知（session/update 等）时回调
	onNotify func(method string, params json.RawMessage)
}

type acpResult struct {
	raw json.RawMessage
	err error
}

type acpMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func newACPClient(endpoint string, headers map[string]string) *acpClient {
	return &acpClient{
		endpoint: endpoint,
		headers:  headers,
		post:     &http.Client{Timeout: 30 * time.Second},
		stream:   &http.Client{Timeout: 0},
		pending:  map[int64]chan acpResult{},
		streams:  map[string]bool{},
	}
}

func (c *acpClient) connect(ctx context.Context) error {
	id := atomic.AddInt64(&c.nextID, 1)
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": id, "method": "initialize",
		"params": map[string]any{
			"protocolVersion":    1,
			"clientCapabilities": map[string]any{},
			"clientInfo":         map[string]any{"name": "device-mcp", "version": "0.1.0"},
		},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	c.applyHeaders(req, "")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.post.Do(req)
	if err != nil {
		return fmt.Errorf("ACP initialize: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("ACP initialize HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	c.connID = resp.Header.Get(headerConnection)
	if c.connID == "" {
		return fmt.Errorf("ACP initialize 缺少 %s", headerConnection)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	go c.consume(ctx, "")
	return nil
}

func (c *acpClient) consume(ctx context.Context, sessionID string) {
	c.mu.Lock()
	if c.streams[sessionID] {
		c.mu.Unlock()
		return
	}
	c.streams[sessionID] = true
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.streams, sessionID)
		c.mu.Unlock()
	}()

	for ctx.Err() == nil {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint, nil)
		if err != nil {
			return
		}
		c.applyHeaders(req, sessionID)
		req.Header.Set("Accept", "text/event-stream")
		resp, err := c.stream.Do(req)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return
		}
		scan := bufio.NewScanner(resp.Body)
		scan.Buffer(make([]byte, 64*1024), 4*1024*1024)
		var data []string
		for scan.Scan() {
			line := scan.Text()
			if line == "" {
				if len(data) > 0 {
					c.handle(strings.Join(data, "\n"))
					data = nil
				}
				continue
			}
			if strings.HasPrefix(line, "data:") {
				data = append(data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			}
		}
		resp.Body.Close()
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

func (c *acpClient) handle(data string) {
	var m acpMessage
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		return
	}
	if len(m.ID) > 0 && string(m.ID) != "null" {
		var id int64
		if json.Unmarshal(m.ID, &id) == nil {
			c.mu.Lock()
			ch := c.pending[id]
			delete(c.pending, id)
			c.mu.Unlock()
			if ch != nil {
				if m.Error != nil {
					ch <- acpResult{err: fmt.Errorf("ACP error %d: %s", m.Error.Code, m.Error.Message)}
				} else {
					ch <- acpResult{raw: m.Result}
				}
			}
		}
		return
	}
	if m.Method != "" && c.onNotify != nil {
		c.onNotify(m.Method, m.Params)
	}
}

func (c *acpClient) call(ctx context.Context, method string, params any, sessionID string) (json.RawMessage, error) {
	id := atomic.AddInt64(&c.nextID, 1)
	ch := make(chan acpResult, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()

	if sessionID != "" {
		go c.consume(ctx, sessionID)
	}

	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		c.drop(id)
		return nil, err
	}
	c.applyHeaders(req, sessionID)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.post.Do(req)
	if err != nil {
		c.drop(id)
		return nil, fmt.Errorf("ACP %s: %w", method, err)
	}
	rawBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		c.drop(id)
		return nil, fmt.Errorf("ACP 连接已失效")
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		c.drop(id)
		return nil, fmt.Errorf("ACP %s HTTP %d: %s", method, resp.StatusCode, strings.TrimSpace(string(rawBody)))
	}
	// 有些实现直接在 POST body 里返回结果
	if len(rawBody) > 0 {
		var m acpMessage
		if json.Unmarshal(rawBody, &m) == nil && len(m.ID) > 0 {
			var rid int64
			if json.Unmarshal(m.ID, &rid) == nil && rid == id && len(m.Result) > 0 {
				c.drop(id)
				return m.Result, nil
			}
		}
	}

	select {
	case r := <-ch:
		return r.raw, r.err
	case <-ctx.Done():
		c.drop(id)
		return nil, ctx.Err()
	}
}

func (c *acpClient) applyHeaders(req *http.Request, sessionID string) {
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	req.Header.Set(headerConnection, c.connID)
	if sessionID != "" {
		req.Header.Set(headerSession, sessionID)
	}
}

func (c *acpClient) drop(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}


// runOnDevice 通过 orbit-server 的 ACP 代理，在目标设备上跑一轮 prompt。
func runOnDevice(server, internalToken, deviceSecret, deviceID, osName, prompt string) (string, error) {
	headers := map[string]string{"X-Client-ID": deviceID}
	if deviceSecret != "" {
		headers["X-Secret-Key"] = deviceSecret
	}
	if internalToken != "" {
		headers["Authorization"] = "Bearer " + internalToken
	}
	ac := newACPClient(strings.TrimRight(server, "/")+"/api/internal/agent-acp/acp", headers)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	if err := ac.connect(ctx); err != nil {
		return "", err
	}

	var mu sync.Mutex
	var text strings.Builder
	ac.onNotify = func(method string, params json.RawMessage) {
		if method != "session/update" {
			return
		}
		var p struct {
			Update struct {
				SessionUpdate string `json:"sessionUpdate"`
				Content       struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"update"`
		}
		_ = json.Unmarshal(params, &p)
		if p.Update.SessionUpdate == "agent_message_chunk" && p.Update.Content.Text != "" {
			mu.Lock()
			text.WriteString(p.Update.Content.Text)
			mu.Unlock()
		}
	}

	cwd := strings.TrimSpace(os.Getenv("ORBIT_DEVICE_CWD"))
	if cwd == "" {
		if strings.Contains(strings.ToLower(osName), "win") {
			cwd = `C:\`
		} else {
			cwd = "/"
		}
	}
	res, err := ac.call(ctx, "session/new", map[string]any{"cwd": cwd, "mcpServers": []any{}}, "")
	if err != nil {
		return "", fmt.Errorf("session/new: %w", err)
	}
	var info struct {
		SessionID string `json:"sessionId"`
	}
	_ = json.Unmarshal(res, &info)
	if info.SessionID == "" {
		return "", fmt.Errorf("session/new 没返回 sessionId: %s", truncate(string(res), 200))
	}
	if _, err := ac.call(ctx, "session/prompt", map[string]any{
		"sessionId": info.SessionID,
		"prompt":    []any{map[string]any{"type": "text", "text": prompt}},
	}, info.SessionID); err != nil {
		return "", fmt.Errorf("session/prompt: %w", err)
	}
	mu.Lock()
	out := strings.TrimSpace(text.String())
	mu.Unlock()
	if out == "" {
		out = "（设备执行完成，但没有返回文本）"
	}
	return out, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
