/**
 * Entry point (mirrors official src/renderer.tsx):
 *  1. install the shell bridge BEFORE any renderer module reads
 *     window.electron / window.appConfig,
 *  2. hydrate startup config from Go,
 *  3. apply theme tokens before first paint,
 *  4. load i18n messages, then mount React with Intl + Config providers.
 */
import {StrictMode} from "react";
import {createRoot} from "react-dom/client";
import {IntlProvider} from "react-intl";
import "./styles/ours.css"; // 必须在最前：tailwind + 上游 main.css + 我们文件的 @source
import {installShellBridge, setAppConfig} from "./bridge/electronShim";
import {ShellService} from "../bindings/changeme";
import {ConfigProvider} from "@goose/components/ConfigContext";
import {applyThemeTokens} from "@goose/theme/theme-tokens";
import {currentLocale, currentMessageLocale, loadMessages} from "@goose/i18n/index";
import App from "./App";

// 1. Synchronous install — module-level reads (e.g. workingDir.ts) must not
//    see undefined.
installShellBridge();

// 2. Hydrate startup config from the Go side (best effort; renderer falls
//    back to defaults).
try {
    const cfg = await ShellService.GetAppConfig();
    setAppConfig((cfg ?? {}) as Record<string, unknown>);
} catch (err) {
    console.warn("[shell] GetAppConfig failed:", err);
}

// 3. Theme tokens before first paint.
applyThemeTokens();

// 4. Load messages, then mount.
let warnedFallbackLocale = false;
function handleIntlError(err: {code: string; message: string}) {
    if (err.code === "MISSING_TRANSLATION" && currentLocale !== currentMessageLocale) {
        if (!warnedFallbackLocale) {
            warnedFallbackLocale = true;
            console.warn(
                `[i18n] Locale "${currentLocale}" has no translations; falling back to "${currentMessageLocale}".`
            );
        }
        return;
    }
    console.error(err);
}

const messages = await loadMessages(currentMessageLocale);

createRoot(document.getElementById("root")!).render(
    <StrictMode>
        <IntlProvider
            locale={currentLocale}
            defaultLocale="en"
            messages={messages}
            onError={handleIntlError}
        >
            <ConfigProvider>
                <App />
            </ConfigProvider>
        </IntlProvider>
    </StrictMode>,
);
