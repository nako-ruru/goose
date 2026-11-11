/**
 * electronShim — window.electron compatibility layer for the Wails shell.
 *
 * The ported Goose renderer code calls `window.electron.*` (Electron preload
 * API) in ~30 places. Rather than editing those files (which would fork them
 * from upstream), we install a shim object with the same shape. Only the
 * methods that truly need native code call into Wails bindings; everything
 * else is localStorage / event bus / no-op.
 *
 * Layering rule: this file is pure adaptation, no business logic.
 */
import {ShellService} from "../../bindings/changeme";
import {defaultSettings, type SettingKey, type Settings} from "@goose/utils/settings";
import type {GooseApp} from "@goose/types/apps";
import type {OpenExternalUrlResult} from "@goose/utils/urlSecurity";

// ---------------------------------------------------------------------------
// Settings: localStorage-backed (renderer-only preferences, per our storage
// layering: Go-owned config lives on the Go side, secrets never persist here).
// ---------------------------------------------------------------------------

const SETTINGS_KEY = "rimuse.settings";

function readStoredSettings(): Partial<Settings> {
    try {
        return JSON.parse(localStorage.getItem(SETTINGS_KEY) ?? "{}") as Partial<Settings>;
    } catch {
        return {};
    }
}

function writeStoredSettings(patch: Partial<Settings>): void {
    const next = {...readStoredSettings(), ...patch};
    localStorage.setItem(SETTINGS_KEY, JSON.stringify(next));
}

// ---------------------------------------------------------------------------
// on/off: in-process event bus (replaces ipcRenderer channels).
// ---------------------------------------------------------------------------

type ElectronEventListener = (...args: any[]) => void;
const listeners = new Map<string, Set<ElectronEventListener>>();

// ---------------------------------------------------------------------------
// The shim itself.
// ---------------------------------------------------------------------------

export interface ElectronShim {
    platform: string;
    arch: string;
    getAcpUrl(): Promise<string | null>;
    getSecretKey(): Promise<string | null>;
    getSetting<K extends SettingKey>(key: K): Promise<Settings[K]>;
    setSetting<K extends SettingKey>(key: K, value: Settings[K]): Promise<void>;
    openExternal(url: string): Promise<OpenExternalUrlResult>;
    showMessageBox(options: {
        type?: string;
        buttons?: string[];
        title?: string;
        message?: string;
        detail?: string;
        defaultId?: number;
        cancelId?: string;
        [key: string]: unknown;
    }): Promise<{response: number}>;
    listFiles(dirPath: string, extension?: string): Promise<string[]>;
    createChatWindow(options?: {
        resumeSessionId?: string;
        viewType?: string;
        recipeId?: string;
        dir?: string;
        query?: string;
        [key: string]: unknown;
    }): void;
    showSaveDialog(options?: {
        title?: string;
        defaultPath?: string;
        filters?: {name: string; extensions: string[]}[];
        [key: string]: unknown;
    }): Promise<{canceled: boolean; filePath?: string; filePaths?: string[]}>;
    writeFile(directory: string, content: string): Promise<boolean>;
    selectFileOrDirectory(defaultPath?: string): Promise<string | null>;
    on(channel: string, callback: ElectronEventListener): void;
    off(channel: string, callback: ElectronEventListener): void;
    emit(channel: string, ...args: unknown[]): void;
    broadcastThemeChange(data: {
        mode: string;
        useSystemTheme: boolean;
        theme: string;
        tokensUpdated?: boolean;
    }): void;
    logInfo(txt: string): void;
    hasAcceptedRecipeBefore(recipe: unknown): Promise<boolean>;
    recordRecipeHash(recipe: unknown): Promise<boolean>;
    launchApp(app: GooseApp): Promise<void>;
    refreshApp(app: GooseApp): Promise<void>;
    closeApp(appName: string): Promise<void>;
    // --- degraded methods (signatures match Electron preload; MVP behavior
    //     mirrors goose3's stubs: safe empty values, real Go/Wails impl can
    //     land later without touching ported callers) ---
    addRecentDir(dir: string): Promise<boolean>;
    listRecentDirs(): Promise<string[]>;
    listGitWorktreeDirs(dir: string): Promise<string[]>;
    getGitBranchInfo(dir: string): Promise<{branch: string} | null>;
    listGitBranches(dir: string): Promise<string[]>;
    switchGitBranch(dir: string, branch: string): Promise<{success: boolean; error?: string}>;
    directoryChooser(): Promise<{canceled: boolean; filePaths: string[]}>;
    openDirectoryInExplorer(directoryPath: string): Promise<boolean>;
    getPathForFile(file: File): string;
    isAnyWindowFocused(): Promise<boolean>;
    showNotification(data: {title: string; body: string}): void;
    getIsFullScreen(): Promise<boolean>;
}

export const electronShim: ElectronShim = {
    platform: "win32",
    arch: "x64",

    async getAcpUrl() {
        try {
            return await ShellService.GetAcpUrl();
        } catch {
            return null;
        }
    },

    async getSecretKey() {
        try {
            return await ShellService.GetSecretKey();
        } catch {
            return null;
        }
    },

    async getSetting(key) {
        const stored = readStoredSettings();
        if (key in stored) {
            return stored[key] as Settings[typeof key];
        }
        return defaultSettings[key];
    },

    async setSetting(key, value) {
        writeStoredSettings({[key]: value} as Partial<Settings>);
    },

    async openExternal(url): Promise<OpenExternalUrlResult> {
        // Browser-standard fallback (same approach as goose3's tauriShims).
        if (/^https?:\/\//i.test(url)) {
            window.open(url, "_blank", "noopener,noreferrer");
            return "opened";
        }
        return "blocked";
    },

    async showMessageBox(options) {
        // MVP: surface through console; UI already has react-toastify for
        // user-facing errors. Wire to Wails dialog when needed.
        console.warn("[shell] showMessageBox:", options.title ?? options.message, options.detail ?? "");
        return {response: 0};
    },

    async listFiles() {
        // MVP: file-mention autocomplete degraded to empty (goose3 does the
        // same). A Wails Go implementation can land later without touching
        // the ported callers.
        return [];
    },

    createChatWindow(options) {
        console.info("[shell] createChatWindow not supported yet:", options);
    },

    async showSaveDialog() {
        // Webview has no native save dialog in MVP; recipe export degrades to
        // "user cancelled" (same approach as goose3's tauriShims).
        console.warn("[shell] showSaveDialog not supported yet");
        return {canceled: true, filePath: undefined, filePaths: []};
    },

    async writeFile() {
        console.warn("[shell] writeFile not supported yet");
        return false;
    },

    async selectFileOrDirectory() {
        console.warn("[shell] selectFileOrDirectory not supported yet");
        return null;
    },

    on(channel, callback) {
        let set = listeners.get(channel);
        if (!set) {
            set = new Set();
            listeners.set(channel, set);
        }
        set.add(callback);
    },

    off(channel, callback) {
        listeners.get(channel)?.delete(callback);
    },

    emit(channel, ...args) {
        const set = listeners.get(channel);
        if (!set) return;
        for (const cb of [...set]) {
            try {
                cb(undefined, ...args);
            } catch (err) {
                console.error(`[shell] listener for "${channel}" threw:`, err);
            }
        }
    },

    broadcastThemeChange() {
        // Single-window MVP: the calling window already applied its own theme.
    },

    logInfo(txt) {
        console.log(txt);
    },

    async hasAcceptedRecipeBefore() {
        // Skip recipe consent gate in MVP.
        return true;
    },

    async recordRecipeHash() {
        return true;
    },

    async launchApp(app) {
        console.info("[shell] launchApp not supported yet:", app?.name);
    },

    async refreshApp(app) {
        console.info("[shell] refreshApp not supported yet:", app?.name);
    },

    async closeApp(appName) {
        console.info("[shell] closeApp not supported yet:", appName);
    },

    async addRecentDir() {
        return true;
    },

    async listRecentDirs() {
        return [];
    },

    async listGitWorktreeDirs() {
        return [];
    },

    async getGitBranchInfo() {
        return null;
    },

    async listGitBranches() {
        return [];
    },

    async switchGitBranch() {
        return {success: false, error: "not supported in rimuse shell"};
    },

    async directoryChooser() {
        // No native dialog in MVP; DirSwitcher treats canceled as a no-op.
        return {canceled: true, filePaths: []};
    },

    async openDirectoryInExplorer() {
        return false;
    },

    getPathForFile(file) {
        // Electron returns an absolute path; browsers only expose the name.
        return file.name;
    },

    async isAnyWindowFocused() {
        // Assume focused so the shell does not fire OS notifications in MVP.
        return true;
    },

    showNotification(data) {
        try {
            if (typeof Notification !== "undefined" && Notification.permission === "granted") {
                new Notification(data.title, {body: data.body});
                return;
            }
        } catch {
            // fall through
        }
        console.info(`[shell] notification: ${data.title} — ${data.body}`);
    },

    async getIsFullScreen() {
        // Wails fullscreen events can be wired later; never-fullscreen is a
        // safe default (only affects macOS traffic-light inset).
        return false;
    },
};

// ---------------------------------------------------------------------------
// appConfig: startup config injected by Go (working dir etc.).
// ---------------------------------------------------------------------------

export interface AppConfigShim {
    get(key: string): unknown;
    getAll(): Record<string, unknown>;
}

let appConfigValues: Record<string, unknown> = {};

export function setAppConfig(values: Record<string, unknown>): void {
    appConfigValues = values;
}

export const appConfigShim: AppConfigShim = {
    get: (key) => appConfigValues[key],
    getAll: () => ({...appConfigValues}),
};

// ---------------------------------------------------------------------------
// Install: must run before any renderer module reads window.electron.
// ---------------------------------------------------------------------------

declare global {
    interface Window {
        electron: ElectronShim;
        appConfig: AppConfigShim;
    }
}

export function installShellBridge(): void {
    window.electron = electronShim;
    window.appConfig = appConfigShim;
}
