# rimuse-shell

Rimuse 桌面壳：基于 [Wails v3](https://wails.io)（Go + WebView2）的 Goose 发行版外壳，
前端直接 import 上游 `ui/desktop` 源码（零拷贝、零修改），实现白标替换 Electron。

## 架构原则

```
rimuse-shell/frontend/src/     ← 我们的文件（入口 + 路由壳 + 桥接垫片）
  App.tsx                        路由表（手写：登记哪些侧栏项可点）
  bridge/electronShim.ts         window.electron 垫片（33 方法）
  components/PlaceholderView.tsx 未接入视图的占位页（防黑屏）
ui/desktop/src/                ← 上游源码，必须保持与 origin/main 字节一致
  （通过 tsconfig paths / vite alias 的 @goose/* 引用，绝不就地修改）
```

三条铁律：

1. **上游文件字节不变**：`git diff origin/main -- ui/desktop/` 必须为空。
   需要新依赖、新类型时，只改 `rimuse-shell/frontend` 侧的配置（paths / dedupe / overrides）。
2. **每接入一个侧栏视图 = 拷闭包 + 在 `App.tsx` 路由表登记一行**。
   只登记不拷闭包会编译失败；只拷闭包不登记会黑屏。
3. **未接入的路由一律挂 `PlaceholderView`**，不允许无渲染路由。

## 开发

```powershell
# 前端
cd frontend
npm install
npm run build          # vite 构建（产物由 go:embed 打进二进制）

# 壳
cd ..
go build -o rimuse-shell.exe .

# 运行（开发时后端 goose serve 由外部提供）
$env:RIMUSE_ACP_URL = 'ws://127.0.0.1:<serve-port>/acp?token=<secret>'
$env:RIMUSE_SERVE_SECRET = '<secret>'
.\rimuse-shell.exe
```

## 与上游同步（origin/main）

```powershell
git fetch origin
git merge origin/main          # 上游 ui/desktop 变更（零冲突是常态）
cd frontend
node ../../ui/desktop/scripts/i18n-compile.js   # 上游 messages → compiled/（有新 key 时）
npx tsc --noEmit               # 必须 0 错误
npm run build                  # 必须成功
cd ..
go build -o rimuse-shell.exe .
```

同步后必查：

- `git diff origin/main -- ../../ui/desktop/` 为空（上游没被改脏）
- 新出现的 `error TS2xxx` 多为依赖版本漂移 —— 对照 `ui/pnpm-lock.yaml`
  把对应包钉进 `frontend/package.json` 的 `overrides` 和 `tsconfig.json` 的 `paths`
- 侧栏若新增视图入口 → 走铁律 2

## 测试

前端 `tsc --noEmit` = 0 错误，`vite build` + `go build` 通过；
壳启动后经 CDP（WebView2 remote debugging）逐路由截图验证渲染与控制台无错误。
