import type { ComponentProps } from "react";
import { Zap } from "lucide-react";

import { normalizeVideoResolutionValue } from "@/components/video-settings-panel";

export function CreditSymbol({ className, ...props }: ComponentProps<"span">) {
    return (
        <span {...props} className={`inline-flex items-center justify-center ${className || ""}`}>
            <Zap className="size-[1em] fill-current" strokeWidth={2.4} />
        </span>
    );
}

export type ModelCreditCost = {
    model: string;
    resolution?: string;
    credits: number;
};

export function modelCreditCost(modelCosts: ModelCreditCost[] | undefined, model: string, resolution = "") {
    const items = modelCosts?.filter((item) => item.model === model) || [];
    const value = normalizeVideoResolutionValue(resolution).toLowerCase();
    const credits = items.find((item) => item.resolution && normalizeVideoResolutionValue(item.resolution).toLowerCase() === value)?.credits;
    if (credits !== undefined) return credits;
    return items.find((item) => !item.resolution)?.credits || 0;
}

export function requestCreditCost(options: { channelMode: string; modelCosts?: ModelCreditCost[]; model: string; resolution?: string; count?: string | number; seconds?: string | number }) {
    if (options.channelMode !== "remote") return 0;
    const seconds = Number(options.seconds);
    const count = options.seconds !== undefined && seconds === -1 ? 15 : Math.max(1, Math.floor(Math.abs(Number(options.seconds ?? options.count)) || 1));
    return Math.round(modelCreditCost(options.modelCosts, options.model, options.resolution) * count * 100) / 100;
}
