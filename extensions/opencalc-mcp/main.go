// opencalc-mcp 把 OpenCalc 的“实时表格”能力以 MCP（stdio）形式暴露给 host。
//
// 设计约束（与 Rimuse 的产品原则一致）：
//
//   - 能力是插件，不改 Goose 源码；
//   - 真正的协同/OT 在 opencalc-live（Rust sidecar）里，本进程只做适配：
//     自动铸造 token、自动打开文档、把 handle 从 agent 的上下文里拿掉；
//   - 如果 opencalc-live / host 不在，可用 OPENCALC_MOCK=1 跑内置假数据，
//     仅用于验证 MCP 契约（明确标注 mock）。
//
// 环境变量：
//
//	OPENCALC_HOST       host HTTP 地址（默认 http://127.0.0.1:8088）
//	OPENCALC_STORE      文档目录（默认 %LOCALAPPDATA%\Rimuse\opencalc\data，存在才用）
//	OPENCALC_DOC        默认打开的文档 ID
//	OPENCALC_LIVE_BIN   opencalc-live 可执行文件（默认在 PATH 与 Rimuse 安装目录里找）
//	OPENCALC_COLLAB_URL 覆盖 collab WebSocket 地址（一般由 host 的 session 响应给出）
//	OPENCALC_ACTOR      token 里的展示名（默认 AI）
//	OPENCALC_MOCK       1/true 时使用内置假文档，不启动 sidecar
package main

import (
	"bufio"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

const panelURI = "ui://opencalc/panel.html"

//go:embed panel.html
var panelHTML string

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func main() {
	srv := newServer(loadConfig())
	defer srv.close()

	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	out := bufio.NewWriter(os.Stdout)

	for sc.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(sc.Text(), "\ufeff"))
		if line == "" {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			continue
		}
		// 通知（没有 id）不需要响应。
		if len(req.ID) == 0 || string(req.ID) == "null" {
			continue
		}
		resp := srv.handle(req)
		b, err := json.Marshal(resp)
		if err != nil {
			continue
		}
		out.Write(b)
		out.WriteByte('\n')
		out.Flush()
	}
}

func ok(id json.RawMessage, result any) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "result": result}
}

func fail(id json.RawMessage, code int, msg string) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   map[string]any{"code": code, "message": msg},
	}
}

func (s *server) handle(req rpcRequest) any {
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		proto := p.ProtocolVersion
		if proto == "" {
			proto = "2024-11-05"
		}
		return ok(req.ID, map[string]any{
			"protocolVersion": proto,
			"capabilities": map[string]any{
				"tools":     map[string]any{"listChanged": false},
				"resources": map[string]any{"listChanged": false, "subscribe": false},
			},
			"serverInfo": map[string]any{
				"name":    "opencalc",
				"title":   "OpenCalc",
				"version": "0.1.0",
			},
			"instructions": "OpenCalc 实时表格。read_range 读取 A1 区域（如 A1:D20，或 Sheet2!A1:B3），" +
				"write_cells 从锚点写入一个矩形块（'=...' 是公式），一个工具调用是一个撤销步骤。" +
				"文档参数可省略，默认使用当前/默认文档。面板（ui://opencalc/panel.html）是同一份数据的只读预览。",
		})
	case "ping":
		return ok(req.ID, map[string]any{})
	case "tools/list":
		return ok(req.ID, map[string]any{"tools": toolDefs()})
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return fail(req.ID, -32602, "invalid params")
		}
		text, structured, err := s.callTool(p.Name, p.Arguments)
		if err != nil {
			return ok(req.ID, map[string]any{
				"content": []map[string]any{{"type": "text", "text": err.Error()}},
				"isError": true,
				"_meta":   uiMeta(),
			})
		}
		result := map[string]any{
			"content": []map[string]any{{"type": "text", "text": text}},
			"isError": false,
		}
		if structured != nil {
			result["structuredContent"] = structured
			result["_meta"] = uiMeta()
		}
		return ok(req.ID, result)
	case "resources/list":
		return ok(req.ID, map[string]any{"resources": []map[string]any{panelResource()}})
	case "resources/read":
		var p struct {
			URI string `json:"uri"`
		}
		_ = json.Unmarshal(req.Params, &p)
		if p.URI != panelURI {
			return fail(req.ID, -32602, "unknown resource: "+p.URI)
		}
		return ok(req.ID, map[string]any{"contents": []map[string]any{panelContent()}})
	case "prompts/list":
		return ok(req.ID, map[string]any{"prompts": []any{}})
	default:
		return fail(req.ID, -32601, "method not found: "+req.Method)
	}
}

func uiMeta() map[string]any {
	return map[string]any{
		"ui": map[string]any{"resourceUri": panelURI},
	}
}

func panelResource() map[string]any {
	return map[string]any{
		"uri":         panelURI,
		"name":        "OpenCalc 表格面板",
		"description": "OpenCalc 实时文档的只读预览：显示最近一次 read_range / open_document 的结果。",
		"mimeType":    "text/html;profile=mcp-app",
		// Goose 的 Apps 页面用这个 meta 决定独立窗口大小（goose_apps/app.rs 只认 window）。
		"_meta": map[string]any{
			"window": map[string]any{"width": 560, "height": 680, "resizable": true},
		},
	}
}

func panelContent() map[string]any {
	return map[string]any{
		"uri":      panelURI,
		"mimeType": "text/html;profile=mcp-app",
		"text":     panelHTML,
		"_meta": map[string]any{
			"ui": map[string]any{
				"prefersBorder": true,
				"csp": map[string]any{
					"connectDomains":  []string{},
					"resourceDomains": []string{},
					"frameDomains":    []string{},
					"baseUriDomains":  []string{},
				},
			},
		},
	}
}

var errNoDocument = errors.New("还没有指定文档：请传入 document，或设置 OPENCALC_DOC")

func (s *server) callTool(name string, args json.RawMessage) (string, any, error) {
	if args == nil {
		args = json.RawMessage("{}")
	}
	switch name {
	case "list_documents":
		return s.listDocuments()
	case "open_document":
		return s.openDocument(args)
	case "read_range":
		return s.readRange(args)
	case "write_cells":
		return s.writeCells(args)
	case "document_status":
		return s.documentStatus(args)
	case "recalculate":
		return s.recalculate(args)
	default:
		return "", nil, fmt.Errorf("unknown tool: %s", name)
	}
}
