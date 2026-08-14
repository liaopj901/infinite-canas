---
title: 接口响应约定
description: 业务接口统一响应结构与前端处理约定
---

# 接口响应约定

后端业务接口统一返回 JSON：

```json
{
  "code": 0,
  "data": {},
  "msg": "ok"
}
```

- `code`: 业务状态码，`0` 表示成功，非 `0` 表示失败。
- `data`: 业务数据。失败时通常为 `null`。
- `msg`: 响应消息。成功默认为 `ok`，失败时放错误原因。

前端请求逻辑以 `code` 判断业务是否成功。当前后端业务失败也会返回 HTTP 200，前端不要只依赖 HTTP 状态码判断结果。

接口连接失败、服务不可达、返回体不是约定 JSON 时，前端按网络或接口异常处理。

## 生成图片存储接口

`generated_media` 只记录文件元数据和本地/云端位置，不保存图片二进制或 base64。登录用户调用保存接口后，后端先将文件写入 `GENERATED_MEDIA_DIR`；如果请求开启自动上传并且云端上传成功，则删除本地副本并返回云端存储信息。自动上传失败时仍返回成功，图片保持 `local` 状态，并在 `storageMessage` 中返回提示。

### `POST /api/v1/generated-images`

使用 `multipart/form-data` 保存生成图片：

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `file` | 是 | 图片文件 |
| `width` | 否 | 图片宽度 |
| `height` | 否 | 图片高度 |
| `autoUpload` | 否 | 是否立即上传云端，默认 `false` |
| `provider` | 否 | 用户对象存储配置 JSON；未提供时使用服务端可用的全局配置 |

成功返回的 `data` 只包含前端展示和后续上传所需字段：

```json
{
  "id": "generated-media-id",
  "url": "/api/v1/generated-images/generated-media-id/content",
  "storageKey": "local:generated-media-id",
  "storageStatus": "local",
  "storageMessage": "",
  "width": 1024,
  "height": 1024,
  "bytes": 123456,
  "mimeType": "image/png"
}
```

`storageStatus` 取值：

- `local`：文件在服务器本地，`url` 使用内容接口。
- `cloud`：文件已上传云端，优先使用云端 `url`；私有云端仍可通过内容接口读取。
- `cleaned`：本地文件已被定时清理，`url` 为空，`storageMessage` 为“图片已被清理”。

### `POST /api/v1/generated-images/:id/upload`

将 `local:<id>` 对应的服务器本地图片手动上传云端。成功后删除本地文件，返回与保存接口相同的图片存储数据；已是 `cloud` 状态时直接返回当前云端信息。

### `GET /api/v1/generated-images/:id/content`

返回图片二进制内容。请求需要登录并校验图片所有者；本地图片直接响应，云端图片通过对象存储读取。图片已被清理时返回 HTTP `410`，业务消息为“图片已被清理”。

### `DELETE /api/v1/generated-images/:id`

删除当前用户的生成图片记录及其本地文件；如果图片已上传云端，同时删除关联的云端对象。成功返回：

```json
{ "deleted": true }
```

## 图片生成历史接口

### `GET /api/v1/generation-logs/images`

返回当前用户的图片历史。接口只查询 `image_generation_logs` 的精简 `summary_json`，再批量补充 `generated_media` 的存储状态和展示地址，不返回完整 `payload_json`。图片记录中的 `images` 只保留前端展示和交互需要的字段：

- 图片标识：`id`、`storageKey`、`storageStatus`、`storageMessage`
- 展示地址：`dataUrl`
- 图片尺寸和大小：`width`、`height`、`bytes`、`mimeType`
- 历史卡片需要的标题、提示词、模型、时间、状态、错误和精简配置

服务器本地图片在响应中使用带权限的内容地址，并由前端转换成 Blob URL；已清理图片保留记录但 `dataUrl` 为空，前端应显示“图片已被清理”，不要将空地址当作普通加载失败。

### `POST /api/v1/generation-logs/images`

保存图片历史。请求仍接收 `{ "logs": [...] }`，后端只返回确认信息，不回传整份历史列表：

```json
{ "saved": true }
```

### `POST /api/v1/generation-logs/images/delete`

批量软删除图片历史，接收 `{ "ids": ["..."] }`，成功返回 `{ "deleted": true }`。
