// ShellService bridge — ONE interface, TWO backends, selected at build time:
//
//   default          → Wails-generated bindings (tag `wails-baseline` behavior)
//   VITE_SHELL=tauri → Tauri invoke (src-tauri commands)
//
// Frontend call sites (main.tsx GetAppConfig, electronShim GetAcpUrl/
// GetSecretKey) import ShellService from here and never care which shell is
// underneath — so the frozen Wails baseline and the Tauri target build from
// the same tree and stay behavior-comparable.

import {ShellService as WailsShell} from "../../bindings/changeme";
import {invoke} from "@tauri-apps/api/core";

interface ShellBridge {
    /** ACP WebSocket URL of the backend goose serve. */
    GetAcpUrl(): Promise<string>;
    /** Backend auth secret for the MCP Apps sandbox proxy ("" when unset). */
    GetSecretKey(): Promise<string>;
    /** Startup config hydrated into the renderer before the first chat. */
    GetAppConfig(): Promise<Record<string, string>>;
}

const wailsBackend: ShellBridge = WailsShell as unknown as ShellBridge;

const tauriBackend: ShellBridge = {
    GetAcpUrl(): Promise<string> {
        return invoke<string>("get_acp_url");
    },
    GetSecretKey(): Promise<string> {
        return invoke<string>("get_secret_key");
    },
    GetAppConfig(): Promise<Record<string, string>> {
        return invoke<Record<string, string>>("get_app_config");
    },
};

export const ShellService: ShellBridge =
    import.meta.env.VITE_SHELL === "tauri" ? tauriBackend : wailsBackend;
