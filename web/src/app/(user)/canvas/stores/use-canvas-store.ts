import { create } from "zustand";
import { persist, type PersistStorage, type StorageValue } from "zustand/middleware";

import { nanoid } from "nanoid";
import { getAccountOwnerId, getAccountStorageKey, GUEST_ACCOUNT_OWNER } from "@/lib/account-scope";
import { localForageStorage } from "@/lib/localforage-storage";
import { listCanvasProjects, saveCanvasProject, syncCanvasProjects } from "@/services/api/canvas-tasks";
import { fetchUserConfig } from "@/services/api/user-config";
import { useUserStore } from "@/stores/use-user-store";
import type { CanvasBackgroundMode } from "@/lib/canvas-theme";
import type { CanvasAgentConfig, CanvasAssistantSession, CanvasConnection, CanvasNodeData, CanvasPendingAgentRequest, ViewportTransform } from "../types";

export type CanvasSidePanelState = {
    open: boolean;
    width: number;
};

export const DEFAULT_CANVAS_SIDE_PANEL: CanvasSidePanelState = { open: true, width: 280 };
export const DEFAULT_CANVAS_AGENT_PANEL: CanvasSidePanelState = { open: false, width: 390 };

export type CanvasProject = {
    id: string;
    title: string;
    createdAt: string;
    updatedAt: string;
    nodes: CanvasNodeData[];
    connections: CanvasConnection[];
    chatSessions: CanvasAssistantSession[];
    activeChatId: string | null;
    agentConfig: CanvasAgentConfig | null;
    autoTitlePending: boolean;
    pendingAgentRequest?: CanvasPendingAgentRequest;
    backgroundMode: CanvasBackgroundMode;
    showImageInfo: boolean;
    viewport: ViewportTransform;
    sidePanel: CanvasSidePanelState;
    agentPanel: CanvasSidePanelState;
};

type CanvasStore = {
    hydrated: boolean;
    projects: CanvasProject[];
    createProject: (title?: string, options?: { agentConfig?: CanvasAgentConfig; pendingAgentRequest?: CanvasPendingAgentRequest }) => string;
    importProject: (project: Partial<CanvasProject>) => string;
    openProject: (id: string) => CanvasProject | null;
    renameProject: (id: string, title: string) => void;
    deleteProjects: (ids: string[]) => void;
    updateProject: (id: string, patch: Partial<Pick<CanvasProject, "nodes" | "connections" | "chatSessions" | "activeChatId" | "agentConfig" | "autoTitlePending" | "backgroundMode" | "showImageInfo" | "viewport" | "sidePanel" | "agentPanel" | "pendingAgentRequest">>) => void;
    syncWithRemote: (token: string, syncEnabled: boolean) => Promise<void>;
    setSyncEnabled: (enabled: boolean) => void;
    resetForAccount: () => void;
};

const initialViewport: ViewportTransform = { x: 0, y: 0, k: 1 };
const CANVAS_STORE_KEY = "infinite-canvas:canvas_store";
type PersistedCanvasState = Pick<CanvasStore, "projects"> & { ownerId: string };
let saveTimer: ReturnType<typeof setTimeout> | null = null;
let queuedPersistState: PersistedCanvasState | null = null;
let accountCanvasSyncEnabled = false;
let suppressCanvasPersistence = false;
let canvasRehydratePromise: Promise<void> | null = null;
const projectSaveTimers = new Map<string, ReturnType<typeof setTimeout>>();

function waitForUserStoreHydration() {
    if (useUserStore.persist.hasHydrated() && useUserStore.getState().isReady) return Promise.resolve();

    return new Promise<void>((resolve) => {
        let unsubscribePersist = () => { };
        let unsubscribeStore = () => { };
        const finish = () => {
            if (!useUserStore.persist.hasHydrated() || !useUserStore.getState().isReady) return;
            unsubscribePersist();
            unsubscribeStore();
            resolve();
        };
        unsubscribePersist = useUserStore.persist.onFinishHydration(finish);
        unsubscribeStore = useUserStore.subscribe(finish);
        finish();
    });
}

function queueProjectSave(project: CanvasProject) {
    const token = useUserStore.getState().token;
    const ownerId = getAccountOwnerId();
    const syncEnabled = accountCanvasSyncEnabled;
    const previous = projectSaveTimers.get(project.id);
    if (previous) clearTimeout(previous);

    projectSaveTimers.set(
        project.id,
        setTimeout(() => {
            projectSaveTimers.delete(project.id);
            if (
                !token ||
                !syncEnabled ||
                !accountCanvasSyncEnabled ||
                useUserStore.getState().token !== token ||
                getAccountOwnerId() !== ownerId
            ) {
                return;
            }
            void saveCanvasProject(token, project).catch(() => undefined);
        }, 400),
    );
}

function cancelProjectSaves(ids: string[]) {
    ids.forEach((id) => {
        const timer = projectSaveTimers.get(id);
        if (!timer) return;
        clearTimeout(timer);
        projectSaveTimers.delete(id);
    });
}

async function reconcileCanvasProjects(
    token: string,
    remoteProjects: CanvasProject[],
    localProjects: CanvasProject[],
) {
    const ownerId = getAccountOwnerId();
    const remoteById = new Map(
        remoteProjects.map((project) => [project.id, project]),
    );
    const missingProjects = localProjects.filter(
        (project) => !remoteById.has(project.id),
    );
    const existingLocalProjects = localProjects.filter((project) =>
        remoteById.has(project.id),
    );
    const projects = missingProjects.length
        ? await syncCanvasProjects(token, missingProjects)
            .then((syncedProjects) =>
                mergeCanvasProjects(
                    syncedProjects,
                    existingLocalProjects,
                ),
            )
            .catch(() =>
                mergeCanvasProjects(remoteProjects, localProjects),
            )
        : mergeCanvasProjects(remoteProjects, existingLocalProjects);

    if (getAccountOwnerId() !== ownerId || useUserStore.getState().token !== token) return null;

    localProjects.forEach((project) => {
        const remote = remoteById.get(project.id);
        if (
            remote &&
            Date.parse(project.updatedAt || "") >
            Date.parse(remote.updatedAt || "")
        ) {
            queueProjectSave(project);
        }
    });

    return projects;
}

const canvasStorage: PersistStorage<CanvasStore> = {
    getItem: async (name) => {
        await waitForUserStoreHydration();
        const ownerId = getAccountOwnerId();
        const localValue = await localForageStorage.getItem(getAccountStorageKey(name, ownerId));
        const token = useUserStore.getState().token;
        const localParsed = localValue
            ? (JSON.parse(localValue) as StorageValue<CanvasStore>)
            : null;
        const localState = localParsed?.state as PersistedCanvasState | undefined;
        const localProjects = localState?.ownerId === ownerId ? localState.projects || [] : [];
        const localHasData = Array.isArray(localProjects) && localProjects.length > 0;

        if (token) {
            try {
                const [userConfig, remoteProjects] = await Promise.all([
                    fetchUserConfig(token),
                    listCanvasProjects(token),
                ]);
                if (getAccountOwnerId() !== ownerId || useUserStore.getState().token !== token) return null;
                accountCanvasSyncEnabled = userConfig.syncCapabilities?.userData === true;

                if (accountCanvasSyncEnabled && localHasData) {
                    const projects = await reconcileCanvasProjects(token, remoteProjects, localProjects);
                    if (!projects) return null;
                    const nextState = { ownerId, projects };
                    const parsed = { state: nextState, version: 0 } as StorageValue<CanvasStore>;
                    queuedPersistState = nextState;
                    await localForageStorage.setItem(getAccountStorageKey(name, ownerId), JSON.stringify(parsed));
                    return parsed;
                }

                if (remoteProjects.length > 0 && (accountCanvasSyncEnabled || !localHasData)) {
                    const nextState = { ownerId, projects: remoteProjects };
                    const parsed = { state: nextState, version: 0 } as StorageValue<CanvasStore>;
                    queuedPersistState = nextState;
                    await localForageStorage.setItem(getAccountStorageKey(name, ownerId), JSON.stringify(parsed));
                    return parsed;
                }
            } catch (error) {
                console.error("Failed to hydrate canvas projects from remote", error);
            }
        }

        if (!localParsed || localState?.ownerId !== ownerId) return null;
        queuedPersistState = localState;
        return localParsed;
    },

    setItem: (name, value) => {
        if (suppressCanvasPersistence) return;
        const ownerId = getAccountOwnerId();
        const nextState = { ...(value.state as PersistedCanvasState), ownerId };
        if (queuedPersistState && queuedPersistState.projects === nextState.projects && queuedPersistState.ownerId === ownerId) return;
        queuedPersistState = nextState;
        if (saveTimer) clearTimeout(saveTimer);
        saveTimer = setTimeout(() => {
            saveTimer = null;
            void localForageStorage.setItem(
                getAccountStorageKey(name, ownerId),
                JSON.stringify({ ...value, state: nextState }),
            );
        }, 400);
    },
    removeItem: (name) => localForageStorage.removeItem(getAccountStorageKey(name)),
};

export const useCanvasStore = create<CanvasStore>()(
    persist(
        (set, get) => ({
            hydrated: false,
            projects: [],
            createProject: (title = "未命名画布", options) => {
                const now = new Date().toISOString();
                const id = nanoid();
                const project: CanvasProject = {
                    id,
                    title,
                    createdAt: now,
                    updatedAt: now,
                    nodes: [],
                    connections: [],
                    chatSessions: [],
                    activeChatId: null,
                    agentConfig: options?.agentConfig || null,
                    autoTitlePending: true,
                    pendingAgentRequest: options?.pendingAgentRequest,
                    backgroundMode: "lines",
                    showImageInfo: false,
                    viewport: initialViewport,
                    sidePanel: DEFAULT_CANVAS_SIDE_PANEL,
                    agentPanel: options?.pendingAgentRequest ? { ...DEFAULT_CANVAS_AGENT_PANEL, open: true } : DEFAULT_CANVAS_AGENT_PANEL,
                };
                set((state) => ({
                    projects: [project, ...state.projects],
                }));
                queueProjectSave(project);
                return id;
            },
            importProject: (source) => {
                const now = new Date().toISOString();
                const project: CanvasProject = {
                    id: nanoid(),
                    title: source.title || "导入画布",
                    createdAt: source.createdAt || now,
                    updatedAt: now,
                    nodes: source.nodes || [],
                    connections: source.connections || [],
                    chatSessions: source.chatSessions || [],
                    activeChatId: source.activeChatId || null,
                    agentConfig: source.agentConfig || null,
                    autoTitlePending: false,
                    backgroundMode: source.backgroundMode || "lines",
                    showImageInfo: source.showImageInfo || false,
                    viewport: source.viewport || initialViewport,
                    sidePanel: source.sidePanel || DEFAULT_CANVAS_SIDE_PANEL,
                    agentPanel: source.agentPanel || DEFAULT_CANVAS_AGENT_PANEL,
                };
                set((state) => ({
                    projects: [project, ...state.projects],
                }));
                queueProjectSave(project);
                return project.id;
            },
            openProject: (id) =>
                get().projects.find((item) => item.id === id) || null,
            renameProject: (id, title) => {
                const project = get().projects.find(
                    (item) => item.id === id,
                );
                if (!project) return;
                const nextProject = {
                    ...project,
                    title: title.trim() || project.title,
                    autoTitlePending: false,
                    updatedAt: new Date().toISOString(),
                };
                set((state) => ({
                    projects: state.projects.map((item) =>
                        item.id === id ? nextProject : item,
                    ),
                }));
                queueProjectSave(nextProject);
            },
            deleteProjects: (ids) => {
                cancelProjectSaves(ids);
                set((state) => ({
                    projects: state.projects.filter(
                        (project) => !ids.includes(project.id),
                    ),
                }));
            },
            updateProject: (id, patch) => {
                const project = get().projects.find(
                    (item) => item.id === id,
                );
                if (!project) return;
                const nextProject = {
                    ...project,
                    ...patch,
                    updatedAt: new Date().toISOString(),
                };
                set((state) => ({
                    projects: state.projects.map((item) =>
                        item.id === id ? nextProject : item,
                    ),
                }));
                queueProjectSave(nextProject);
            },
            syncWithRemote: async (token, syncEnabled) => {
                const ownerId = getAccountOwnerId();
                accountCanvasSyncEnabled = syncEnabled;
                if (!syncEnabled) return;
                const localProjects = get().projects;
                const remoteProjects = await listCanvasProjects(token).catch(() => null);
                if (!remoteProjects || getAccountOwnerId() !== ownerId || useUserStore.getState().token !== token) return;
                const projects = await reconcileCanvasProjects(token, remoteProjects, localProjects);
                if (!projects || getAccountOwnerId() !== ownerId || useUserStore.getState().token !== token) return;
                if (saveTimer) {
                    clearTimeout(saveTimer);
                    saveTimer = null;
                }
                const nextState = { ownerId, projects };
                queuedPersistState = nextState;
                set({ projects });
                await localForageStorage.setItem(
                    getAccountStorageKey(CANVAS_STORE_KEY, ownerId),
                    JSON.stringify({ state: nextState, version: 0 }),
                );
            },
            setSyncEnabled: (enabled) => {
                accountCanvasSyncEnabled = enabled;
            },
            resetForAccount: () => {
                // 切号时先阻断 persist，避免清空旧账号内存状态的动作把新账号 namespace 覆盖成空数组。
                suppressCanvasPersistence = true;
                projectSaveTimers.forEach((timer) => clearTimeout(timer));
                projectSaveTimers.clear();
                if (saveTimer) clearTimeout(saveTimer);
                saveTimer = null;
                queuedPersistState = null;
                accountCanvasSyncEnabled = false;
                set({ hydrated: false, projects: [] });
            },
        }),
        {
            name: CANVAS_STORE_KEY,
            storage: canvasStorage,
            partialize: (state) =>
                ({
                    ownerId: getAccountOwnerId(),
                    projects: state.projects,
                }) as StorageValue<CanvasStore>["state"],
            onRehydrateStorage: () => () => {
                suppressCanvasPersistence = false;
                useCanvasStore.setState({ hydrated: true });
            },
        },
    ),
);

export function mergeCanvasProjects(
    remoteProjects: CanvasProject[],
    localProjects: CanvasProject[],
): CanvasProject[] {
    const projects = new Map<string, CanvasProject>();
    [...localProjects, ...remoteProjects].forEach((project) => {
        const previous = projects.get(project.id);
        if (
            !previous ||
            Date.parse(project.updatedAt || "") >=
            Date.parse(previous.updatedAt || "")
        ) {
            projects.set(project.id, project);
        }
    });
    return Array.from(projects.values()).sort(
        (a, b) =>
            Date.parse(b.updatedAt || "") -
            Date.parse(a.updatedAt || ""),
    );
}

export function rehydrateCanvasForCurrentAccount() {
    if (useCanvasStore.getState().hydrated) return Promise.resolve();
    if (!canvasRehydratePromise) {
        canvasRehydratePromise = useCanvasStore.persist.rehydrate().finally(() => {
            canvasRehydratePromise = null;
        });
    }
    return canvasRehydratePromise;
}

let activeCanvasOwnerId = getAccountOwnerId();
useUserStore.subscribe((state, previousState) => {
    const nextOwnerId = state.user?.id || GUEST_ACCOUNT_OWNER;
    const previousOwnerId = previousState.user?.id || GUEST_ACCOUNT_OWNER;
    if (nextOwnerId === previousOwnerId || nextOwnerId === activeCanvasOwnerId) return;
    activeCanvasOwnerId = nextOwnerId;
    useCanvasStore.getState().resetForAccount();
    // Zustand persist 只会在初始化时读取一次；切号后必须主动读取新账号 namespace，避免旧账号清空后连新账号本地数据也丢失。
    void rehydrateCanvasForCurrentAccount();
});
