"use client";

import localforage from "localforage";

import { nanoid } from "nanoid";
import { getAccountOwnerId, getAccountStorageKey } from "@/lib/account-scope";
import { readImageMeta } from "@/lib/image-utils";
import { apiGet } from "@/services/api/request";
import type { AiConfig } from "@/stores/use-config-store";
import { useUserStore } from "@/stores/use-user-store";

export type UploadedImage = {
    url: string;
    storageKey: string;
    storageStatus?: "local" | "cloud" | "cleaned";
    storageMessage?: string;
    width: number;
    height: number;
    bytes: number;
    mimeType: string;
};

type UserStorageProviderBase = {
    enabled: boolean;
    name: string;
    endpoint: string;
};

export type UserS3StorageProvider = UserStorageProviderBase & {
    type: "s3";
    region: string;
    bucket: string;
    accessKeyId: string;
    secretAccessKey: string;
    publicBaseUrl: string;
    pathPrefix: string;
};

export type UserWebDAVStorageProvider = UserStorageProviderBase & {
    type: "webdav";
    pathPrefix: string;
    username: string;
    password: string;
};

export type UserStorageProvider = UserS3StorageProvider | UserWebDAVStorageProvider;

type UploadImageOptions = {
    localOnly?: boolean;
};

export type StorageRequestContext = {
    ownerId?: string;
    token?: string | null;
};

export type StorageConfig = {
    mode: string;
    allowUserProvider: boolean;
    allowUserGlobalProvider: boolean;
    autoSyncGeneratedMedia: boolean;
    autoSyncLocalGeneratedMedia: boolean;
};

const store = localforage.createInstance({ name: "infinite-canvas", storeName: "image_files" });
const objectUrls = new Map<string, string>();
const serverUrls = new Map<string, string>();

function serverUrlCacheKey(ownerId: string, id: string) {
    return `${ownerId}:${id}`;
}
export const USER_STORAGE_PROVIDER_KEY = "infinite-canvas:user_storage_provider";
export const USER_WEBDAV_STORAGE_PROVIDER_KEY = "infinite-canvas:user_webdav_storage_provider";
let storageConfigPromise: Promise<StorageConfig> | null = null;

export function canUseGlobalStorage(config: StorageConfig) {
    const user = useUserStore.getState().user;
    return config.mode === "server_sqlite_s3" && Boolean(user && user.role !== "guest" && (user.role === "admin" || config.allowUserGlobalProvider));
}

export function shouldAutoSyncGeneratedMedia(config: StorageConfig, channelMode: AiConfig["channelMode"]) {
    // 必须按生成时的渠道判断，不能让用户后续切换渠道改变历史结果的上传策略。
    return channelMode === "local" ? config.autoSyncLocalGeneratedMedia : config.autoSyncGeneratedMedia;
}

function isLocalNetworkHost(hostname: string) {
    const host = hostname.toLowerCase().replace(/^\[|\]$/g, "");
    if (
        host === "localhost" ||
        host.endsWith(".localhost") ||
        host.endsWith(".local") ||
        host === "host.docker.internal" ||
        host === "::1"
    ) {
        return true;
    }
    if (
        host.includes(":") &&
        (host.startsWith("fc") ||
            host.startsWith("fd") ||
            /^fe[89ab]/.test(host))
    ) {
        return true;
    }
    const parts = host.split(".").map(Number);
    if (
        parts.length !== 4 ||
        parts.some((part) => !Number.isInteger(part) || part < 0 || part > 255)
    ) {
        return false;
    }
    const [a, b] = parts;
    return (
        a === 10 ||
        a === 127 ||
        (a === 172 && b >= 16 && b <= 31) ||
        (a === 192 && b === 168) ||
        (a === 169 && b === 254)
    );
}

export function getProxyUrl(url: string): string {
    if (!url.startsWith("http://") && !url.startsWith("https://")) {
        return url;
    }
    try {
        const parsed = new URL(url);
        if (
            isLocalNetworkHost(parsed.hostname) ||
            (typeof window !== "undefined" &&
                parsed.host === window.location.host)
        ) {
            return url;
        }
    } catch {
        return url;
    }
    return `/api/proxy-image?url=${encodeURIComponent(url)}`;
}

export async function uploadImage(input: string | Blob, options: UploadImageOptions = {}): Promise<UploadedImage> {
    const requestOwnerId = getAccountOwnerId();
    const requestToken = useUserStore.getState().token;
    const url = typeof input === "string" ? getProxyUrl(input) : input;
    let blob: Blob;
    if (typeof url === "string") {
        const response = await fetch(url);
        if (!response.ok) {
            const payload = await response.json().catch(() => null) as { msg?: string } | null;
            throw new Error(payload?.msg || `代理图片拉取失败：${response.status}`);
        }
        const contentType = response.headers.get("content-type") || "";
        if (contentType.includes("application/json")) {
            const payload = await response.json().catch(() => null) as { msg?: string } | null;
            throw new Error(payload?.msg || "代理图片下载失败");
        }
        blob = await response.blob();
    } else {
        blob = url;
    }
    if (!options.localOnly) {
        const serverUpload = await maybeUploadImageToServer(blob, requestToken, requestOwnerId);
        if (serverUpload) return serverUpload;
    }
    const storageKey = `image:${nanoid()}`;
    await store.setItem(storageKey, blob);
    const urlObj = URL.createObjectURL(blob);
    objectUrls.set(storageKey, urlObj);
    const meta = await readImageMeta(urlObj);
    return { url: urlObj, storageKey, width: meta.width, height: meta.height, bytes: blob.size, mimeType: blob.type || meta.mimeType };
}

export async function uploadRemoteImageToServer(url: string, filename: string, requestToken = useUserStore.getState().token, requestOwnerId = getAccountOwnerId()): Promise<UploadedImage> {
    const response = await fetch(getProxyUrl(url));
    if (!response.ok) {
        const payload = await response.json().catch(() => null) as { msg?: string } | null;
        throw new Error(payload?.msg || "代理图片拉取失败：" + response.status);
    }
    const blob = await response.blob();
    const config = await loadStorageConfig();
    const userProvider = config.allowUserProvider ? loadUserStorageProvider(requestOwnerId) : null;
    if (!canUseGlobalStorage(config) && !userProvider) throw new Error("服务端对象存储未启用");
    const token = requestToken;
    if (!token) throw new Error("服务端存储需要先登录");
    const formData = new FormData();
    formData.append("file", blob, filename || "image-" + nanoid() + "." + imageExtension(blob.type));
    if (userProvider) formData.append("provider", JSON.stringify(toProviderPayload(userProvider)));
    const uploadResponse = await fetch("/api/v1/files", { method: "POST", headers: { Authorization: "Bearer " + token }, body: formData });
    const payload = (await uploadResponse.json().catch(() => null)) as { code?: number; msg?: string; data?: UploadedImage } | null;
    if (!uploadResponse.ok || payload?.code !== 0 || !payload.data) throw new Error(payload?.msg || "服务端图片上传失败");
    const meta = await readImageMeta(payload.data.url);
    if (payload.data.storageKey?.startsWith("server:")) serverUrls.set(serverUrlCacheKey(requestOwnerId, payload.data.storageKey.slice("server:".length)), payload.data.url);
    return { ...payload.data, width: payload.data.width || meta.width, height: payload.data.height || meta.height, mimeType: payload.data.mimeType || blob.type || "image/png", bytes: payload.data.bytes || blob.size };
}

export async function saveGeneratedImage(
    input: string | Blob,
    filename: string,
    width: number,
    height: number,
    autoUpload: boolean,
    requestToken = useUserStore.getState().token,
    requestOwnerId = getAccountOwnerId(),
): Promise<UploadedImage> {
    if (!requestToken) throw new Error("保存生成图片需要先登录");
    const blob = await loadImageBlob(input);
    const config = await loadStorageConfig().catch(() => null);
    const userProvider = config?.allowUserProvider ? loadUserStorageProvider(requestOwnerId) : null;
    const formData = new FormData();
    formData.append("file", blob, filename || `image-${nanoid()}.${imageExtension(blob.type)}`);
    formData.append("width", String(width || 0));
    formData.append("height", String(height || 0));
    formData.append("autoUpload", String(autoUpload));
    if (userProvider) formData.append("provider", JSON.stringify(toProviderPayload(userProvider)));
    const response = await fetch("/api/v1/generated-images", {
        method: "POST",
        headers: { Authorization: `Bearer ${requestToken}` },
        body: formData,
    });
    const payload = (await response.json().catch(() => null)) as { code?: number; msg?: string; data?: UploadedImage } | null;
    if (!response.ok || payload?.code !== 0 || !payload.data) throw new Error(payload?.msg || "生成图片保存失败");
    if (getAccountOwnerId() !== requestOwnerId || useUserStore.getState().token !== requestToken) return payload.data;
    const url = await resolveImageUrl(payload.data.storageKey, payload.data.url);
    return {
        ...payload.data,
        url,
        width: payload.data.width || width,
        height: payload.data.height || height,
        bytes: payload.data.bytes || blob.size,
        mimeType: payload.data.mimeType || blob.type || "image/png",
    };
}

export async function uploadGeneratedImageToCloud(
    storageKey: string,
    requestToken = useUserStore.getState().token,
    requestOwnerId = getAccountOwnerId(),
): Promise<UploadedImage> {
    if (!storageKey.startsWith("local:")) throw new Error("图片不在服务器本地存储中");
    if (!requestToken) throw new Error("上传云端需要先登录");
    const id = storageKey.slice("local:".length);
    const config = await loadStorageConfig().catch(() => null);
    const userProvider = config?.allowUserProvider ? loadUserStorageProvider(requestOwnerId) : null;
    const response = await fetch(`/api/v1/generated-images/${encodeURIComponent(id)}/upload`, {
        method: "POST",
        headers: { "Content-Type": "application/json", Authorization: `Bearer ${requestToken}` },
        body: JSON.stringify(userProvider ? { provider: toProviderPayload(userProvider) } : {}),
    });
    const payload = (await response.json().catch(() => null)) as { code?: number; msg?: string; data?: UploadedImage } | null;
    if (!response.ok || payload?.code !== 0 || !payload.data) throw new Error(payload?.msg || "图片上传云端失败");
    const localUrl = objectUrls.get(storageKey);
    if (localUrl) URL.revokeObjectURL(localUrl);
    objectUrls.delete(storageKey);
    if (getAccountOwnerId() !== requestOwnerId || useUserStore.getState().token !== requestToken) return payload.data;
    const url = await resolveImageUrl(payload.data.storageKey, payload.data.url);
    return { ...payload.data, url };
}

export function clearStorageConfigCache() {
    storageConfigPromise = null;
}

export async function resolveImageUrl(storageKey?: string, fallback = "") {
    if (!storageKey) return fallback;
    if (storageKey.startsWith("local:")) {
        const ownerId = getAccountOwnerId();
        const token = useUserStore.getState().token;
        const id = storageKey.slice("local:".length);
        const cached = objectUrls.get(storageKey);
        if (cached) return cached;
        if (!token || !id) return "";
        const response = await fetch(`/api/v1/generated-images/${encodeURIComponent(id)}/content`, { headers: { Authorization: `Bearer ${token}` } }).catch(() => null);
        if (!response?.ok || getAccountOwnerId() !== ownerId || useUserStore.getState().token !== token) return "";
        const blob = await response.blob();
        const url = URL.createObjectURL(blob);
        objectUrls.set(storageKey, url);
        return url;
    }
    if (storageKey.startsWith("server:")) {
        const ownerId = getAccountOwnerId();
        const token = useUserStore.getState().token;
        const id = storageKey.slice("server:".length);
        if (fallback && !fallback.startsWith("blob:") && !fallback.includes("/api/files/")) return fallback;
        const cached = objectUrls.get(storageKey);
        if (cached) return cached;
        const blob = await store.getItem<Blob>(storageKey).catch(() => null);
        if (blob) {
            const url = URL.createObjectURL(blob);
            objectUrls.set(storageKey, url);
            return url;
        }
        const cachedUrl = serverUrls.get(serverUrlCacheKey(ownerId, id));
        if (cachedUrl) return cachedUrl;
        const info = await apiGet<{ publicUrl?: string }>(`/api/files/${encodeURIComponent(id)}`, undefined, token).catch(() => null);
        if (getAccountOwnerId() !== ownerId || useUserStore.getState().token !== token) return fallback;
        if (!info) return fallback;
        if (info.publicUrl) {
            serverUrls.set(serverUrlCacheKey(ownerId, id), info.publicUrl);
            return info.publicUrl;
        }
        if (!token) return fallback;
        const response = await fetch(`/api/files/${encodeURIComponent(id)}/content`, { headers: { Authorization: `Bearer ${token}` } }).catch(() => null);
        if (!response?.ok || getAccountOwnerId() !== ownerId || useUserStore.getState().token !== token) return fallback;
        const privateBlob = await response.blob();
        const url = URL.createObjectURL(privateBlob);
        objectUrls.set(storageKey, url);
        serverUrls.set(serverUrlCacheKey(ownerId, id), url);
        return url;
    }
    const cached = objectUrls.get(storageKey);
    if (cached) return cached;
    const blob = await store.getItem<Blob>(storageKey);
    if (!blob) return fallback;
    const url = URL.createObjectURL(blob);
    objectUrls.set(storageKey, url);
    return url;
}

async function maybeUploadImageToServer(blob: Blob, requestToken: string | null, requestOwnerId: string): Promise<UploadedImage | null> {
    const config = await loadStorageConfig().catch(() => null);
    const userProvider = config?.allowUserProvider ? loadUserStorageProvider(requestOwnerId) : null;
    const canUseGlobalProvider = config ? canUseGlobalStorage(config) : false;
    const useServerStorage = canUseGlobalProvider || Boolean(userProvider);
    if (!config || !useServerStorage) return null;
    const token = requestToken;
    if (!token) {
        if (canUseGlobalProvider) throw new Error("服务端存储需要先登录");
        return null;
    }
    const formData = new FormData();
    formData.append("file", blob, `image-${nanoid()}.${imageExtension(blob.type)}`);
    if (userProvider) formData.append("provider", JSON.stringify(toProviderPayload(userProvider)));
    const response = await fetch("/api/v1/files", { method: "POST", headers: { Authorization: `Bearer ${token}` }, body: formData });
    const payload = (await response.json().catch(() => null)) as { code?: number; msg?: string; data?: UploadedImage } | null;
    if (!response.ok || payload?.code !== 0 || !payload.data) {
        if (!canUseGlobalProvider) return null;
        throw new Error(payload?.msg || "服务端图片上传失败");
    }
    const meta = await readImageMeta(payload.data.url);
    if (payload.data.storageKey?.startsWith("server:")) serverUrls.set(serverUrlCacheKey(requestOwnerId, payload.data.storageKey.slice("server:".length)), payload.data.url);
    return { ...payload.data, width: payload.data.width || meta.width, height: payload.data.height || meta.height, mimeType: payload.data.mimeType || blob.type || "image/png", bytes: payload.data.bytes || blob.size };
}

export async function loadStorageConfig() {
    storageConfigPromise ||= apiGet<StorageConfig>("/api/storage/config");
    return storageConfigPromise;
}

function imageExtension(mimeType: string) {
    if (mimeType === "image/jpeg") return "jpg";
    if (mimeType === "image/webp") return "webp";
    return "png";
}

export async function getImageBlob(storageKey: string) {
    return store.getItem<Blob>(storageKey);
}

export async function setImageBlob(storageKey: string, blob: Blob) {
    await store.setItem(storageKey, blob);
    const url = URL.createObjectURL(blob);
    objectUrls.set(storageKey, url);
    return url;
}

export async function imageToDataUrl(image: { url?: string; dataUrl?: string; storageKey?: string }) {
    const resolvedStorageUrl = await resolveImageUrl(image.storageKey, image.url || image.dataUrl || "");
    const urls = [
        image.dataUrl && !image.dataUrl.startsWith("blob:") && !image.dataUrl.includes("/api/files/") ? image.dataUrl : "",
        image.url && !image.url.startsWith("blob:") && !image.url.includes("/api/files/") ? image.url : "",
        resolvedStorageUrl,
    ].filter((url, index, list): url is string => Boolean(url) && list.indexOf(url) === index);
    if (!urls.length) return "";
    let lastError = "";
    for (const url of urls) {
        if (url.startsWith("data:")) return url;
        try {
            const proxyUrl = getProxyUrl(url);
            const response = await fetch(proxyUrl);
            if (!response.ok) {
                lastError = `读取参考图失败：${response.status}`;
                continue;
            }
            return blobToDataUrl(await response.blob());
        } catch (error) {
            lastError = error instanceof Error ? error.message : "读取参考图失败";
        }
    }
    throw new Error(lastError || "读取参考图失败");
}

export async function deleteStoredImages(keys: Iterable<string>, request: StorageRequestContext = {}) {
    const ownerId = request.ownerId ?? getAccountOwnerId();
    const token = request.token !== undefined ? request.token : useUserStore.getState().token;
    const { useAssetStore } = await import("@/stores/use-asset-store");
    const assetKeys = new Set(
        useAssetStore.getState().assets
            .map((a) => (a.kind !== "text" ? a.data.storageKey : null))
            .filter((k): k is string => Boolean(k))
    );
    await Promise.all(
        Array.from(new Set(keys)).map(async (key) => {
            if (assetKeys.has(key)) return;
            if (key.startsWith("server:")) {
                await deleteServerImage(key, ownerId, token);
                return;
            }
            if (key.startsWith("local:")) {
                await deleteLocalGeneratedImage(key, ownerId, token);
                return;
            }
            const url = objectUrls.get(key);
            if (url) URL.revokeObjectURL(url);
            objectUrls.delete(key);
            await store.removeItem(key);
        }),
    );
}

export async function cleanupUnusedImages(usedData: unknown) {
    const usedKeys = collectImageStorageKeys(usedData);
    const unused: string[] = [];
    await store.iterate((_value, key) => {
        if (!usedKeys.has(key)) unused.push(key);
    });
    await deleteStoredImages(unused);
}

export function collectImageStorageKeys(value: unknown, keys = new Set<string>()) {
    if (typeof value === "string") {
        if (value.startsWith("image:") || value.startsWith("server:") || value.startsWith("local:")) keys.add(value);
        return keys;
    }
    if (!value || typeof value !== "object") return keys;
    if ("storageKey" in value && typeof value.storageKey === "string" && (value.storageKey.startsWith("image:") || value.storageKey.startsWith("server:") || value.storageKey.startsWith("local:"))) keys.add(value.storageKey);
    Object.values(value).forEach((item) => (Array.isArray(item) ? item.forEach((child) => collectImageStorageKeys(child, keys)) : collectImageStorageKeys(item, keys)));
    return keys;
}

export function defaultUserStorageProvider(): UserS3StorageProvider {
    return {
        enabled: false,
        name: "我的 R2",
        type: "s3",
        endpoint: "",
        region: "auto",
        bucket: "",
        accessKeyId: "",
        secretAccessKey: "",
        publicBaseUrl: "",
        pathPrefix: "canvas",
    };
}

export function defaultUserWebDAVStorageProvider(): UserWebDAVStorageProvider {
    return {
        enabled: false,
        name: "我的 WebDAV",
        type: "webdav",
        endpoint: "",
        pathPrefix: "canvas",
        username: "",
        password: "",
    };
}

export function loadUserS3StorageProvider(ownerId = getAccountOwnerId()) {
    if (typeof window === "undefined") return null;
    try {
        const parsed = JSON.parse(window.localStorage.getItem(getAccountStorageKey(USER_STORAGE_PROVIDER_KEY, ownerId)) || "null") as UserS3StorageProvider | null;
        return parsed ? { ...defaultUserStorageProvider(), ...parsed, type: "s3" as const } : null;
    } catch {
        return null;
    }
}

export function loadUserWebDAVStorageProvider(ownerId = getAccountOwnerId()) {
    if (typeof window === "undefined") return null;
    try {
        const parsed = JSON.parse(window.localStorage.getItem(getAccountStorageKey(USER_WEBDAV_STORAGE_PROVIDER_KEY, ownerId)) || "null") as UserWebDAVStorageProvider | null;
        return parsed ? { ...defaultUserWebDAVStorageProvider(), ...parsed, type: "webdav" as const } : null;
    } catch {
        return null;
    }
}

export function loadUserStorageProvider(ownerId = getAccountOwnerId()): UserStorageProvider | null {
    const s3 = loadUserS3StorageProvider(ownerId);
    const webdav = loadUserWebDAVStorageProvider(ownerId);
    if (s3?.enabled && webdav?.enabled) return null;
    if (s3?.enabled && validS3Provider(s3)) return s3;
    if (webdav?.enabled && validWebDAVProvider(webdav)) return webdav;
    return null;
}

export function saveUserStorageProvider(provider: UserS3StorageProvider) {
    window.localStorage.setItem(getAccountStorageKey(USER_STORAGE_PROVIDER_KEY, ownerId), JSON.stringify({ ...defaultUserStorageProvider(), ...provider, type: "s3" }));
}

export function saveUserWebDAVStorageProvider(provider: UserWebDAVStorageProvider) {
    window.localStorage.setItem(getAccountStorageKey(USER_WEBDAV_STORAGE_PROVIDER_KEY, ownerId), JSON.stringify({ ...defaultUserWebDAVStorageProvider(), ...provider, type: "webdav" }));
}

function validS3Provider(provider: UserS3StorageProvider) {
    return Boolean(provider.endpoint && provider.bucket && provider.accessKeyId && provider.secretAccessKey);
}

function validWebDAVProvider(provider: UserWebDAVStorageProvider) {
    return Boolean(provider.endpoint && provider.username && provider.password);
}

export function toProviderPayload(provider: UserStorageProvider) {
    if (provider.type === "webdav") {
        return {
            enabled: provider.enabled,
            name: provider.name,
            type: "webdav" as const,
            endpoint: provider.endpoint,
            pathPrefix: provider.pathPrefix,
            username: provider.username,
            password: provider.password,
        };
    }
    return {
        enabled: provider.enabled,
        name: provider.name,
        type: "s3" as const,
        endpoint: provider.endpoint,
        region: provider.region || "auto",
        bucket: provider.bucket,
        accessKeyId: provider.accessKeyId,
        secretAccessKey: provider.secretAccessKey,
        publicBaseUrl: provider.publicBaseUrl,
        pathPrefix: provider.pathPrefix,
    };
}

async function loadImageBlob(input: string | Blob) {
    if (input instanceof Blob) return input;
    const response = await fetch(getProxyUrl(input));
    if (!response.ok) {
        const payload = (await response.json().catch(() => null)) as { msg?: string } | null;
        throw new Error(payload?.msg || `代理图片拉取失败：${response.status}`);
    }
    return response.blob();
}

async function deleteLocalGeneratedImage(storageKey: string, ownerId = getAccountOwnerId(), token = useUserStore.getState().token) {
    const id = storageKey.slice("local:".length);
    const url = objectUrls.get(storageKey);
    if (url) URL.revokeObjectURL(url);
    objectUrls.delete(storageKey);
    if (!id || !token) return;
    const provider = loadUserStorageProvider(ownerId);
    const response = await fetch(`/api/v1/generated-images/${encodeURIComponent(id)}`, {
        method: "DELETE",
        headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` },
        body: JSON.stringify(provider ? { provider: toProviderPayload(provider) } : {}),
    });
    const payload = (await response.json().catch(() => null)) as { code?: number; msg?: string } | null;
    if (!response.ok || payload?.code !== 0) throw new Error(payload?.msg || "删除本地生成图片失败");
}

async function deleteServerImage(storageKey: string, ownerId = getAccountOwnerId(), token = useUserStore.getState().token) {
    const id = storageKey.slice("server:".length);
    if (!id) return;
    serverUrls.delete(serverUrlCacheKey(ownerId, id));
    if (!token) return;
    const provider = loadUserStorageProvider(ownerId);
    const response = await fetch(`/api/v1/files/${encodeURIComponent(id)}`, {
        method: "DELETE",
        headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` },
        body: JSON.stringify(provider ? { provider: toProviderPayload(provider) } : {}),
    });
    const payload = (await response.json().catch(() => null)) as { code?: number; msg?: string } | null;
    if (!response.ok || payload?.code !== 0) throw new Error(payload?.msg || "删除服务端图片失败");
}

function blobToDataUrl(blob: Blob) {
    return new Promise<string>((resolve, reject) => {
        const reader = new FileReader();
        reader.onload = () => resolve(String(reader.result || ""));
        reader.onerror = () => reject(new Error("读取图片失败"));
        reader.readAsDataURL(blob);
    });
}
