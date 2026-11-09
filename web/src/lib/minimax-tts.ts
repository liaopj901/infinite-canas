import { channelProtocolForConfig, type AiConfig } from "@/stores/use-config-store";

export const miniMaxTtsModels = new Set([
    "speech-2.8-hd",
    "speech-2.8-turbo",
    "speech-2.6-hd",
    "speech-2.6-turbo",
    "speech-02-hd",
    "speech-02-turbo",
    "speech-01-hd",
    "speech-01-turbo",
]);

export type MiniMaxTtsVoice = { voice_id: string; voice_name?: string };

export const miniMaxTtsFormatOptions = [
    { value: "mp3", label: "MP3" },
    { value: "wav", label: "WAV" },
    { value: "flac", label: "FLAC" },
];

export function isMiniMaxTtsConfig(config: AiConfig, modelName: string) {
    const model = modelName.trim().toLowerCase();
    const protocol = channelProtocolForConfig({ ...config, model: modelName, audioModel: modelName });
    if (protocol === "minimax") return miniMaxTtsModels.has(model);
    return protocol === "tokendance" && model.startsWith("minimax-") && miniMaxTtsModels.has(model.slice(8));
}

export function normalizeMiniMaxTtsVoice(value: string) {
    return value.trim() || "female-yujie";
}

export function normalizeMiniMaxTtsFormat(value: string) {
    return miniMaxTtsFormatOptions.some((item) => item.value === value) ? value : "mp3";
}

export function normalizeMiniMaxTtsSpeed(value: string) {
    const speed = Number(value);
    if (!Number.isFinite(speed)) return "1";
    return String(Math.max(0.5, Math.min(2, Number(speed.toFixed(2)))));
}
