"use client";

import localforage from "localforage";

import { nanoid } from "nanoid";
import { getAccountOwnerId, getAccountStorageKey } from "@/lib/account-scope";
import { readImageMeta } from "@/lib/image-utils";
import { deleteAnonymousStorageFile, uploadAnonymousStorageFile } from "@/services/anonymous-storage";
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

function protectedImagePath(value?: string) {
    if (!value) return "";
    try {
        const appOrigin = typeof window === "undefined" ? "http://local.invalid" : window.location.origin;
        const url = new URL(value, appOrigin);
        if (!/^\/api\/(?:files\/[^/]+\/content|v1\/generated-images\/[^/]+\/content)$/.test(url.pathname)) {
            return "";
        }
        return `${url.pathname}${url.search}`;
    } catch {
        return "";
    }
}

export function isProtectedImageUrl(value?: string) {
    return Boolean(protectedImagePath(value));
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
    const blob = await loadImageBlob(typeof input === "string" ? getProxyUrl(input) : input);

    if (options.localOnly) return saveImageToBrowser(blob);
    if (requestToken) {
        return uploadImageBlobToGeneratedMedia(blob, `image-${nanoid()}.${imageExtension(blob.type)}`, requestToken, requestOwnerId);
    }

    const uploaded = await maybeUploadImageToServer(blob, requestOwnerId);
    return uploaded || saveImageToBrowser(blob);
}

export async function uploadRemoteImageToServer(
    url: string,
    filename: string,
    requestToken = useUserStore.getState().token,
    requestOwnerId = getAccountOwnerId(),
): Promise<UploadedImage> {
    const blob = await loadImageBlob(getProxyUrl(url));
    const config = await loadStorageConfig().catch(() => null);
    const userProvider = config?.allowUserProvider ? loadUserStorageProvider(requestOwnerId) : null;
    if (!config || (!canUseGlobalStorage(config) && !userProvider)) throw new Error("服务端对象存储未启用");

    if (
        userProvider?.type === "webdav" &&
        getAccountOwnerId() === requestOwnerId &&
        useUserStore.getState().token === requestToken
    ) {
        const directUpload = await uploadWebDAVImageDirect(blob, filename || `image-${nanoid()}.${imageExtension(blob.type)}`, userProvider, requestOwnerId);
        if (directUpload) return directUpload;
    }

    if (!requestToken) {
        if (!userProvider) throw new Error("服务端存储需要先登录");
        const uploaded = await uploadAnonymousStorageFile<UploadedImage>(blob, filename || `image-${nanoid()}.${imageExtension(blob.type)}`, toProviderPayload(userProvider));
        return cacheAnonymousImage(uploaded, blob, requestOwnerId);
    }

    return uploadImageBlobToGeneratedMedia(blob, filename || `image-${nanoid()}.${imageExtension(blob.type)}`, requestToken, requestOwnerId);
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
    if (!url) throw new Error("生成图片已保存，但服务器无法读取本地文件");
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
        return setImageBlob(storageKey, await response.blob());
    }

    if (storageKey.startsWith("server:webdav:")) {
        const localUrl = await resolveLocalImageUrl(storageKey).catch(() => "");
        if (localUrl) return localUrl;
        const provider = loadUserStorageProvider();
        if (provider?.type !== "webdav") return fallback;
        const direct = await import("@/services/webdav-direct-storage");
        return setImageBlob(storageKey, await direct.readDirectWebDAV(provider, direct.directWebDAVObjectKey(storageKey)));
    }

    if (!storageKey.startsWith("server:")) return await resolveLocalImageUrl(storageKey) || fallback;
    const ownerId = getAccountOwnerId();
    const token = useUserStore.getState().token;
    const id = storageKey.slice("server:".length);
    if (fallback && !fallback.startsWith("blob:") && !isProtectedImageUrl(fallback) && !fallback.includes("direct=1") && !fallback.startsWith("/webdav-media/")) return fallback;

    const localUrl = await resolveLocalImageUrl(storageKey).catch(() => "");
    if (localUrl) return localUrl;

    const cacheKey = serverUrlCacheKey(ownerId, id);
    const cachedUrl = serverUrls.get(cacheKey);
    if (cachedUrl && !isProtectedImageUrl(cachedUrl)) return cachedUrl;

    const info = await apiGet<{ publicUrl?: string; direct?: boolean; objectKey?: string; mimeType?: string }>(`/api/files/${encodeURIComponent(id)}`, undefined, token).catch(() => null);
    if (getAccountOwnerId() !== ownerId || useUserStore.getState().token !== token || !info) return fallback;

    const provider = loadUserStorageProvider(ownerId);
    if (info.direct && info.objectKey && provider?.type === "webdav") {
        const direct = await import("@/services/webdav-direct-storage");
        try {
            return setImageBlob(storageKey, await direct.readDirectWebDAV(provider, info.objectKey, info.mimeType));
        } catch (error) {
            if (!token || !direct.isWebDAVDirectUnavailable(error)) throw error;
        }
    }

    if (info.publicUrl) {
        serverUrls.set(cacheKey, info.publicUrl);
        return info.publicUrl;
    }
    if (!token) return fallback;

    // 私有对象只允许当前浏览器携带令牌读取，不能把裸接口地址交给模型或外部页面。
    const contentUrl = protectedImagePath(cachedUrl) || protectedImagePath(fallback) || `/api/files/${encodeURIComponent(id)}/content`;
    const response = await fetch(contentUrl, { headers: { Authorization: `Bearer ${token}` } }).catch(() => null);
    if (!response?.ok || getAccountOwnerId() !== ownerId || useUserStore.getState().token !== token) return fallback;
    const url = await setImageBlob(storageKey, await response.blob());
    serverUrls.set(cacheKey, url);
    return url;
}

async function resolveLocalImageUrl(storageKey: string) {
    const cached = objectUrls.get(storageKey);
    if (cached) return cached;
    const blob = await store.getItem<Blob>(storageKey);
    if (!blob) return "";
    const url = URL.createObjectURL(blob);
    objectUrls.set(storageKey, url);
    return url;
}

async function uploadImageBlobToGeneratedMedia(blob: Blob, filename: string, requestToken: string, requestOwnerId: string) {
    const previewUrl = URL.createObjectURL(blob);
    try {
        const meta = await readImageMeta(previewUrl);
        const config = await loadStorageConfig().catch(() => null);
        const userProvider = config?.allowUserProvider ? loadUserStorageProvider(requestOwnerId) : null;
        const autoUpload = Boolean(config && (canUseGlobalStorage(config) || userProvider));
        // 登录用户先保存在生成媒体目录；对象存储同步失败不应让素材和参考图丢失。
        return await saveGeneratedImage(blob, filename, meta.width, meta.height, autoUpload, requestToken, requestOwnerId);
    } finally {
        URL.revokeObjectURL(previewUrl);
    }
}

async function maybeUploadImageToServer(blob: Blob, requestOwnerId: string): Promise<UploadedImage | null> {
    const config = await loadStorageConfig().catch(() => null);
    const userProvider = config?.allowUserProvider ? loadUserStorageProvider(requestOwnerId) : null;
    const canUseGlobalProvider = config ? canUseGlobalStorage(config) : false;
    if (!config || (!canUseGlobalProvider && !userProvider)) return null;

    const token = useUserStore.getState().token;
    if (token) return null;
    if (!userProvider) {
        if (canUseGlobalProvider) throw new Error("服务端存储需要先登录");
        return null;
    }

    if (userProvider.type === "webdav" && getAccountOwnerId() === requestOwnerId) {
        const directUpload = await uploadWebDAVImageDirect(blob, `image-${nanoid()}.${imageExtension(blob.type)}`, userProvider, requestOwnerId);
        if (directUpload) return directUpload;
    }

    try {
        const uploaded = await uploadAnonymousStorageFile<UploadedImage>(blob, `image-${nanoid()}.${imageExtension(blob.type)}`, toProviderPayload(userProvider));
        return cacheAnonymousImage(uploaded, blob, requestOwnerId);
    } catch {
        return null;
    }
}

async function uploadWebDAVImageDirect(blob: Blob, filename: string, provider: UserWebDAVStorageProvider, ownerId = getAccountOwnerId()): Promise<UploadedImage | null> {
    const direct = await import("@/services/webdav-direct-storage");
    const uploaded = await direct.persistDirectWebDAV(provider, blob, filename);
    return uploaded ? cacheAnonymousImage({ ...uploaded, width: 0, height: 0 }, blob, ownerId) : null;
}

async function cacheAnonymousImage(uploaded: UploadedImage, blob: Blob, ownerId = getAccountOwnerId()) {
    await store.setItem(uploaded.storageKey, blob);
    const url = URL.createObjectURL(blob);
    objectUrls.set(uploaded.storageKey, url);
    if (uploaded.storageKey.startsWith("server:") && uploaded.url && !isProtectedImageUrl(uploaded.url)) {
        serverUrls.set(serverUrlCacheKey(ownerId, uploaded.storageKey.slice("server:".length)), uploaded.url);
    }
    const meta = await readImageMeta(url);
    return { ...uploaded, url, width: uploaded.width || meta.width, height: uploaded.height || meta.height, mimeType: uploaded.mimeType || blob.type || meta.mimeType, bytes: uploaded.bytes || blob.size };
}

async function saveImageToBrowser(blob: Blob): Promise<UploadedImage> {
    const storageKey = `image:${nanoid()}`;
    await store.setItem(storageKey, blob);
    const url = URL.createObjectURL(blob);
    objectUrls.set(storageKey, url);
    const meta = await readImageMeta(url);
    return { url, storageKey, width: meta.width, height: meta.height, bytes: blob.size, mimeType: blob.type || meta.mimeType };
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
    const storageKey = image.storageKey;
    const serverObjectId = storageKey?.startsWith("server:") && !storageKey.startsWith("server:webdav:") ? storageKey.slice("server:".length) : "";
    const resolvedStorageUrl = storageKey ? await resolveImageUrl(storageKey, image.url || image.dataUrl || "") : "";
    const token = useUserStore.getState().token;
    const urls = [
        image.dataUrl && !image.dataUrl.startsWith("blob:") && !isProtectedImageUrl(image.dataUrl) ? image.dataUrl : "",
        image.url && !image.url.startsWith("blob:") && !isProtectedImageUrl(image.url) ? image.url : "",
        resolvedStorageUrl,
        serverObjectId && token ? `/api/files/${encodeURIComponent(serverObjectId)}/content` : "",
    ].filter((url, index, list): url is string => Boolean(url) && list.indexOf(url) === index);
    if (!urls.length) return "";

    let lastError = "";
    for (const url of urls) {
        if (url.startsWith("data:")) return url;
        try {
            const protectedPath = protectedImagePath(url);
            const response = await fetch(protectedPath || getProxyUrl(url), protectedPath && token ? { headers: { Authorization: `Bearer ${token}` } } : undefined);
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
    window.localStorage.setItem(getAccountStorageKey(USER_STORAGE_PROVIDER_KEY, getAccountOwnerId()), JSON.stringify({ ...defaultUserStorageProvider(), ...provider, type: "s3" }));
}

export function saveUserWebDAVStorageProvider(provider: UserWebDAVStorageProvider) {
    window.localStorage.setItem(getAccountStorageKey(USER_WEBDAV_STORAGE_PROVIDER_KEY, getAccountOwnerId()), JSON.stringify({ ...defaultUserWebDAVStorageProvider(), ...provider, type: "webdav" }));
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
    if ((response.headers.get("content-type") || "").includes("application/json")) {
        const payload = (await response.json().catch(() => null)) as { msg?: string } | null;
        throw new Error(payload?.msg || "代理图片下载失败");
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

function clearCachedImage(storageKey: string, ownerId: string, id: string) {
    const url = objectUrls.get(storageKey);
    if (url) URL.revokeObjectURL(url);
    objectUrls.delete(storageKey);
    serverUrls.delete(serverUrlCacheKey(ownerId, id));
}

async function deleteServerImage(storageKey: string, ownerId = getAccountOwnerId(), token = useUserStore.getState().token) {
    const id = storageKey.slice("server:".length);
    if (!id) return;
    const provider = loadUserStorageProvider(ownerId);
    clearCachedImage(storageKey, ownerId, id);

    if (storageKey.startsWith("server:webdav:")) {
        if (provider?.type !== "webdav") return;
        if (getAccountOwnerId() === ownerId && useUserStore.getState().token === token) {
            const direct = await import("@/services/webdav-direct-storage");
            if (await direct.deletePersistedDirectWebDAV(provider, storageKey)) {
                await store.removeItem(storageKey);
                return;
            }
        }
        return;
    } else if (provider?.type === "webdav" && getAccountOwnerId() === ownerId && useUserStore.getState().token === token) {
        const direct = await import("@/services/webdav-direct-storage");
        if (await direct.deletePersistedDirectWebDAV(provider, storageKey)) {
            await store.removeItem(storageKey);
            return;
        }
    }

    if (!token) {
        if (!provider) return;
        await deleteAnonymousStorageFile(id, toProviderPayload(provider));
        await store.removeItem(storageKey);
        return;
    }

    const response = await fetch(`/api/v1/files/${encodeURIComponent(id)}`, {
        method: "DELETE",
        headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` },
        body: JSON.stringify(provider ? { provider: toProviderPayload(provider) } : {}),
    });
    const payload = (await response.json().catch(() => null)) as { code?: number; msg?: string } | null;
    if (!response.ok || payload?.code !== 0) throw new Error(payload?.msg || "删除服务端图片失败");
    await store.removeItem(storageKey);
}

function blobToDataUrl(blob: Blob) {
    return new Promise<string>((resolve, reject) => {
        const reader = new FileReader();
        reader.onload = () => resolve(String(reader.result || ""));
        reader.onerror = () => reject(new Error("读取图片失败"));
        reader.readAsDataURL(blob);
    });
}
