# device-mcp（设备 MCP）

把 orbit-server 的设备列表/在线状态、以及"在远端设备上执行任务"的能力暴露给 agent。
用来验证**多设备协同**的可行性：能力以插件（MCP）形式提供，**不改 Goose 源码**。

## 工具

| 工具 | 作用 |
|---|---|
| `list_devices` | 列出当前账号下的设备及在线/离线状态 |
| `device_status` | 按名称或 ID 查某台设备是否在线 |
| `run_on_device` | 通过 ACP 在目标设备上跑一轮任务，返回设备侧回复 |

`run_on_device` 的链路：

```
device-mcp → orbit-server /api/internal/agent-acp/acp（X-Client-ID 选设备）
           → NPS 隧道 {deviceId}.acp:6730 → 设备上的 goose serve
           → initialize → session/new → session/prompt → SSE 收 session/update
```

设备匹配规则：先按 **ID 精确匹配**，再按名称模糊匹配；同名时优先 **在线** 的设备。

## 环境变量

| 变量 | 说明 |
|---|---|
| `ORBIT_SERVER` | orbit-server 地址，默认 `http://127.0.0.1:6700` |
| `ORBIT_TOKEN` | Logto access_token（调用 `/api/devices` 用） |
| `ORBIT_INTERNAL_TOKEN` | orbit-server 内部 token（ACP 代理鉴权） |
| `ORBIT_DEVICE_SECRET` | 设备侧 `goose serve` 的 `GOOSE_SERVER__SECRET_KEY`（`X-Secret-Key`） |
| `ORBIT_DEVICE_CWD` | 远端会话工作目录，默认 Windows `C:\` / 其它 `/` |

> 产品化时 `ORBIT_TOKEN` 应由客户端在启动扩展时注入，不写死在配置文件里。

## 构建

```powershell
cd extensions/device-mcp
go build -o device-mcp.exe .
```

## 挂进 Goose（stdio 扩展）

在 Goose 的 `config.yaml`（Windows：`%APPDATA%\Block\goose\config\config.yaml`）里加：

```yaml
extensions:
  device:
    type: stdio
    name: device
    enabled: true
    cmd: "D:\\projects\\goose\\extensions\\device-mcp\\device-mcp.exe"
    args: []
    envs:
      ORBIT_SERVER: "http://127.0.0.1:6700"
      ORBIT_TOKEN: "<Logto access_token>"
      ORBIT_INTERNAL_TOKEN: "<orbit-server internal token>"
      ORBIT_DEVICE_SECRET: "<goose serve secret key>"
    timeout: 300
```

然后重启 Goose，问它："我有哪几台设备在线？" / "在 DESKTOP-TFPE8TC 上打个招呼"。

## 本地自测（不经过 Goose）

```powershell
$env:ORBIT_SERVER='http://127.0.0.1:6700'
$env:ORBIT_TOKEN='<token>'
cmd /c ".\device-mcp.exe < test.jsonl"
```

`test.jsonl` 每行一个 JSON-RPC 请求：`initialize`、`tools/list`、`tools/call`。

> **注意 PowerShell 管道编码**：`Get-Content | .\device-mcp.exe` 会用 ANSI/ASCII 编码把中文变成 `?`，
> 请用文件重定向（`cmd /c "... < test.jsonl"`）或先设置 `$OutputEncoding` 为 UTF-8。

## 已知限制（PoC）

- 不处理 `session/request_permission`（需要授权的工具调用可能一直等待）；当前建议先跑只读/问答类任务
- 没有取消/断线重连，单次调用超时 180s
- `ORBIT_TOKEN` 等凭据目前靠环境变量注入
- 目标是**在线且 `goose serve` 可经 NPS 隧道访问**的设备；离线设备的代理会返回 5xx/404

## 验证记录（2026-11-10）

- 独立进程自测：`list_devices` → 3 台设备（1 在线）；`run_on_device` → 远端返回「收到」
- 挂进 Goose 配置后，用 `goose run` 实测：
  - 问设备列表 → 模型自动调用 `Device.listDevices`，返回真实设备
  - 要求跨设备执行 → 模型调用 `Device.runOnDevice`，远端设备回复「收到，我在这台机器上」
- 结论：**加设备能力不需要改 Goose 源码**，只需一份 `type: stdio` 扩展配置
- 踩坑记录：
  - 漏设 `Content-Type: application/json` → ACP 415
  - 同名设备离线/在线并存 → 必须优先在线，否则代理返回 502/404
  - PowerShell 管道默认 ASCII 编码，会把中文变成 `?`（用文件重定向或 `$OutputEncoding`）
  - `goose serve` 所在设备需在线且 NPS 隧道可达

## 为什么先做独立进程

- daemon（orbital-client）只暴露 `/api/ice-servers`、`/api/show-gui`、`/ws/desktop`，没有设备列表接口
- 设备列表在 orbit-server `/api/devices`
- 独立进程最快验证；稳定后可选择：
  - 并进 orbitd（daemon 增加本地设备接口 + MCP 端点，共享登录态）
  - 或让 daemon 代理 `/api/devices`，MCP 只连本机
