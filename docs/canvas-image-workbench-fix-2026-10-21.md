# Canvas 生图工作台：生成成功后图片不显示的排查与修复说明

排查日期：2026-10-21。本文时间均为 UTC，北京时间需加 8 小时。

本文供本地源码修复、验证和重新构建使用。服务器已完成只读排查及隔离复现，尚未应用线上修复。本文不包含账号密码、API Key 或用户提示词。

## 1. 问题结论

**主要故障位于前端批量查询图片任务状态的函数 `batchCanvasImageTaskStatus`。该函数引用了未定义的 `token`，导致收到任务状态后抛出 `ReferenceError`，中断页面状态及生成结果的更新。**

实际链路如下：

```text
提交生图任务
  → 后端完成生成，保存完成状态及图片地址
  → 前端 POST /api/v1/canvas/image-tasks/status 查询状态
  → 前端遍历返回任务时读取未定义的 token
  → ReferenceError: token is not defined
  → 结果转换和页面更新被中断，工作台仍显示“生成中”
```

这也会影响失败任务的状态回显：异常发生在读取 `token` 时，尚未进入根据任务状态处理结果的辅助函数。

另外发现部分历史图片的存储记录缺失。这是独立问题，不能用它解释所有新任务不显示的现象，也不能通过修复轮询函数恢复这些缺失记录。

## 2. 运行环境与定位信息

| 项目 | 排查时的实际值 |
| --- | --- |
| 应用目录 | `/opt/infinite-canvas` |
| 运行容器 | `infinite-canvas` |
| Docker 镜像 | `infinite-canvas:server` |
| 版本文件 | `/app/VERSION` 为 `v0.8.1` |
| 版本备注 | CHANGELOG 含 Unreleased 及二开说明，不能仅用版本号推断源码提交 |
| 前端 | Next.js 16.2.9、React 19.2.5 |
| 服务端口 | 宿主机 `127.0.0.1:3001`，容器内前端 `3001`、后端 `8080` |
| 持久化目录 | 宿主机 `/opt/infinite-canvas/data` 挂载到 `/app/data` |
| 数据库 | PostgreSQL，数据库名 `infinite_canvas` |
| 任务状态接口 | `POST /api/v1/canvas/image-tasks/status` |

服务器目录主要包含部署配置、镜像包和数据，运行容器内是编译后的后端及前端产物。本次没有拿到本地完整源码，**不能确认原始 TypeScript 文件路径和源码行号**。下述函数名、接口路径及构建产物可用于本地搜索。

## 3. 已确认的证据

### 3.1 后端已完成任务，图片可以访问

2026-10-21 07:04 的任务记录：

```text
task_id:      client_image_task_7Df3_Y9EdftLyfrVUYqFC
source:       image-workbench
model:        grok-imagine-image-2.0
status:       completed
progress:     100
error:        空
created_at:   2026-10-21T07:04:15Z
completed_at: 2026-10-21T07:04:33.704702928Z
```

该任务保存的图片地址已实测返回 `HTTP 200`，响应类型为 `image/jpeg`，大小为 `268056` 字节。对应任务创建时的生成日志状态仍为“生成中”；排查期间该日志随后被标记删除。以上说明后端完成状态与前端日志回显没有正常同步，不应依赖这条已删除日志进行后续验收。

当天另一张图片也已经写入服务器本地：

```text
/opt/infinite-canvas/data/generated-images/2026/10/21/36266ff8-482b-48a3-bf89-7e3c8d0a5ef9.png
文件大小：2654289 字节
文件权限：0644
数据库 generated_media.storage_status：local
```

这是另外一条生成结果，仅用于说明有图片成功落盘，不应与上面的 Grok 任务混为同一张图片。

### 3.2 线上前端包含明确的未定义变量引用

容器内构建产物：

```text
/app/web/.next/static/chunks/1go5g5bxrj89a.js
```

通过本机前端服务获取的同名 JS 与容器内检查的文件一致；生图工作台的 HTML 引用了此文件。文件名是本次构建产物名，重新构建后可能变化。

该模块把压缩函数 `eF` 导出为 `batchCanvasImageTaskStatus`。函数中存在以下代码：

```javascript
async function eF(e, t) {
  // 前面完成参数处理、发起请求及解析返回 JSON。
  // 此处函数内没有定义 token。
  return Promise.all(
    n.data.map(async e => T(await eC(e, void 0, token)))
  );
}
```

上面的参数及局部名称来自压缩产物；注释为说明文字。报错位置是 `token` 的求值，`eC` 尚未执行。

同模块的单任务查询函数 `pollCanvasImageTaskStatus` 已经采用了正确模式：在请求前读取 `useUserStore.getState().token`，请求完成后将保存的值传给结果处理函数。批量查询应保持同样的会话处理方式。

### 3.3 已完成隔离复现

从运行容器中提取原始批量查询函数，在 Node.js VM 中执行；模拟成功的接口返回，并替换网络及结果处理依赖。

| 返回任务状态 | 原始函数 | 请求前补充 token 定义后 |
| --- | --- | --- |
| `completed` | `ReferenceError: token is not defined` | 正常返回完成任务 |
| `failed` | 同上 | 正常返回失败任务 |
| `running` | 同上 | 正常返回运行中任务 |

原始函数的结果处理辅助函数调用次数为 0；补充定义后调用次数为 1，且收到请求前捕获的会话值。

**验证范围：已确认运行时错误及最小修改的效果。尚未修改生产文件，也未完成修复后真实浏览器生图、云同步或账号切换的端到端验收。**

## 4. 本地源码定位与修复要求

### 4.1 查找真正的源码

在本地仓库根目录执行：

```bash
rg -n 'batchCanvasImageTaskStatus|/canvas/image-tasks/status' \
  -g '!node_modules/**' -g '!.next/**' -g '!dist/**' .

rg -n 'pollCanvasImageTaskStatus|useUserStore.getState\(\).token' \
  -g '!node_modules/**' -g '!.next/**' -g '!dist/**' .
```

优先修复导出 `batchCanvasImageTaskStatus` 的源码文件。核对该函数最终调用结果处理辅助函数时使用的 `token` 是否有定义；辅助函数在本地可能有可读名称，不一定叫 `eC` 或 `T`。

如果本地源码已经定义了 `token`，应继续核对部署镜像来源、源码提交和二开补丁，确认为什么线上构建产物仍缺少定义。此时不要直接重复加入同名变量。

### 4.2 最小修复

在现有批量查询函数中，**发起网络请求前读取并保存本次会话的 token**，在结果处理中使用这个保存值。

以下只展示需要插入的一行；函数签名及其余代码应保留本地源码的实际写法：

```diff
 export async function batchCanvasImageTaskStatus(/* 现有参数 */) {
+  const token = useUserStore.getState().token;
   // 现有参数处理、请求、返回值校验和结果处理代码。
 }
```

实施时确认：

1. 复用项目已有的 `useUserStore` 导入及调用方式，避免重复导入或命名冲突。
2. 在请求前捕获 token，结果处理继续接收该值。现有辅助函数会将它与当前登录状态比较，用于避免旧账号的异步结果触发当前账号的图片同步；调用方也有账号及会话校验，应保留。
3. 保留现有的任务 ID 去重、接口错误校验、图片地址解析和自动同步逻辑。
4. 不要通过定义全局 `window.token`、屏蔽异常或强制把任务标记成功来绕过问题。
5. 在源码中修改并重新构建。直接修改 `.next/static/chunks` 只能形成临时产物补丁，下次构建就会丢失，不适合作为本地正式修复。

如果本地采用其他状态管理名称，以源码为准；关键是保持“请求前捕获会话，处理返回结果时继续使用该会话”的语义。

### 4.3 构建与发布核对

先查看本地前端 `package.json` 的实际脚本，再运行项目规定的类型检查和构建。排查到的运行包使用 Bun，构建脚本为：

```text
build: bun run build:bridge && next build
build:bridge: bun ../canvas-agent/scripts/build-comfy-bridge.ts
```

如果本地结构一致，在前端包目录执行 `bun run build`。该构建依赖相邻的 `canvas-agent` 目录，不能只复制容器里的前端运行文件来重新构建。

构建完成后按本地仓库原有流程制作和发布镜像。服务器使用 ARM64 镜像，部署配置位于 `/opt/infinite-canvas/docker-compose.server.yml`，Compose 服务名为 `app`。发布时应核对新镜像确实替换了旧前端产物；只重启旧容器不会应用本地源码修改。

保留 `/opt/infinite-canvas/data` 的挂载及数据库。此问题的最小修复不需要变更数据库结构或清理用户数据。若页面仍加载旧 JS，核对构建版本和静态资源缓存，再重新验收。

## 5. 验收步骤与通过标准

### 5.1 核心验收

在浏览器开发者工具中同时打开 Console 和 Network，使用已有可用渠道提交一个普通、可成功生成的任务。

1. 观察 `POST /api/v1/canvas/image-tasks/status`，返回 JSON 中 `code` 为 `0`，`data` 是任务数组。
2. 当后端返回 `status: completed`、`progress: 100` 及有效图片地址时，工作台在下一次正常轮询后更新为成功并显示图片，无需刷新页面。
3. Console 不再出现 `ReferenceError: token is not defined`。
4. 页面中的生成日志同步为成功，刷新后仍可正常查看新生成结果。

任务 HTTP 请求成功与业务成功需分开判断：仅看到接口 `HTTP 200`，不能证明任务本身成功，应检查 JSON 中的任务状态及错误字段。

### 5.2 必要回归

| 场景 | 通过标准 |
| --- | --- |
| 任务仍在运行 | 继续正常轮询，无未定义变量异常 |
| 接口返回失败任务 | 页面更新为失败并显示实际错误，不无限停留在生成中；可用模拟返回验证，避免为失败场景额外消耗生图额度 |
| 多任务批量查询 | 一次返回多个任务时，各任务都能正常处理 |
| 切换账号或退出登录 | 请求返回后，不把旧账号的生成结果写入新账号；保留既有的账号与会话校验 |
| 直接 URL 与本地持久化图片 | 在现有渠道支持的范围内，图片均可解析、显示及刷新后恢复 |

最小修改先完成项目现有必要检查和上述针对性验收，无需为此次修复引入新的测试框架。

## 6. 独立问题：历史图片存储记录缺失

服务器日志多次出现：

```text
/app/repository/storage.go:23  record not found
SELECT * FROM "storage_objects" WHERE id = '...'
GET /api/files/<id>
```

排查时涉及的存储 ID 包括：

```text
8af641bd-85fe-404b-9d6a-b076fbe55236
b56e4da4-ea63-4cb8-b1a1-de3bbb332227
7062c082-2d55-4e25-b40c-9b1805145a60
```

目前只确认读取这些 ID 时数据库查询未找到记录，尚未确认删除原因，也未确认对应云端文件是否仍然存在。可能的清理、迁移或引用失效需要进一步取证，不能直接写成已确认根因。

若需要修复这些历史图片，应单独核对：

- 前端历史记录、素材或项目是否仍引用这些 ID。
- 数据库备份中能否找到原始 `storage_objects` 记录。
- 对应存储提供方中的文件是否存在，且能匹配原有归属和对象路径。
- 文件已缺失时，界面能否提示资源不可用并允许重新上传。

不要伪造空存储记录来消除报错。修复前端 token 后，这些已失效的历史图片仍可能需要恢复或重新上传。

## 7. 可直接交给本地开发工具的任务说明

> 请先阅读本文，在本地仓库中搜索 `batchCanvasImageTaskStatus` 及 `/canvas/image-tasks/status`，核对批量查询图片任务状态的代码是否在结果处理中引用了未定义的 `token`。线上已从运行产物复现 `ReferenceError: token is not defined`：后端任务完成且图片有效，但前端结果处理被中断。请参考同模块 `pollCanvasImageTaskStatus` 的现有做法，在网络请求前捕获当前登录 token，并将该值用于结果处理，保留现有账号隔离、状态校验和图片同步逻辑。修改源码后运行仓库已有检查及构建，按本文完成任务成功、失败、运行中和账号切换的验证，报告改动文件与实际验证结果。不要直接修改构建产物作为正式修复，不要把历史图片存储记录缺失混入这次最小修复。若本地源码已无此错误，请核对线上部署版本与构建来源。
