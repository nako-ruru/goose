# drag-matrix — 拖拽/移动延迟四框架对照实验

起因：rimuse-shell（Wails v3）在"活跃 opencode 会话前拖拽"场景100%复现卡顿，
重启不愈。本目录归档了用于定位的**极简对照工程**与**基准脚本**，供 Tauri 重写
期间对比参考、以及将来复现/回归。

## 结论（2026-11-12，本机：Win11 25H2 / 150%高刷 / Intel UHD）

五方同刻交替基准（每窗120次 SetWindowPos ×3轮，轮内排名有效；跨轮绝对值受
系统负载影响 —— 注意对照组本身会漂移）：

| 实现 | 哲学 | 移动通知 | 实测表现 |
|---|---|---|---|
| Tauri2 (tao) | Rust | **从不调用** NotifyParentWindowPositionChanged（tao 源码零调用） | ✅ 唯一负载免疫：7.5-9.1ms ≈ 对照1.6-2.6× |
| webview_go（bisoncraft fork = 官方 webview/webview_go，diff 仅10行 import 路径） | cgo + C++ | webview.cc 内 | ⚠️ 负载脆弱：1.4→21ms 波动 |
| Wails3（internal edge，jchv 系纯 Go COM） | 纯 Go | 每帧 WM_MOVE+WM_MOVING 各一次 | ⚠️ 负载脆弱：18-24ms |
| glaze（crgimenes，purego） | 纯 Go FFI | 直连 WebView2 | ⚠️ 负载脆弱：21-46ms（冷启动更糟） |
| 原生 WinForms | — | — | 基准（1.1-5.6ms 随负载） |

关键事实：
- **Go 系三种 FFI（cgo/纯Go COM/purego）全部负载脆弱** → 问题不在语言/FFI，
  在 Go 宿主窗口的移动消息处理与调度路径
- **Wails 每帧两次同步跨进程通知**是其独有负担（我们的 move guard 就是它的补丁，
  已随 `wails-baseline` 标签封存）
- fork 与上游 webview/webview_go：webview.cc/glue.c **字节相同**、WebView2 SDK
  同版本，仅 webview.go10行 import 差异 → 无需分别评估

## 工程

- `minwails/` 纯净 Wails3（`go build -ldflags "-H=windowsgui" .`）
- `minowv/` 纯净 webview_go（cgo，需 mingw gcc）
- `minglaze/` 纯净 glaze（`CGO_ENABLED=0`，go1.27 工具链由 GOTOOLCHAIN=auto 拉取）
- `mintauri/` 纯净 Tauri2（`cargo build --release`，需 icons/icon.ico）

## 基准脚本

- `move_bench.ps1` — 单窗口移动延迟（含原生对照）
- `interleave_bench.ps1` — rimuse/openidea/原生 同刻交替
- `matrix_bench3.ps1` — 四框架+原生 五方同刻交替（主证据）

复现：编译各工程 → `powershell -File matrix_bench3.ps1`。
结果解读注意：**只比同一轮内的排名**，跨轮绝对值受当时系统负载影响。

## 关联

- Wails 壳冻结点：git tag `wails-baseline`
- Tauri 重写对照：重写遇到行为差异时，回此目录的同名工程做 A/B
