import {defineConfig} from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import wails from "@wailsio/runtime/plugins/vite";
import path from "node:path";
import {fileURLToPath} from "node:url";

const frontendDir = path.dirname(fileURLToPath(import.meta.url));
// 上游 ui/desktop/src —— 所有移植文件现在直接从这里 import
const gooseSrc = path.resolve(frontendDir, "../../ui/desktop/src");

export default defineConfig({
    server: {
        host: "127.0.0.1",
        port: Number(process.env.WAILS_VITE_PORT) || 9245,
        strictPort: true,
        // dev 模式允许访问仓库根（ui/desktop 在 vite root 之外）
        fs: {allow: [path.resolve(frontendDir, "../..")]},
    },
    plugins: [react(), tailwindcss(), wails("./bindings")],
    resolve: {
        alias: {"@goose": gooseSrc},
        // 必须：ui/desktop/node_modules 存在（react 19.2.8），我们是 19.3.1。
        // 不 dedupe = 双 React/双 context 实例 = hooks 崩。
        // 全量列出共享包：两棵树都有的强制解析到我们这份。
        dedupe: [
            "react", "react-dom", "react-intl", "react-router", "react-toastify",
            "lucide-react", "framer-motion", "lodash", "uuid", "zod", "clsx",
            "tailwind-merge", "class-variance-authority", "katex", "turndown",
            "react-select", "react-markdown", "react-syntax-highlighter",
            "rehype-katex", "remark-breaks", "remark-gfm", "remark-math",
            "@mcp-ui/client", "@modelcontextprotocol/sdk", "@modelcontextprotocol/ext-apps",
            "@agentclientprotocol/sdk", "@aaif/goose-acp-client",
            "@radix-ui/react-collapsible", "@radix-ui/react-dialog",
            "@radix-ui/react-dropdown-menu", "@radix-ui/react-portal",
            "@radix-ui/react-scroll-area", "@radix-ui/react-slot",
            "@radix-ui/react-switch", "@radix-ui/react-tooltip",
            "@radix-ui/react-accordion", "@radix-ui/react-avatar",
            "@radix-ui/react-icons", "@radix-ui/react-popover",
            "@radix-ui/react-radio-group", "@radix-ui/react-select",
            "@radix-ui/react-tabs",
            "@tanstack/react-form", "@tanstack/form-core",
            "@tanstack/store", "@tanstack/react-store",
        ],
    },
});
