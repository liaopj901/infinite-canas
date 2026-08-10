"use client";

import { ArrowRight, ArrowUpRight, Maximize2, Plus } from "lucide-react";
import { useEffect, useState } from "react";
import { App, Button, Image, Tag } from "antd";
import { useRouter } from "next/navigation";

import { fetchPrompts, type Prompt } from "@/services/api/prompts";
import { BRAND_NAME, NEW_API_DASHBOARD_URL, getCanvasProjectTitle } from "@/constant/brand";
import { cn } from "@/lib/utils";
import { useConfigStore } from "@/stores/use-config-store";
import { useUserStore } from "@/stores/use-user-store";
import { useCanvasStore } from "./canvas/stores/use-canvas-store";

export default function IndexPage() {
    const { message } = App.useApp();
    const router = useRouter();
    const createProject = useCanvasStore((state) => state.createProject);
    const hydrated = useCanvasStore((state) => state.hydrated);
    const requireLogin = useConfigStore((state) => state.publicSettings?.auth.requireLogin === true);
    const isPublicSettingsReady = useConfigStore((state) => state.isPublicSettingsReady);
    const user = useUserStore((state) => state.user);
    const isUserReady = useUserStore((state) => state.isReady);
    const [promptShowcase, setPromptShowcase] = useState<Prompt[]>([]);
    const [previewIndex, setPreviewIndex] = useState(0);
    const [previewOpen, setPreviewOpen] = useState(false);

    useEffect(() => {
        void fetchPrompts({ pageSize: 12 })
            .then((data) => setPromptShowcase(data.items))
            .catch((error) => message.error(error instanceof Error ? error.message : "获取提示词失败"));
    }, [message]);

    const openProtectedPage = (path: string) => {
        if (requireLogin && !user) {
            router.push(`/login?redirect=${encodeURIComponent(path)}`);
            return false;
        }
        router.push(path);
        return true;
    };

    const createAndEnter = () => {
        if (requireLogin && !user) {
            openProtectedPage("/canvas");
            return;
        }
        if (!hydrated) {
            message.info("画布数据正在加载，请稍后再试");
            return;
        }

        // 新入口只负责创建一个唯一项目标题，具体画布状态仍由画布 store 持有。
        const projectId = createProject(getCanvasProjectTitle(useCanvasStore.getState().projects.map(({ title }) => title)));
        router.push(`/canvas/${projectId}`);
    };

    return (
        <main className="h-full overflow-x-hidden overflow-y-auto bg-background text-foreground">
            <section className="relative mx-auto flex min-h-[calc(100svh-8rem)] w-full max-w-7xl items-center px-6 py-16 sm:px-10 lg:px-16">
                <div className="pointer-events-none absolute inset-x-6 top-10 border-t border-stone-200 dark:border-stone-800" />
                <div className="pointer-events-none absolute inset-x-6 bottom-10 border-t border-stone-200 dark:border-stone-800" />
                <div className="mx-auto w-full max-w-4xl text-center">
                    <div className="mb-8 flex items-center justify-center gap-3 text-xs font-semibold uppercase tracking-normal text-stone-500 dark:text-stone-400">
                        <span
                            className="size-6 bg-[#c45a3c] dark:bg-[#ee8a66]"
                            style={{
                                mask: "url(/logo.svg) center / contain no-repeat",
                                WebkitMask: "url(/logo.svg) center / contain no-repeat",
                            }}
                            aria-hidden="true"
                        />
                        <span>QQ LIAO / CANVAS</span>
                    </div>
                    <h1 className="text-5xl font-semibold tracking-normal text-stone-950 sm:text-7xl dark:text-stone-100">{BRAND_NAME}</h1>
                    <p className="mx-auto mt-7 max-w-2xl text-base leading-8 text-stone-500 sm:text-lg dark:text-stone-400">
                        在一个可以不断展开的工作台中，生成、连接和重组图片、文字与灵感，让创作从一次输出变成持续推演。
                    </p>
                    <div className="mt-10 flex flex-wrap items-center justify-center gap-3">
                        <Button type="primary" size="large" icon={<Plus className="size-4" />} disabled={!isPublicSettingsReady || !isUserReady || (!requireLogin && !hydrated)} onClick={createAndEnter}>
                            开始使用
                        </Button>
                        <Button size="large" icon={<Maximize2 className="size-4" />} disabled={!isPublicSettingsReady || !isUserReady} onClick={() => openProtectedPage("/canvas")}>
                            打开画布
                        </Button>
                        <Button size="large" icon={<ArrowUpRight className="size-4" />} href={NEW_API_DASHBOARD_URL}>
                            返回 NewAPI
                        </Button>
                    </div>
                    <div className="mt-16 flex flex-wrap items-center justify-center gap-x-8 gap-y-3 text-xs text-stone-400 dark:text-stone-500">
                        <span>图像</span>
                        <span>视频</span>
                        <span>音频</span>
                        <span>提示词</span>
                        <span>素材</span>
                    </div>
                </div>
            </section>

            <section className="mx-auto max-w-7xl border-t border-stone-200 px-6 pb-24 pt-16 sm:px-10 lg:px-16 dark:border-stone-800">
                <div className="mb-8 grid gap-4 md:grid-cols-[1fr_auto_1fr] md:items-start">
                    <div />
                    <div className="max-w-2xl text-center">
                        <div className="flex flex-wrap items-center justify-center gap-3">
                            <h2 className="text-3xl font-semibold text-stone-950 dark:text-stone-100">沉淀每一次好结果</h2>
                            <Button type="primary" size="middle" href="https://prompts.tdeh.top/" target="_blank">
                                提示词仓库
                            </Button>
                        </div>
                        <p className="mt-3 text-base leading-7 text-stone-500 dark:text-stone-400">收藏稳定出图的提示词、参考风格和结果图片，让下一次创作从已有经验开始。</p>
                    </div>
                    <Button type="link" href="/prompts" className="justify-self-center md:justify-self-end" icon={<ArrowRight className="size-4" />} iconPlacement="end">
                        提示词库
                    </Button>
                </div>
                <div className="grid auto-rows-[210px] gap-4 md:grid-cols-4">
                    {promptShowcase.map((item, index) => (
                        <button
                            key={item.id}
                            type="button"
                            onClick={() => {
                                setPreviewIndex(index);
                                setPreviewOpen(true);
                            }}
                            className={cn(
                                "group relative cursor-pointer overflow-hidden border border-stone-200 bg-stone-100 text-left dark:border-stone-800 dark:bg-stone-900",
                                index === 0 && "md:col-span-2 md:row-span-2",
                                index === 3 && "md:col-span-2",
                            )}
                        >
                            <img src={item.coverUrl} alt={item.title} className="h-full w-full object-cover transition duration-500 group-hover:scale-[1.03]" />
                            <div className="absolute inset-x-0 bottom-0 bg-gradient-to-t from-black/70 via-black/35 to-transparent p-4 text-white">
                                <div className="mb-2 flex flex-wrap gap-1.5">
                                    {item.tags.slice(0, 2).map((tag) => (
                                        <Tag key={tag} variant="filled" className="m-0 bg-white/15 text-[11px] text-white backdrop-blur">
                                            {tag}
                                        </Tag>
                                    ))}
                                </div>
                                <h3 className="text-sm font-medium">{item.title}</h3>
                                <p className="mt-1 line-clamp-2 text-xs leading-5 text-white/75">{item.prompt}</p>
                            </div>
                        </button>
                    ))}
                </div>
            </section>

            <Image.PreviewGroup
                items={promptShowcase.map((item) => ({
                    src: item.coverUrl,
                    alt: item.title,
                }))}
                preview={{
                    open: previewOpen,
                    current: previewIndex,
                    onOpenChange: setPreviewOpen,
                    onChange: setPreviewIndex,
                }}
            />
            <footer className="fixed inset-x-0 bottom-0 z-30 border-t border-stone-200 bg-background/95 px-4 py-2 text-center text-[11px] leading-4 text-stone-500 backdrop-blur dark:border-stone-800 dark:text-stone-400 sm:text-xs">
                qqliao画布基于 basketikun/infinite-canvas 二次开发 · 原作者 basketikun · AGPL-3.0
            </footer>
        </main>
    );
}
