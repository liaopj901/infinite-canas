import { useUserStore } from "@/stores/use-user-store";

export const GUEST_ACCOUNT_OWNER = "guest";

export function getAccountOwnerId() {
    return useUserStore.getState().user?.id || GUEST_ACCOUNT_OWNER;
}

export function getAccountStorageKey(baseKey: string, ownerId = getAccountOwnerId()) {
    return `${baseKey}:${ownerId}`;
}

export function getAccountRecordStorageKey(baseKey: string, recordId: string, ownerId = getAccountOwnerId()) {
    return `${getAccountStorageKey(baseKey, ownerId)}:${recordId}`;
}

export function isAccountRecordStorageKey(key: string, baseKey: string, ownerId = getAccountOwnerId()) {
    return key.startsWith(`${getAccountStorageKey(baseKey, ownerId)}:`);
}
