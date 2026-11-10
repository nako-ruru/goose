# opencalc-mcp（OpenCalc 表格能力）

把 OpenCalc 的实时表格**以 MCP 插件的形式**接进 Goose：agent 能读区域、写单元格，
用户能在 MCP Apps 面板里看到同一份数据。**不改 Goose 的 Rust 源码**。

```
agent (Goose)
   │ MCP stdio
   ▼
opencalc-mcp (本目录, Go)          ← 适配层：铸 token、开文档、藏 handle
   │ MCP stdio（子进程）
   ▼
opencalc-live (Rust sidecar)       ← 协同 / OT / 公式，来自 opencalc-live 项目
   │ WebSocket /collab
   ▼
OpenCalc host + collab server      ← 官方，未改动
```

分工遵循 `opencalc-live/INTEGRATION.md`：token 由 integrator（这里就是本进程，
它扮演产品后端）向 host 的 `GET /api/documents/{id}/session` 申请；agent 的上下文里
不出现 token，也不出现 sidecar 的 `handle`。

## 工具

| 工具 | 作用 | 带面板 |
|---|---|---|
| `list_documents` | 列出文档（ID + 标题）。`document` 参数优先用 ID，也接受标题 | — |
| `open_document` | 加入实时协同会话（自动铸 token），返回工作表与连接状态 | ✅ |
| `read_range` | 读 A1 区域（`A1:D20`、`Sheet2!A1:B3`），返回 Markdown 表格 + 结构化数据 | ✅ |
| `write_cells` | 从锚点写二维数组（`=SUM(...)` 是公式），一次调用一个撤销步骤 | ✅ |
| `document_status` | 连接状态、revision、用户最后停留的工作表/选区 | ✅ |
| `recalculate` | 整份文档重算 | — |

`document` 可省略：默认用当前文档，再退到 `OPENCALC_DOC`。

## MCP Apps 面板

工具定义上有：

```json
"_meta": { "ui": { "resourceUri": "ui://opencalc/panel.html" } }
```

`resources/list` 暴露 `ui://opencalc/panel.html`（`text/html;profile=mcp-app`），
`resources/read` 返回内嵌的 `panel.html`（`go:embed`，单文件分发）。

面板是**只读预览**，不做编辑器：

- 渲染最近一次 `read_range` / `open_document` / `write_cells` 的 `structuredContent`；
- 带行号、列字母、粘性表头，跟随宿主 light/dark 主题；
- 有「刷新」按钮，通过 MCP Apps 的 `tools/call` 重新调用 `read_range`；
- 在独立窗口（Goose Apps 页面）打开时没有工具结果，它会自己读一次 `A1:H20`。

资源上的 `_meta.window`（560×680，可缩放）会被 Goose 用作独立窗口尺寸。

## 环境变量

| 变量 | 说明 |
|---|---|
| `OPENCALC_HOST` | host HTTP 地址，默认 `http://127.0.0.1:8088` |
| `OPENCALC_STORE` | 文档数据目录（`list_documents` 直接读它）。默认 `%LOCALAPPDATA%\Rimuse\opencalc\data`（存在才用） |
| `OPENCALC_DOC` | 默认文档 ID |
| `OPENCALC_LIVE_BIN` | `opencalc-live` 可执行文件；默认在 `PATH` 和 `%LOCALAPPDATA%\Rimuse\opencalc\bin` 里找 |
| `OPENCALC_COLLAB_URL` | 覆盖 collab WebSocket 地址（一般用 host session 响应里的） |
| `OPENCALC_ACTOR` | token 里的展示名，默认 `AI` |
| `OPENCALC_MOCK` | `1` 时用内置假文档，不启动 sidecar（只验证 MCP 契约） |

> host 没有“列出文档”的 HTTP 接口，所以 `list_documents` 读数据目录里的 `*.json`。
> 产品化时应改成 host 自己的列表 API。

## 构建

```powershell
cd extensions/opencalc-mcp
go build -o opencalc-mcp.exe .
```

无第三方依赖（只用标准库 + `go:embed`）。仓库里不提交 exe（见 `.gitignore`）。

## 挂进 Goose

`%APPDATA%\Block\goose\config\config.yaml` 的 `extensions:` 下（实测用的就是这段）：

```yaml
  opencalc:
    enabled: true
    type: stdio
    name: opencalc
    description: OpenCalc 实时表格：列出文档、读取区域、写入单元格，并在 MCP Apps 面板里预览
    display_name: OpenCalc
    cmd: "D:\\projects\\goose\\extensions\\opencalc-mcp\\opencalc-mcp.exe"
    args: []
    envs:
      OPENCALC_HOST: "http://127.0.0.1:8088"
      OPENCALC_DOC: "5e613b7ca9a857ce"
      OPENCALC_STORE: "C:\\Users\\PC\\AppData\\Local\\Rimuse\\opencalc\\data"
    timeout: 300
```

改配置前备份在 `config.yaml.bak-opencalc-20261110`。

## 实测结果（2026-11-10）

环境：Goose `1.51.0`（`D:\projects\goose\target\release\goose.exe`），
OpenCalc host `127.0.0.1:8088`、collab `127.0.0.1:8443` 都在跑；
用 `POST /api/documents` 新建了测试文档 `5e613b7ca9a857ce`（`opencalc-mcp-test.xlsx`）。

### 1) JSON-RPC 行协议（真实 sidecar，非 mock）

`initialize → list_documents → read_range → write_cells → read_range → document_status → resources/read`，
整串 0.24s：

```text
# read_range A1:C4（空表）
《opencalc-mcp-test.xlsx》 · Sheet1 · A1:C4（4 行 × 3 列）
|  | A | B | C |
|---|---|---|---|
| 1 |  |  |  |

# write_cells A1 [[项目,预算,备注],[服务器,12000,含税],[合计,=SUM(B2:B2),]]
已写入 9 个单元格到《opencalc-mcp-test.xlsx》Sheet1!A1（3 行 × 3 列）

# read_range A1:C3 —— 公式已由 sidecar 算出
| 3 | 合计 | 12000 |  |

# document_status
《opencalc-mcp-test.xlsx》（5e613b7ca9a857ce）：status=connected connected=true revision=1 editable=true

# resources/read
ui://opencalc/panel.html  text/html;profile=mcp-app  html=8943 bytes
```

`document` 传标题也能打开（`open_document {"document":"opencalc-mcp-test.xlsx"}` →
解析成 ID，状态 `connected`）。mock 模式（`OPENCALC_MOCK=1`）同样跑通读写回环，
用于没有 host/sidecar 时验证契约。

### 2) Goose CLI 真正驱动扩展

```text
goose run -i prompt.txt      # 让 agent：打开文档 → 读 A1:C4 → 复述
```

原始日志（UTF-8，经 `cmd /c "... > log 2>&1"` 重定向）：

```text
  ▸ open_document opencalc
    document: opencalc-mcp-test.xlsx
  ▸ read_range opencalc
    document: opencalc-mcp-test.xlsx
    range: A1:C4

已读取 `opencalc-mcp-test.xlsx` 的 `A1:C4` 区域（Sheet1），表格内容如下：
| 行 | 项目 | 预算 | 备注 |
|---|---|---|---|
| 1 | 项目 | 预算 | 备注 |
| 2 | 服务器 | 12000 | 含税 |
| 3 | 合计 | 12000 | （空） |
| 4 | 总计 | 24000 | 由 AI 写入 |
```

另一轮让 agent 写入第 4 行 `[总计, =SUM(B2:B3), 由 AI 写入]`，随后重读得到 `24000`，
说明写路径、公式重算、协同会话都是真的。CLI 里**没有任何 UI**——见下一节。

### 3) 宿主侧加载路径（用真实 `goose acp` 验证）

写了一个最小 ACP 客户端：`initialize`（带 `clientCapabilities._meta.goose.mcpHostCapabilities`）
→ `session/new` → `_goose/unstable/*`。结果：

```json
{"tools_total": 41, "apps_total": 10,
 "opencalc_app": {
   "uri": "ui://opencalc/panel.html",
   "name": "OpenCalc 表格面板",
   "mimeType": "text/html;profile=mcp-app",
   "mcpServers": ["opencalc"],
   "width": 560, "height": 680, "resizable": true,
   "textLen": 8943
 },
 "resource": {"uri": "ui://opencalc/panel.html",
              "mimeType": "text/html;profile=mcp-app", "htmlLen": 8943},
 "resource_contents": 1}
```

同一轮调用 `opencalc__read_range` 后，工具结果里出现了（仅当宿主声明支持 MCP Apps 时）：

```json
"_meta": {
  "ui": { "resourceUri": "ui://opencalc/panel.html" },
  "__goose_tool_update_meta": {
    "mcpApp": {
      "toolName": "read_range",
      "extensionName": "opencalc",
      "resourceUri": "ui://opencalc/panel.html",
      "resourceResult": { "contents": [{ "text": "<!DOCTYPE html>…8943 bytes…" }] }
    }
  }
}
```

把 `mcpHostCapabilities` 去掉再跑同一套调用：`_meta` 只剩我们自己的 `ui`，
**没有 `mcpApp`**——这就是 CLI 不渲染的原因。

### 4) 面板本身能渲染

用 Chrome headless 跑一个最小 host harness（iframe + `ui/initialize` / `initialized` /
`tool-input` / `tool-result`），截图确认：表头 `A/B/C`、行号 1–4、中文、数字右对齐、
「刷新」按钮都在，`size-changed` 也能把 iframe 撑开。产物在
`%LOCALAPPDATA%\Temp\opencode\opencalc-test\panel.png`。

## MCP Apps / “侧边栏”可行性的结论

**能做，而且不需要改 Goose 源码；但“侧边栏”只在桌面端（或自建 ACP 宿主）成立，
CLI 只会有文本。**

支持程度（Goose 1.51.0）：

| 能力 | 结论 | 证据 |
|---|---|---|
| 客户端声明 `io.modelcontextprotocol/ui` | ✅ | `crates/goose/src/agents/mcp_client.rs:47-59` |
| 读工具定义 `_meta.ui.resourceUri` | ✅ | `crates/goose/src/agents/extension_manager/mod.rs:573-581` |
| 调用后自动 `resources/read` 并附到结果 | ✅ | `crates/goose/src/agents/extension_manager/lease.rs:832-874` |
| 结果里注入 `__goose_tool_update_meta.mcpApp` | ✅ | `crates/goose/src/agents/extension_manager/mod.rs:621-641`；`crates/goose/src/acp/server/tool_calls/conversion.rs:215-239` |
| 桌面端 iframe 渲染 + postMessage JSON-RPC | ✅ | `ui/desktop/src/components/ToolCallWithResponse.tsx:166-225`；`ui/desktop/src/components/McpApps/McpAppRenderer.tsx:262-341`、`:381-393` |
| `ui://` 资源出现在 Apps 页面 | ✅ | `crates/goose/src/agents/extension_manager/lease.rs:458-477`；`crates/goose/src/goose_apps/app.rs:211-289` |
| 独立窗口（真正的“常驻面板”） | ✅ | `ui/desktop/src/main.ts:3018-3032`；`ui/desktop/src/components/apps/StandaloneAppView.tsx` |
| 资源 `_meta.window` 控制窗口大小 | ✅ | `crates/goose/src/goose_apps/app.rs:241-279`（实测返回 560×680） |
| **CLI 渲染 UI** | ❌ | `crates/goose/src/agents/extension_manager/mod.rs:394-399` 只有 `GoosePlatform::GooseDesktop` 才把 `mcpui` 置 true；`crates/goose-cli` 里搜 `ui://` / `resourceUri` / `mcpApp` **0 处** |

补充：ACP 的 `_goose/unstable/tools/list` 序列化里不带 `_meta`（实测 `meta=null`），
但这不影响内联渲染——hydration 用的是 rmcp 内部的 `Tool` 对象，不经过 ACP 的工具列表。

所以：

- **Goose 桌面端**：`_meta.ui.resourceUri` → 聊天里内联渲染；`ui://` 资源还会进 Apps
  页面，点开就是独立窗口（这正是我们要的“侧边栏/浮窗”形态）。
- **Goose CLI（`goose run` / `goose session`）**：不声明 UI 能力，工具结果只有文本，
  **这是预期行为**，不是扩展的 bug。
- **Rimuse**：如果最终宿主不是 Electron 桌面端，就走同样的 ACP/JSON-RPC 契约自己渲染
  `panel.html`；面板只依赖 postMessage，没有 Goose 专有 API。

## 已知限制

1. **只读面板**：不做编辑、选区、单元格交互；写入仍由 agent 调 `write_cells`。
   “侧边栏编辑”需要宿主侧再做一层（面板发 `tools/call` → 我们的 `write_cells` 已经可用）。
2. **覆盖审批**：`opencalc-live` 对“覆盖较多已有单元格”的写入会在
   `%TEMP%\opencalc-live-plans\` 等宿主批准，最多 240s，没人应答就 fail closed。
   这个 PoC 没有实现审批 watcher，所以“大范围覆盖”会超时失败；追加到空区域不受影响。
3. **一个进程一个 sidecar、一个当前文档**：可以同时开多个文档（按 ID 存 handle），
   但共用同一个 `opencalc-live` 进程；sidecar 崩溃后下次调用会自动重启并重开当前文档。
4. **token 是本进程铸的**：PoC 里 `opencalc-mcp` 扮演产品后端直接调 host 的
   `/session`。生产上应由 Rimuse 后端铸好、注入环境变量（`INTEGRATION.md` 的原则）。
5. **`list_documents` 读文件系统**：host 没有列表 API；换 host 部署要改这里。
6. **MCP Apps 在 Goose 里仍是 experimental**：
   `documentation/docs/tutorials/building-mcp-apps.md:12-13` 明确写了
   “experimental … minimally implemented … no persistent app windows”。
7. 面板在 CLI 中不显示是预期；不要把它当成验证失败的信号。

## 文件

| 文件 | 说明 |
|---|---|
| `main.go` | MCP stdio 循环、`initialize` / `tools/*` / `resources/*`、`ui://` 资源与工具 `_meta` |
| `live.go` | `opencalc-live` 子进程 JSON-RPC 客户端、host token、数据目录扫描 |
| `tools.go` | 6 个工具、A1 解析、Markdown 表格渲染 |
| `mock.go` | `OPENCALC_MOCK=1` 时的内置工作簿 |
| `panel.html` | MCP Apps 面板（`go:embed`） |
