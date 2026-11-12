// ShellService bridge — ONE interface, TWO backends, selected at build time:
//
//   default          → Wails-generated bindings (tag `wails-baseline` behavior)
//   VITE_SHELL=tauri → Tauri invoke (src-tauri commands)
//
// Frontend call sites (main.tsx GetAppConfig, electronShim GetAcpUrl/
// GetSecretKey/DirectoryChooser/...) import ShellService from here and never
// care which shell is underneath — so the frozen Wails baseline and the
// Tauri target build from the same tree and stay behavior-comparable.
//
// The window trio (OpenStandalone/CloseWindow/WindowID) is capability surface
// ported in P3: no frontend caller yet, but both backends expose the same
// shape so a future caller works on either shell unchanged.
//
// The picker trio (DirectoryChooser/SelectFileOrDirectory/
// OpenDirectoryInExplorer) landed with the DirSwitcher bug fix: the buttons
// "选择目录" and "在文件管理器中打开" did nothing because electronShim stubbed
// them. Wails never implemented them either, so its side keeps the old stub
// behavior verbatim while Tauri talks to native dialogs.

import {ShellService as WailsShell} from "../../bindings/changeme";
import {invoke} from "@tauri-apps/api/core";

export interface DirectoryChoice {
    canceled: boolean;
    filePaths: string[];
}

interface ShellBridge {
    /** ACP WebSocket URL of the backend goose serve. */
    GetAcpUrl(): Promise<string>;
    /** Backend auth secret for the MCP Apps sandbox proxy ("" when unset). */
    GetSecretKey(): Promise<string>;
    /** Startup config hydrated into the renderer before the first chat. */
    GetAppConfig(): Promise<Record<string, string>>;
    /** Open a standalone panel window; resolves to its numeric window id. */
    OpenStandalone(panelURL: string, title: string): Promise<number>;
    /** Close a standalone window by id (rejects when the id is unknown). */
    CloseWindow(id: number): Promise<void>;
    /** Id of the calling window (0 = main/unknown). */
    WindowID(): Promise<number>;
    /** Native folder picker; cancelling → {canceled:true, filePaths:[]}. */
    DirectoryChooser(): Promise<DirectoryChoice>;
    /** Native file picker (recipe .yaml/.yml); null when cancelled. */
    SelectFileOrDirectory(defaultPath?: string): Promise<string | null>;
    /** Reveal a working dir in Explorer; false when the shell cannot. */
    OpenDirectoryInExplorer(directoryPath: string): Promise<boolean>;
}

// Wails baseline (frozen at tag `wails-baseline`) never exposed the picker
// trio, so spread its bindings first and then pin the three methods to the
// exact stub behavior electronShim used to implement — that keeps the shared
// shim's contract intact on both shells instead of crashing on undefined.
const wailsBackend: ShellBridge = {
    ...(WailsShell as unknown as ShellBridge),
    DirectoryChooser: async () => ({canceled: true, filePaths: []}),
    SelectFileOrDirectory: async () => null,
    OpenDirectoryInExplorer: async () => false,
};

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
    OpenStandalone(panelURL: string, title: string): Promise<number> {
        return invoke<number>("open_standalone", {panelUrl: panelURL, title});
    },
    CloseWindow(id: number): Promise<void> {
        return invoke<void>("close_window", {id});
    },
    WindowID(): Promise<number> {
        return invoke<number>("window_id");
    },
    DirectoryChooser(): Promise<DirectoryChoice> {
        return invoke<DirectoryChoice>("directory_chooser");
    },
    SelectFileOrDirectory(defaultPath?: string): Promise<string | null> {
        return invoke<string | null>("select_file_or_directory", {defaultPath});
    },
    OpenDirectoryInExplorer(directoryPath: string): Promise<boolean> {
        return invoke<boolean>("open_directory_in_explorer", {directoryPath});
    },
};

export const ShellService: ShellBridge =
    import.meta.env.VITE_SHELL === "tauri" ? tauriBackend : wailsBackend;
