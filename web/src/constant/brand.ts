export const BRAND_NAME = "qqliao画布";
export const NEW_API_DASHBOARD_URL = "https://new-api.qqliao.online/dashboard";

export function getCanvasProjectTitle(existingTitles: Iterable<string>) {
    const titles = new Set(existingTitles);
    let title = BRAND_NAME;

    // 只为新建项目避开重名，旧项目标题和本地存储结构保持不动。
    for (let index = 1; titles.has(title); index += 1) {
        title = `${BRAND_NAME} ${index}`;
    }

    return title;
}
