# device-mcp（最小设备 MCP）

把 orbit-server 的设备列表/在线状态暴露给 agent，用来验证"多设备协同"的可行性。

## 工具

| 工具 | 作用 |
|---|---|
| `list_devices` | 列出当前账号下的设备及在线/离线状态 |
| `device_status` | 按名称或 ID 查某台设备是否在线 |

> 下一步：`run_on_device`（通过 daemon / orbit-server 的 ACP 通道派发任务），保持插件化，不写死在 Goose 里。

## 环境变量

| 变量 | 说明 |
|---|---|
| `ORBIT_SERVER` | orbit-server 地址，默认 `http://127.0.0.1:6700` |
| `ORBIT_TOKEN` | Logto access_token（调用 `/api/devices` 用） |

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
    timeout: 30
```

然后重启 Goose，问它："我有哪几台设备在线？"

## 本地自测（不经过 Goose）

```powershell
$env:ORBIT_SERVER='http://127.0.0.1:6700'
$env:ORBIT_TOKEN='<token>'
Get-Content test.jsonl | .\device-mcp.exe
```

`test.jsonl` 三行：`initialize`、`tools/list`、`tools/call list_devices`。

## 为什么先做独立进程

- daemon（orbital-client）只暴露 `/api/ice-servers`、`/api/show-gui`、`/ws/desktop`，**没有设备列表接口**
- 设备列表在 orbit-server `/api/devices`
- 独立进程最快验证；稳定后可选择：
  - 并进 orbitd（daemon 增加本地设备接口 + MCP 端点）
  - 或让 daemon 代理 `/api/devices`，MCP 只连本机

## 认证备注

`ORBIT_TOKEN` 是用户登录后的 Logto access_token。正式产品里应由客户端在启动扩展时注入，而不是手写死在配置里。
