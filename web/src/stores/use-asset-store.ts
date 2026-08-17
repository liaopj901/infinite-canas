"use client";

import { create } from "zustand";
import { persist, type PersistStorage, type StorageValue } from "zustand/middleware";

import { nanoid } from "nanoid";
import { getAccountOwnerId, getAccountStorageKey, GUEST_ACCOUNT_OWNER } from "@/lib/account-scope";
import { localForageStorage } from "@/lib/localforage-storage";
import { isProtectedImageUrl, resolveImageUrl, uploadImage } from "@/services/image-storage";
import { resolveMediaUrl } from "@/services/file-storage";
import { fetchUserAssetData, syncUserAssetData } from "@/services/api/user-config";
import { useUserStore } from "@/stores/use-user-store";

export type AssetKind = "text" | "image" | "video" | "audio";
export type TextAsset = AssetBase<"text"> & { data: { content: string } };
export type ImageAsset = AssetBase<"image"> & { data: { dataUrl: string; storageKey?: string; width: number; height: number; bytes: number; mimeType: string } };
export type VideoAsset = AssetBase<"video"> & { data: { url: string; storageKey?: string; width: number; height: number; bytes: number; mimeType: string } };
export type AudioAsset = AssetBase<"audio"> & { data: { url: string; storageKey?: string; bytes?: number; mimeType: string; durationMs?: number } };
export type Asset = TextAsset | ImageAsset | VideoAsset | AudioAsset;

type AssetBase<T extends AssetKind> = {
    id: string;
    kind: T;
    title: string;
    coverUrl: string;
    tags: string[];
    source?: string;
    note?: string;
    createdAt: string;
    updatedAt: string;
    metadata?: Record<string, unknown>;
};

type AssetStore = {
    assets: Asset[];
    addAsset: (asset: Omit<Asset, "id" | "createdAt" | "updatedAt">) => string;
    updateAsset: (id: string, patch: Partial<Omit<Asset, "id" | "createdAt">>) => void;
    removeAsset: (id: string) => void;
    hydrateAccountAssets: (token: string, syncEnabled?: boolean) => Promise<void>;
    syncAccountAssets: (token: string) => Promise<void>;
    stopAccountAssetSync: () => void;
    cleanupImages: (extra?: unknown) => void;
};

const ASSET_STORE_KEY = "infinite-canvas:asset_store";
let activeAssetSyncToken = "";
let activeAssetOwnerId = getAccountOwnerId();
let accountAssetSyncEnabled = false;
let isHydratingAccountAssets = false;
let suppressAssetPersistence = false;
let assetRehydratePromise: Promise<void> | null = null;
let syncTimer: number | null = null;

type AssetSnapshot = { assets: Asset[] };
type PersistedAssetState = AssetSnapshot & { ownerId: string };

const assetStorage: PersistStorage<AssetStore> = {
    getItem: async (name) => {
        const ownerId = getAccountOwnerId();
        const value = await localForageStorage.getItem(getAccountStorageKey(name, ownerId));
        if (!value) return null;
        const parsed = JSON.parse(value) as StorageValue<AssetStore>;
        const persistedState = parsed.state as PersistedAssetState;
        if (persistedState.ownerId !== ownerId) return null;
        const normalizedAssets = await resolveAssetUrls(parsed.state.assets);
        parsed.state.assets = normalizedAssets;
        const nextState = { ...(parsed.state as PersistedAssetState), assets: normalizedAssets };
        const nextParsed = { ...parsed, state: nextState };
        await localForageStorage.setItem(getAccountStorageKey(name, ownerId), JSON.stringify(nextParsed));
        return nextParsed;
    },
    setItem: (name, value) => {
        if (suppressAssetPersistence) return;
        const ownerId = getAccountOwnerId();
        const state = { ...(value.state as AssetSnapshot), ownerId };
        return localForageStorage.setItem(getAccountStorageKey(name, ownerId), JSON.stringify({ ...value, state }));
    },
    removeItem: (name) => localForageStorage.removeItem(getAccountStorageKey(name)),
};

export const useAssetStore = create<AssetStore>()(
    persist(
        (set, get) => ({
            assets: [],
            addAsset: (asset) => {
                const now = new Date().toISOString();
                const id = nanoid();
                set((state) => ({ assets: [{ ...asset, id, createdAt: now, updatedAt: now } as Asset, ...state.assets] }));
                scheduleAssetSync(get);
                return id;
            },
            updateAsset: (id, patch) =>
                set((state) => {
                    const assets = state.assets.map((asset) => (asset.id === id ? ({ ...asset, ...patch, updatedAt: new Date().toISOString() } as Asset) : asset));
                    window.setTimeout(() => scheduleAssetSync(get), 0);
                    return { assets };
                }),
            removeAsset: (id) =>
                set((state) => {
                    const deletedAsset = state.assets.find((asset) => asset.id === id);
                    const assets = state.assets.filter((asset) => asset.id !== id);

                    if (deletedAsset && deletedAsset.kind !== "text" && deletedAsset.data.storageKey) {
                        const key = deletedAsset.data.storageKey;
                        const ownerId = getAccountOwnerId();
                        const taskToken = useUserStore.getState().token;
                        window.setTimeout(async () => {
                            if (getAccountOwnerId() !== ownerId || useUserStore.getState().token !== taskToken) return;
                            const { useCanvasStore } = await import("@/app/(user)/canvas/stores/use-canvas-store");
                            const usedKeys = new Set<string>();
                            // 收集其余资产的 storageKey
                            assets.forEach((a) => {
                                if (a.kind !== "text" && a.data.storageKey) usedKeys.add(a.data.storageKey);
                            });
                            // 收集画布中引用的 storageKey
                            const projects = useCanvasStore.getState().projects;
                            const { collectImageStorageKeys } = await import("@/services/image-storage");
                            const { collectMediaStorageKeys } = await import("@/services/file-storage");
                            collectImageStorageKeys(projects, usedKeys);
                            collectMediaStorageKeys(projects, usedKeys);

                            // 收集本地/云端生图历史与视频历史中的 storageKey，避免生成结果卡片失效
                            try {
                                const localforage = (await import("localforage")).default;
                                const imageLogStore = localforage.createInstance({ name: "infinite-canvas", storeName: "image_generation_logs" });
                                const imageLogPrefix = getAccountStorageKey("infinite-canvas:image_generation_logs", ownerId) + ":";
                                await imageLogStore.iterate((log: any, logKey) => {
                                    if (!logKey.startsWith(imageLogPrefix)) return;
                                    if (log) {
                                        if (Array.isArray(log.images)) {
                                            log.images.forEach((img: any) => {
                                                if (img && img.storageKey) usedKeys.add(img.storageKey);
                                            });
                                        }
                                        if (Array.isArray(log.references)) {
                                            log.references.forEach((ref: any) => {
                                                if (ref && ref.storageKey) usedKeys.add(ref.storageKey);
                                            });
                                        }
                                    }
                                });
                            } catch (e) {
                                console.error("Error iterating image_generation_logs", e);
                            }

                            try {
                                const localforage = (await import("localforage")).default;
                                const videoLogStore = localforage.createInstance({ name: "infinite-canvas", storeName: "video_generation_logs" });
                                const videoLogPrefix = getAccountStorageKey("infinite-canvas:video_generation_logs", ownerId) + ":";
                                await videoLogStore.iterate((log: any, logKey) => {
                                    if (!logKey.startsWith(videoLogPrefix)) return;
                                    if (log) {
                                        if (log.video && log.video.storageKey) {
                                            usedKeys.add(log.video.storageKey);
                                        }
                                        if (Array.isArray(log.references)) {
                                            log.references.forEach((ref: any) => {
                                                if (ref && ref.storageKey) usedKeys.add(ref.storageKey);
                                            });
                                        }
                                    }
                                });
                            } catch (e) {
                                console.error("Error iterating video_generation_logs", e);
                            }

                            // 若全站没有其他地方再引用此 storageKey，则执行真正的物理删除
                            if (!usedKeys.has(key)) {
                                if (key.startsWith("image:") || key.startsWith("server:")) {
                                    const { deleteStoredImages } = await import("@/services/image-storage");
                                    await deleteStoredImages([key], { ownerId, token: taskToken });
                                }
                                if (key.startsWith("file:") || key.startsWith("video:") || key.startsWith("server:")) {
                                    const { deleteStoredMedia } = await import("@/services/file-storage");
                                    await deleteStoredMedia([key], { ownerId, token: taskToken });
                                }
                            }
                        }, 0);
                    }

                    window.setTimeout(() => scheduleAssetSync(get), 0);
                    return { assets };
                }),
            hydrateAccountAssets: async (token, syncEnabled = false) => {
                if (!token) return;
                const ownerId = getAccountOwnerId();
                activeAssetOwnerId = ownerId;
                activeAssetSyncToken = token;
                accountAssetSyncEnabled = syncEnabled;
                isHydratingAccountAssets = true;
                try {
                    const remote = await fetchUserAssetData<AssetSnapshot>(token);
                    if (getAccountOwnerId() !== ownerId || useUserStore.getState().token !== token) return;
                    const remoteAssets = Array.isArray(remote?.assets) ? remote.assets : [];
                    const shouldUseRemote = syncEnabled || (!get().assets.length && remoteAssets.length > 0);
                    if (!shouldUseRemote) return;
                    // 云端 JSON 里的 blob URL 只属于创建它的页面，必须按 storageKey 重新生成当前页面可用的地址。
                    const resolvedAssets = await resolveAssetUrls(remoteAssets);
                    if (getAccountOwnerId() !== ownerId || useUserStore.getState().token !== token) return;
                    set({ assets: resolvedAssets });
                } finally {
                    isHydratingAccountAssets = false;
                }
            },
            syncAccountAssets: async (token) => {
                if (!token || !accountAssetSyncEnabled || getAccountOwnerId() !== activeAssetOwnerId || useUserStore.getState().token !== token) return;
                await syncUserAssetData(token, { assets: assetsForSync(get().assets) });
            },
            stopAccountAssetSync: () => {
                activeAssetSyncToken = "";
                accountAssetSyncEnabled = false;
                if (syncTimer) window.clearTimeout(syncTimer);
                syncTimer = null;
            },
            cleanupImages: () => {
                // 本地文件仓库没有记录文件所属账号，禁止按单个账号的引用集合扫描全局文件。
                // 具体资源删除由 removeAsset 等路径在当前账号范围内完成，宁可暂留孤儿文件也不能误删其他账号数据。
            },
        }),
        {
            name: ASSET_STORE_KEY,
            storage: assetStorage,
            partialize: (state) => ({ assets: state.assets }) as StorageValue<AssetStore>["state"],
        },
    ),
);

async function resolveAssetUrls(assets: Asset[]) {
    return Promise.all(assets.map(resolveAssetUrl));
}

async function resolveAssetUrl(asset: Asset): Promise<Asset> {
    if (asset.kind === "video" || asset.kind === "audio") {
        if (!asset.data.storageKey) return asset;
        const url = await resolveMediaUrl(asset.data.storageKey, asset.data.url);
        const coverUrl = asset.coverUrl === asset.data.url || asset.coverUrl.startsWith("blob:") ? url : asset.coverUrl;
        return { ...asset, coverUrl, data: { ...asset.data, url } } as Asset;
    }
    if (asset.kind !== "image") return asset;
    if (asset.data.storageKey) {
        const dataUrl = await resolveImageUrl(asset.data.storageKey, asset.data.dataUrl);
        const replaceCover = asset.coverUrl === asset.data.dataUrl || asset.coverUrl.startsWith("blob:") || isProtectedImageUrl(asset.coverUrl);
        return { ...asset, coverUrl: replaceCover ? dataUrl : asset.coverUrl, data: { ...asset.data, dataUrl } };
    }
    if (!asset.data.dataUrl.startsWith("data:image/")) return asset;
    // 恢复历史素材只补齐浏览器本地 storageKey，不能在读取缓存时隐式创建服务器文件。
    const image = await uploadImage(asset.data.dataUrl, { localOnly: true });
    return { ...asset, coverUrl: asset.coverUrl.startsWith("data:image/") ? image.url : asset.coverUrl, data: { ...asset.data, dataUrl: image.url, storageKey: image.storageKey, bytes: image.bytes, mimeType: image.mimeType } };
}

function assetsForSync(assets: Asset[]) {
    return assets.map((asset) => {
        if (asset.kind === "image" && asset.data.storageKey) {
            const dataUrl = stableStorageUrl(asset.data.storageKey, asset.data.dataUrl);
            const coverUrl = asset.coverUrl === asset.data.dataUrl || asset.coverUrl.startsWith("blob:") ? dataUrl : asset.coverUrl;
            return { ...asset, coverUrl, data: { ...asset.data, dataUrl } };
        }
        if ((asset.kind === "video" || asset.kind === "audio") && asset.data.storageKey) {
            const url = stableStorageUrl(asset.data.storageKey, asset.data.url);
            const coverUrl = asset.coverUrl === asset.data.url || asset.coverUrl.startsWith("blob:") ? url : asset.coverUrl;
            return { ...asset, coverUrl, data: { ...asset.data, url } };
        }
        return asset;
    });
}

function stableStorageUrl(storageKey: string, currentUrl: string) {
    if (!currentUrl.startsWith("blob:")) return currentUrl;
    if (storageKey.startsWith("local:")) return `/api/v1/generated-images/${encodeURIComponent(storageKey.slice("local:".length))}/content`;
    if (storageKey.startsWith("server:")) return `/api/files/${encodeURIComponent(storageKey.slice("server:".length))}/content`;
    return currentUrl;
}

function scheduleAssetSync(get: () => AssetStore) {
    if (isHydratingAccountAssets || !activeAssetSyncToken || !accountAssetSyncEnabled || typeof window === "undefined") return;
    if (syncTimer) window.clearTimeout(syncTimer);
    syncTimer = window.setTimeout(() => {
        void get().syncAccountAssets(activeAssetSyncToken).catch(() => {});
    }, 600);
}

export function mergeAssets(remoteAssets: Asset[], localAssets: Asset[]) {
    const records = new Map<string, Asset>();
    [...localAssets, ...remoteAssets].forEach((asset) => {
        const previous = records.get(asset.id);
        if (!previous || Date.parse(asset.updatedAt || "") >= Date.parse(previous.updatedAt || "")) {
            records.set(asset.id, asset);
        }
    });
    return Array.from(records.values()).sort((a, b) => Date.parse(b.updatedAt || "") - Date.parse(a.updatedAt || ""));
}


export function rehydrateAssetsForCurrentAccount() {
    if (!assetRehydratePromise) {
        assetRehydratePromise = useAssetStore.persist.rehydrate().finally(() => {
            assetRehydratePromise = null;
        });
    }
    return assetRehydratePromise;
}

useUserStore.subscribe((state, previousState) => {
    const nextOwnerId = state.user?.id || GUEST_ACCOUNT_OWNER;
    const previousOwnerId = previousState.user?.id || GUEST_ACCOUNT_OWNER;
    if (nextOwnerId === previousOwnerId || nextOwnerId === activeAssetOwnerId) return;
    activeAssetOwnerId = nextOwnerId;
    useAssetStore.getState().stopAccountAssetSync();
    suppressAssetPersistence = true;
    useAssetStore.setState({ assets: [] });
    // 账号 namespace 变化后重新读取对应本地素材，不能把切号后的空状态当成新账号数据保存。
    void rehydrateAssetsForCurrentAccount().finally(() => {
        if (activeAssetOwnerId === nextOwnerId) suppressAssetPersistence = false;
    });
});
