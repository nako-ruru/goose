/**
 * Placeholder for views that are wired into the sidebar but whose real
 * implementation lands in a later batch. Replaces a blank/black screen with
 * an explicit "not yet" state so the shell never looks broken.
 *
 * Strings are literal on purpose: registering them in the upstream
 * ui/desktop i18n message catalogues would break byte-identical syncing,
 * and this component disappears once the real view is ported.
 */
export default function PlaceholderView({name}: {name?: string}) {
    return (
        <div className="flex h-full w-full flex-col items-center justify-center gap-4 bg-background-primary px-6 text-center">
            <p className="text-2xl font-light text-text-primary">
                此功能尚未接入
                {name ? `：${name}` : ""}
            </p>
            <p className="max-w-md text-sm text-text-secondary">
                该视图将在后续批次接入（上游 ui/desktop 原版实现），当前点击不会黑屏。
            </p>
            <a
                href="#/"
                className="mt-2 rounded-lg border border-border-primary px-4 py-2 text-sm text-text-primary transition-colors hover:bg-background-secondary"
            >
                返回聊天
            </a>
        </div>
    );
}
