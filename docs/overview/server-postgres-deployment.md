---
title: canvas.qqliao.online 完整部署手册
description: 从最新源码构建无限画布，连接服务器本机 PostgreSQL，并自动配置 Nginx、HTTPS 和提示词
---

# canvas.qqliao.online 完整部署手册

这是一份可直接执行的生产部署手册。部署脚本会完成：

- 拉取 `origin/main` 最新代码并在服务器构建 Docker 镜像。
- 使用服务器本机已经安装的 PostgreSQL。
- 创建专用数据库、账号和最小必要权限。
- 检测 `3001` 端口；被占用时自动选择 `3002-3099` 中第一个空闲端口。
- 应用端口只监听 `127.0.0.1`，由 Nginx 代理 `canvas.qqliao.online`。
- 配置 HTTPS、健康检查和主机防火墙。
- 使用管理员账号调用后端接口，自动同步并逐分类校验远程提示词。

## 固定部署参数

| 项目 | 配置 |
| --- | --- |
| 域名 | `canvas.qqliao.online` |
| 管理员用户名 | `admin` |
| 管理员初始密码 | `liao901.` |
| PostgreSQL 数据库 | `infinite_canvas` |
| PostgreSQL 用户 | `infinite_canvas` |
| 项目目录 | `/opt/infinite-canvas` |
| Docker 网络 | `infinite-canvas-network` |
| 首选应用端口 | `3001` |
| 公网端口 | `80`、`443` |

`liao901.` 只应作为首次登录密码。它已经出现在部署文档中，不是安全的长期密码，部署成功后必须立即修改。

## 部署前确认

服务器需要满足：

- 使用 `systemd`。
- 操作系统为 Debian/Ubuntu，或 RHEL/Rocky/AlmaLinux。
- PostgreSQL 已安装并正常运行，存在 `postgres` 系统用户。
- 当前账号具有 `root` 或 `sudo` 权限。
- `canvas.qqliao.online` 已解析到服务器公网地址。

该域名使用 Cloudflare 时，需要确认：

- DNS 中 `canvas` 记录的目标是服务器真实公网 IP，不要手工填写 Cloudflare 的代理 IP。
- 源站证书签发完成后，将 SSL/TLS 加密模式设为 `完全（严格）`。
- 如果 Certbot 的 HTTP-01 校验失败，可临时把代理状态切为“仅 DNS”，证书签发成功后再恢复代理。

部署前需要在云服务器安全组中放行：

```text
TCP 80
TCP 443
```

不要向公网放行：

```text
应用端口 3001-3099
PostgreSQL 5432
```

即使脚本因端口占用改用了 `3001` 或其他端口，也不需要向公网放行该端口。它只绑定 `127.0.0.1`，Nginx 通过服务器内部回环地址访问。部署脚本仍会报告最终端口，并暂停等待用户确认安全组的 `80/443` 已经放行。

## 首次部署

`deploy/server-postgres.sh` 和 `docker-compose.server.yml` 是本次在
`D:\IdeaWork\infinite-canvas` 中生成的部署文件，不属于上游仓库。不能只克隆
GitHub 源码后直接运行脚本，必须把这两个文件一起上传到服务器。

通过 SSH 登录服务器并切换到 root：

```bash
sudo -i
```

若服务器还没有 Git，先安装：

```bash
if ! command -v git >/dev/null 2>&1; then
  if command -v apt-get >/dev/null 2>&1; then
    apt-get update && apt-get install -y git
  else
    dnf install -y git
  fi
fi
```

确认 PostgreSQL 可用：

```bash
systemctl status postgresql --no-pager || true
runuser -u postgres -- psql -Atqc "SELECT version();"
```

拉取最新源码：

```bash
git clone https://github.com/tigerowo/infinite-canvas.git /opt/infinite-canvas
cd /opt/infinite-canvas
git fetch origin main
git merge --ff-only origin/main
```

如果目录已经存在，不要重新克隆：

```bash
cd /opt/infinite-canvas
git status --short
git pull --ff-only origin main
```

然后在本地 Windows PowerShell 中上传部署文件，将 `服务器IP` 替换为真实地址：

```powershell
ssh root@服务器IP "mkdir -p /opt/infinite-canvas/deploy"
scp "D:\IdeaWork\infinite-canvas\docker-compose.server.yml" root@服务器IP:/opt/infinite-canvas/
scp "D:\IdeaWork\infinite-canvas\deploy\server-postgres.sh" root@服务器IP:/opt/infinite-canvas/deploy/
```

回到服务器，先检查文件确实存在：

```bash
cd /opt/infinite-canvas
test -f docker-compose.server.yml
test -f deploy/server-postgres.sh
bash -n deploy/server-postgres.sh
```

运行部署脚本：

```bash
cd /opt/infinite-canvas
bash deploy/server-postgres.sh
```

如果需要给 Let's Encrypt 配置证书到期通知邮箱：

```bash
cd /opt/infinite-canvas
LETSENCRYPT_EMAIL=你的邮箱 bash deploy/server-postgres.sh
```

脚本执行到公网确认步骤时会显示实际应用端口。此时部署执行者必须通知用户：

```text
应用端口已经确定为 127.0.0.1:实际端口。
请确认云服务器安全组已放行 TCP 80 和 TCP 443。
不要放行应用实际端口，也不要放行 PostgreSQL 5432。
```

用户确认域名解析和安全组后，在终端输入：

```text
yes
```

脚本随后会申请 HTTPS 证书、检查公网健康端点、登录管理员账号、同步全部提示词，并检查每个远程提示词分类的数量。

## 脚本实际执行内容

### 1. 依赖和最新源码

脚本会安装以下必要工具：

- Docker Engine 和 Docker Compose 插件
- Nginx
- Certbot 及 Nginx 插件
- Git、curl、jq、OpenSSL、PostgreSQL 客户端

如果 Git 工作区存在未提交修改，脚本会停止，不会覆盖服务器上的改动。源码只能通过 fast-forward 更新到 `origin/main`。

### 2. 端口选择

脚本优先使用 `3001`。检测命令基于服务器实际监听端口，若已占用则依次尝试：

```text
3002
...
3099
```

最终端口写入 `/opt/infinite-canvas/.env`：

```dotenv
APP_PORT=实际端口
```

服务器专用 Compose 配置只监听回环地址：

```yaml
ports:
  - "127.0.0.1:${APP_PORT}:3001"
```

因此应用不会绕过 Nginx 暴露到公网。

### 3. PostgreSQL

脚本在本机 PostgreSQL 中创建：

```text
数据库：infinite_canvas
用户：infinite_canvas
```

数据库密码和 JWT 密钥由 OpenSSL 随机生成，只保存在权限为 `600` 的：

```text
/opt/infinite-canvas/.env
```

应用配置为：

```dotenv
STORAGE_DRIVER=postgres
DATABASE_DSN=postgres://infinite_canvas:随机数据库密码@host.docker.internal:5432/infinite_canvas?sslmode=disable
```

脚本不会授予应用账号 `SUPERUSER`、`CREATEDB` 或 `CREATEROLE`。应用启动时由 GORM 自动创建和迁移业务表。

脚本还会：

- 创建固定 Docker 网络 `infinite-canvas-network`。
- 将该网络的实际 CIDR 写入 `pg_hba.conf`。
- PostgreSQL 只监听 `localhost` 和该 Docker 网络的宿主机网关，不监听所有公网网卡。
- 使用 `scram-sha-256` 认证。
- 在修改前备份 `pg_hba.conf`。
- 检查 `pg_hba_file_rules` 是否存在语法错误。
- 重启 PostgreSQL 并验证本地连接。

如果首次运行时发现 `infinite_canvas` 数据库已经包含业务表，脚本会在修改数据库账号密码和写入 `.env` 之前停止，不会删除数据库、覆盖用户或重置既有管理员。

### 4. 持久化目录

以下目录会挂载到容器内 `/app/data`：

```text
/opt/infinite-canvas/data
```

即使业务数据库使用 PostgreSQL，该目录也不能删除。它保存：

- 上传的参考图片、视频和音频
- AI 调用日志
- 其他非数据库持久化文件

提示词记录保存在 PostgreSQL 中，不依赖 `data/prompts` 目录。

### 5. Nginx

Nginx 配置文件为：

```text
/etc/nginx/conf.d/infinite-canvas.conf
```

代理目标会自动使用脚本选中的端口：

```nginx
proxy_pass http://127.0.0.1:实际端口;
```

域名固定为：

```nginx
server_name canvas.qqliao.online;
```

项目支持上传参考素材，Nginx 请求体上限设置为：

```nginx
client_max_body_size 80m;
```

如果其他 Nginx 配置已经使用 `canvas.qqliao.online`，脚本会停止并列出冲突文件，不会直接覆盖未知站点配置。

### 6. HTTPS

脚本使用 Certbot 为以下域名申请或更新证书：

```text
canvas.qqliao.online
```

申请证书前必须满足：

- 域名已经解析到当前服务器。
- 云安全组允许公网访问 TCP `80` 和 `443`。
- Nginx 的 HTTP 健康检查已经成功。

### 7. 自动拉取提示词

应用和 HTTPS 健康检查通过后，脚本会自动执行：

1. 使用 `admin` / `liao901.` 调用 `/api/admin/login`。
2. 从响应的 `data.token` 获取管理员令牌。
3. 调用 `/api/admin/prompt-categories/sync-all`。
4. 查询每个远程分类的提示词数量。
5. 检查同步期间是否出现 `scheduled prompt sync failed` 日志。

任何远程分类数量为 `0` 或日志存在同步失败时，脚本都会返回失败并打印应用日志。

## 验收

脚本最后必须输出：

```text
部署完成。
访问地址：https://canvas.qqliao.online
管理后台：https://canvas.qqliao.online/admin
提示词管理：https://canvas.qqliao.online/admin/prompts
管理员用户名：admin
管理员初始密码：liao901.
应用本机端口：127.0.0.1:实际端口
源码提交：实际 commit
```

再执行以下检查：

```bash
cd /opt/infinite-canvas
docker compose -f docker-compose.server.yml ps
curl -fsS https://canvas.qqliao.online/api/health
docker compose -f docker-compose.server.yml logs --tail=100 app
```

健康端点必须返回：

```text
ok
```

检查数据库表：

```bash
runuser -u postgres -- psql -d infinite_canvas -c "\dt"
```

检查提示词总数：

```bash
APP_PORT="$(sed -n 's/^APP_PORT=//p' /opt/infinite-canvas/.env)"
LOGIN_JSON="$(curl -fsS \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"liao901."}' \
  "http://127.0.0.1:${APP_PORT}/api/admin/login")"
TOKEN="$(jq -r '.data.token' <<<"${LOGIN_JSON}")"
curl -fsS \
  -H "Authorization: Bearer ${TOKEN}" \
  "http://127.0.0.1:${APP_PORT}/api/admin/prompts?page=1&pageSize=1" |
  jq '.data.total'
```

返回值必须大于 `0`。

## 部署后必须处理

1. 登录 `https://canvas.qqliao.online/admin`。
2. 立即将管理员密码 `liao901.` 修改为新的强密码。
3. 确认云安全组只开放业务需要的端口，至少不要开放应用实际端口和 `5432`。
4. 将 `/opt/infinite-canvas/.env`、PostgreSQL 和 `/opt/infinite-canvas/data` 纳入备份。

管理员只在数据库中不存在管理员时按 `.env` 创建。首次部署后修改 `.env` 中的 `ADMIN_PASSWORD`，不会自动修改数据库里的管理员密码。

## 更新部署

先备份，再更新：

```bash
cd /opt/infinite-canvas
git status --short
git pull --ff-only origin main
docker compose -f docker-compose.server.yml build --pull app
docker compose -f docker-compose.server.yml up -d --remove-orphans
curl -fsS https://canvas.qqliao.online/api/health
```

更新不会修改现有 `.env`、PostgreSQL 数据或 `data` 目录。

需要重新同步提示词时，可以重新运行完整部署脚本；它会复用已有密钥、数据库密码和应用端口：

```bash
cd /opt/infinite-canvas
bash deploy/server-postgres.sh
```

首次部署后如果已经修改管理员密码，重新运行脚本前需要安全输入当前密码，否则自动提示词同步无法登录：

```bash
cd /opt/infinite-canvas
read -r -s -p "当前管理员密码：" CURRENT_ADMIN_PASSWORD
printf '\n'
PROMPT_SYNC_PASSWORD="${CURRENT_ADMIN_PASSWORD}" bash deploy/server-postgres.sh
unset CURRENT_ADMIN_PASSWORD
```

`PROMPT_SYNC_PASSWORD` 只用于本次同步登录，不会写入 `.env`。

## 备份

```bash
install -d -m 700 /var/backups/infinite-canvas

runuser -u postgres -- pg_dump -Fc infinite_canvas \
  > "/var/backups/infinite-canvas/postgres-$(date +%F-%H%M%S).dump"

tar -C /opt/infinite-canvas -czf \
  "/var/backups/infinite-canvas/data-$(date +%F-%H%M%S).tar.gz" \
  data .env
```

备份必须复制到其他服务器或对象存储。只保存在当前服务器同一块磁盘上，不算有效备份。

## 常见故障

### 3001 端口被占用

无需手工处理。脚本会选择 `3002-3099` 中的空闲端口并更新 `.env` 和 Nginx。

查看最终端口：

```bash
sed -n 's/^APP_PORT=//p' /opt/infinite-canvas/.env
```

不要向公网放行该端口，只需放行 `80/443`。

### Nginx 域名冲突

查找冲突：

```bash
grep -RInF "canvas.qqliao.online" /etc/nginx --include='*.conf'
```

保留唯一一个 `server_name canvas.qqliao.online` 配置后，重新运行部署脚本。

### connection refused

```bash
cd /opt/infinite-canvas
docker compose -f docker-compose.server.yml logs --tail=200 app
runuser -u postgres -- psql -Atqc "SHOW listen_addresses"
docker network inspect infinite-canvas-network
```

应用容器中的 `127.0.0.1` 指向容器自己，连接宿主机 PostgreSQL 必须使用 `host.docker.internal`。

### no pg_hba.conf entry

```bash
HBA_FILE="$(runuser -u postgres -- psql -Atqc 'SHOW hba_file')"
grep -n "infinite-canvas" -A2 -B1 "${HBA_FILE}"
docker network inspect infinite-canvas-network \
  --format '{{(index .IPAM.Config 0).Subnet}}'
```

`pg_hba.conf` 中的 CIDR 必须与 Docker 网络实际子网一致。

### 提示词同步失败

```bash
cd /opt/infinite-canvas
docker compose -f docker-compose.server.yml logs --tail=300 app |
  grep -E "prompt sync|scheduled prompt sync|拉取失败"
```

确认服务器能够访问：

```bash
curl -I https://github.com
curl -I https://raw.githubusercontent.com
```

网络恢复后重新运行部署脚本完成同步和校验。

### HTTPS 申请失败

```bash
getent ahostsv4 canvas.qqliao.online
nginx -t
curl -H "Host: canvas.qqliao.online" http://127.0.0.1/api/health
certbot certificates
```

重点检查域名解析、云安全组 `80/443`、Nginx 冲突和证书申请频率限制。
