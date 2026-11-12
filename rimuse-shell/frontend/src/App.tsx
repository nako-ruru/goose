/**
 * Minimal Rimuse shell entry.
 *
 * Mirrors the official desktop App.tsx skeleton (providers, routes,
 * active-session lifecycle) but only ships the chat surface: Hub (empty
 * state) + /pair (ChatSessionsContainer). Settings/onboarding/sidebars are
 * intentionally not mounted yet — they land in later batches.
 *
 * Structure follows upstream: App = providers + HashRouter, AppInner = state
 * and routes (useLocation requires the router above it).
 */
import {useEffect, useRef, useState, type RefObject} from "react";
import {HashRouter, Route, Routes, useLocation, useNavigate, useSearchParams} from "react-router";
import {ToastContainer} from "react-toastify";
import "react-toastify/dist/ReactToastify.css";

import {ThemeProvider} from "@goose/contexts/ThemeContext";
import {FeaturesProvider} from "@goose/contexts/FeaturesContext";
import {ModelAndProviderProvider} from "@goose/components/ModelAndProviderContext";
import {ChatProvider, DEFAULT_CHAT_TITLE} from "@goose/contexts/ChatContext";
import {AppLayout} from "@goose/components/Layout/AppLayout";
import Hub from "@goose/components/Hub";
import ChatSessionsContainer from "@goose/components/ChatSessionsContainer";
import RecipesView from "@goose/components/recipes/RecipesView";
import SettingsView, {type SettingsViewOptions} from "@goose/components/settings/SettingsView";
import SkillsView from "@goose/components/skills/SkillsView";
import AppsView from "@goose/components/apps/AppsView";
import StandaloneAppView from "@goose/components/apps/StandaloneAppView";
import SchedulesView from "@goose/components/schedule/SchedulesView";
import ExtensionsView, {type ExtensionsViewOptions} from "@goose/components/extensions/ExtensionsView";
import SessionsView from "@goose/components/sessions/SessionsView";
import PlaceholderView from "./components/PlaceholderView";
import {useLiveVoice, type LiveVoiceController} from "@goose/liveVoice/useLiveVoice";
import {useNavigation} from "@goose/hooks/useNavigation";
import {AppEvents} from "@goose/constants/events";
import type {ChatType} from "@goose/types/chat";
import type {UserInput} from "@goose/types/message";

interface ActiveSession {
    sessionId: string;
    initialMessage?: UserInput;
    noAutoSubmit?: boolean;
}

const MAX_ACTIVE_SESSIONS = 10;

// ---------------------------------------------------------------------------
// Route wrappers (same shape as official App.tsx)
// ---------------------------------------------------------------------------

const HubRouteWrapper = ({
    draftRef,
    liveVoice,
}: {
    draftRef: RefObject<string>;
    liveVoice: LiveVoiceController;
}) => {
    const setView = useNavigation();
    return <Hub setView={setView} draftRef={draftRef} liveVoice={liveVoice} />;
};

const PairRoute = ({
    activeSessions,
    setChat,
    liveVoice,
}: {
    activeSessions: ActiveSession[];
    setChat: (chat: ChatType) => void;
    liveVoice: LiveVoiceController;
}) => {
    const [searchParams] = useSearchParams();
    const currentSessionId = searchParams.get("resumeSessionId");

    // Bare /pair without a session: send the user back to the hub.
    useEffect(() => {
        if (!currentSessionId && activeSessions.length === 0) {
            window.location.hash = "#/";
        }
    }, [currentSessionId, activeSessions.length]);

    return (
        <ChatSessionsContainer
            setChat={setChat}
            activeSessions={activeSessions}
            liveVoice={liveVoice}
        />
    );
};
// Settings route wrapper (mirrors official App.tsx SettingsRoute).
const SettingsRoute = () => {
    const location = useLocation();
    const navigate = useNavigate();
    const [searchParams] = useSearchParams();
    const setView = useNavigation();

    // viewOptions from location.state / history.state / URL search params
    const viewOptions =
        (location.state as SettingsViewOptions) || (window.history.state as SettingsViewOptions) || {};

    const sectionFromUrl = searchParams.get("section");
    if (sectionFromUrl) {
        viewOptions.section = sectionFromUrl;
    }

    const closeSettings = () => {
        if (location.key === "default") {
            navigate("/");
        } else {
            navigate(-1);
        }
    };

    return <SettingsView onClose={closeSettings} setView={setView} viewOptions={viewOptions} />;
};

// Extensions route wrapper (mirrors official App.tsx ExtensionsRoute).
const ExtensionsRoute = () => {
    const navigate = useNavigate();
    const location = useLocation();

    const viewOptions =
        (location.state as ExtensionsViewOptions) || (window.history.state as ExtensionsViewOptions) || {};

    return (
        <ExtensionsView
            onClose={() => navigate(-1)}
            setView={(view, options) => {
                switch (view) {
                    case "chat":
                        navigate("/");
                        break;
                    case "pair":
                        navigate("/pair", {state: options});
                        break;
                    case "settings":
                        navigate("/settings", {state: options});
                        break;
                    default:
                        navigate("/");
                }
            }}
            viewOptions={viewOptions}
        />
    );
};
// ---------------------------------------------------------------------------
// AppInner: state + routes (must render under the router)
// ---------------------------------------------------------------------------

function AppInner() {
    const [chat, setChat] = useState<ChatType>({
        sessionId: "",
        name: DEFAULT_CHAT_TITLE,
        messages: [],
        recipe: null,
    });
    const hubDraftRef = useRef("");
    const [activeSessions, setActiveSessions] = useState<ActiveSession[]>([]);
    const liveVoice = useLiveVoice();
    const location = useLocation();

    // Stop live voice when leaving the chat entry points (official behavior).
    const {activeSessionId: activeLiveVoiceSessionId, stop: stopLiveVoice} = liveVoice;
    const hasLiveVoiceEntryPoint = location.pathname === "/" || location.pathname === "/pair";
    useEffect(() => {
        if (!hasLiveVoiceEntryPoint && activeLiveVoiceSessionId) {
            void stopLiveVoice();
        }
    }, [activeLiveVoiceSessionId, hasLiveVoiceEntryPoint, stopLiveVoice]);

    // Active-session lifecycle: Hub/pair dispatch window events; this list
    // feeds ChatSessionsContainer (LRU-capped, keeps streams alive).
    useEffect(() => {
        const handleAddActiveSession = (event: Event) => {
            const {sessionId, initialMessage, noAutoSubmit} = (
                event as CustomEvent<{
                    sessionId: string;
                    initialMessage?: UserInput;
                    noAutoSubmit?: boolean;
                }>
            ).detail;

            setActiveSessions((prev) => {
                const existingIndex = prev.findIndex((s) => s.sessionId === sessionId);
                if (existingIndex !== -1) {
                    const existing = prev[existingIndex]!;
                    return [...prev.slice(0, existingIndex), ...prev.slice(existingIndex + 1), existing];
                }
                const updated = [...prev, {sessionId, initialMessage, noAutoSubmit}];
                if (updated.length > MAX_ACTIVE_SESSIONS) {
                    return updated.slice(updated.length - MAX_ACTIVE_SESSIONS);
                }
                return updated;
            });
        };

        const handleClearInitialMessage = (event: Event) => {
            const {sessionId} = (event as CustomEvent<{sessionId: string}>).detail;
            setActiveSessions((prev) =>
                prev.map((s) => (s.sessionId === sessionId ? {...s, initialMessage: undefined} : s))
            );
        };

        const handleSessionDeleted = (event: Event) => {
            const {sessionId} = (event as CustomEvent<{sessionId: string}>).detail;
            setActiveSessions((prev) => prev.filter((s) => s.sessionId !== sessionId));
        };

        window.addEventListener(AppEvents.ADD_ACTIVE_SESSION, handleAddActiveSession);
        window.addEventListener(AppEvents.CLEAR_INITIAL_MESSAGE, handleClearInitialMessage);
        window.addEventListener(AppEvents.SESSION_DELETED, handleSessionDeleted);
        return () => {
            window.removeEventListener(AppEvents.ADD_ACTIVE_SESSION, handleAddActiveSession);
            window.removeEventListener(AppEvents.CLEAR_INITIAL_MESSAGE, handleClearInitialMessage);
            window.removeEventListener(AppEvents.SESSION_DELETED, handleSessionDeleted);
        };
    }, []);

    return (
        <ChatProvider chat={chat} setChat={setChat} contextKey="hub">
            <ToastContainer
                aria-label="Toast notifications"
                toastClassName={() =>
                    `relative min-h-16 mb-4 p-2 rounded-lg
                     flex justify-between overflow-hidden cursor-pointer
                     text-text-inverse bg-background-inverse`
                }
                style={{width: "450px"}}
                className="mt-6"
                position="top-right"
                autoClose={3000}
                closeOnClick
                pauseOnHover
            />
            <div className="relative w-screen h-screen overflow-hidden bg-background-secondary flex flex-col">
                <Routes>
                    <Route path="standalone-app" element={<StandaloneAppView />} />
                    <Route
                        path="/"
                        element={
                            <AppLayout activeSessions={activeSessions} liveVoice={liveVoice} />
                        }
                    >
                        <Route
                            index
                            element={<HubRouteWrapper draftRef={hubDraftRef} liveVoice={liveVoice} />}
                        />
                        <Route
                            path="pair"
                            element={
                                <PairRoute
                                    activeSessions={activeSessions}
                                    setChat={setChat}
                                    liveVoice={liveVoice}
                                />
                            }
                        />
                        <Route path="recipes" element={<RecipesView />} />
                        <Route path="skills" element={<SkillsView />} />
                        <Route path="apps" element={<AppsView />} />
                        <Route path="schedules" element={<SchedulesView />} />
                        <Route
                            path="extensions"
                            element={
                                <ChatProvider chat={chat} setChat={setChat} contextKey="extensions">
                                    <ExtensionsRoute />
                                </ChatProvider>
                            }
                        />
                        <Route path="sessions" element={<SessionsView />} />
                        <Route path="settings" element={<SettingsRoute />} />
                        <Route path="*" element={<PlaceholderView />} />
                    </Route>
                </Routes>
            </div>
        </ChatProvider>
    );
}

// ---------------------------------------------------------------------------
// App: providers + router (upstream nesting order)
// ---------------------------------------------------------------------------

export default function App() {
    return (
        <ThemeProvider>
            <FeaturesProvider>
                <ModelAndProviderProvider>
                    <HashRouter>
                        <AppInner />
                    </HashRouter>
                </ModelAndProviderProvider>
            </FeaturesProvider>
        </ThemeProvider>
    );
}
