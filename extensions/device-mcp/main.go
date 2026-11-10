// device-mcp 是最小 MCP 服务（stdio）：把 orbit-server 的设备列表/在线状态
// 暴露给 agent。独立进程，不依赖 daemon；先验证“多设备协同”的可行性。
//
// 环境变量：
//   ORBIT_SERVER  orbit-server 地址（默认 http://127.0.0.1:6700）
//   ORBIT_TOKEN   Logto access_token（调用 /api/devices 用）
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   any             `json:"error,omitempty"`
}

type device struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	OS       string    `json:"os"`
	Status   string    `json:"status"`
	LastSeen time.Time `json:"lastSeen"`
	LastIP   string    `json:"lastIp"`
}

type devicesResponse struct {
	Code int      `json:"code"`
	Data []device `json:"data"`
}

func main() {
	server := strings.TrimRight(envOr("ORBIT_SERVER", "http://127.0.0.1:6700"), "/")
	token := strings.TrimSpace(os.Getenv("ORBIT_TOKEN"))

	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			continue
		}
		// 通知（无 id）不需要响应
		if len(req.ID) == 0 || string(req.ID) == "null" {
			continue
		}
		resp := handle(server, token, req)
		b, _ := json.Marshal(resp)
		out.Write(b)
		out.WriteByte('\n')
		out.Flush()
	}
}

func handle(server, token string, req rpcRequest) rpcResponse {
	switch req.Method {
	case "initialize":
		return rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "device-mcp", "version": "0.1.0"},
		}}
	case "ping":
		return rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}}
	case "tools/list":
		return rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": tools()}}
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return rpcErr(req.ID, -32602, "invalid params")
		}
		text, err := callTool(server, token, p.Name, p.Arguments)
		if err != nil {
			return rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
				"content": []map[string]any{{"type": "text", "text": err.Error()}},
				"isError": true,
			}}
		}
		return rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
			"content": []map[string]any{{"type": "text", "text": text}},
		}}
	default:
		return rpcErr(req.ID, -32601, "method not found: "+req.Method)
	}
}

func tools() []map[string]any {
	return []map[string]any{
		{
			"name":        "list_devices",
			"description": "列出当前账号下的设备及在线/离线状态。",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
		},
		{
			"name":        "device_status",
			"description": "查询某台设备是否在线（按名称或 ID 匹配）。",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"device": map[string]any{"type": "string", "description": "设备名称或 ID"},
				},
				"required":             []string{"device"},
				"additionalProperties": false,
			},
		},
	}
}

func callTool(server, token, name string, args json.RawMessage) (string, error) {
	switch name {
	case "list_devices":
		ds, err := fetchDevices(server, token)
		if err != nil {
			return "", err
		}
		return formatDevices(ds), nil
	case "device_status":
		var a struct {
			Device string `json:"device"`
		}
		_ = json.Unmarshal(args, &a)
		ds, err := fetchDevices(server, token)
		if err != nil {
			return "", err
		}
		q := strings.ToLower(strings.TrimSpace(a.Device))
		for _, d := range ds {
			if strings.ToLower(d.ID) == q || strings.Contains(strings.ToLower(d.Name), q) {
				return fmt.Sprintf("%s（%s，id=%s）：%s", d.Name, d.OS, d.ID, statusCN(d.Status)), nil
			}
		}
		return "", fmt.Errorf("没找到设备 %q", a.Device)
	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

func fetchDevices(server, token string) ([]device, error) {
	if token == "" {
		return nil, fmt.Errorf("ORBIT_TOKEN 未配置")
	}
	req, err := http.NewRequest(http.MethodGet, server+"/api/devices", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("连接 orbit-server 失败: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("orbit-server HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var out devicesResponse
	if err := json.Unmarshal(bytes.TrimSpace(raw), &out); err != nil {
		return nil, fmt.Errorf("解析设备列表失败: %w", err)
	}
	return out.Data, nil
}

func formatDevices(ds []device) string {
	if len(ds) == 0 {
		return "当前账号下没有设备。"
	}
	online := 0
	var b strings.Builder
	for _, d := range ds {
		if d.Status == "online" {
			online++
		}
	}
	fmt.Fprintf(&b, "共 %d 台设备（在线 %d）：\n", len(ds), online)
	for _, d := range ds {
		b.WriteString("- ")
		b.WriteString(d.Name)
		fmt.Fprintf(&b, "（%s，id=%s）— %s", d.OS, d.ID, statusCN(d.Status))
		if d.Status != "online" && !d.LastSeen.IsZero() {
			fmt.Fprintf(&b, "（最后在线 %s）", d.LastSeen.Format("2006-01-02 15:04"))
		}
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

func statusCN(s string) string {
	if s == "online" {
		return "在线"
	}
	return "离线"
}

func rpcErr(id json.RawMessage, code int, msg string) rpcResponse {
	return rpcResponse{JSONRPC: "2.0", ID: id, Error: map[string]any{"code": code, "message": msg}}
}

func envOr(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}
