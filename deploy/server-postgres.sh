#!/usr/bin/env bash
set -Eeuo pipefail

DOMAIN="canvas.qqliao.online"
ADMIN_USERNAME="admin"
ADMIN_PASSWORD="liao901."
PROMPT_SYNC_PASSWORD="${PROMPT_SYNC_PASSWORD:-${ADMIN_PASSWORD}}"
DB_NAME="infinite_canvas"
DB_USER="infinite_canvas"
NETWORK_NAME="infinite-canvas-network"
CONTAINER_NAME="infinite-canvas"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
COMPOSE_FILE="${REPO_ROOT}/docker-compose.server.yml"
ENV_FILE="${REPO_ROOT}/.env"
NGINX_CONF="/etc/nginx/conf.d/infinite-canvas.conf"
COMPOSE=(docker compose -f "${COMPOSE_FILE}")
EXISTING_ENV=0
PORT_CHANGED=0

log() {
  printf '\n[%s] %s\n' "$(date '+%H:%M:%S')" "$*"
}

fail() {
  printf '\n部署失败：%s\n' "$*" >&2
  exit 1
}

on_error() {
  local exit_code=$?
  printf '\n部署在第 %s 行失败，退出码 %s。\n' "${BASH_LINENO[0]:-unknown}" "${exit_code}" >&2
  if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
    "${COMPOSE[@]}" logs --tail=100 app 2>/dev/null || true
  fi
  exit "${exit_code}"
}

trap on_error ERR

require_root() {
  [ "${EUID}" -eq 0 ] || fail "请使用 sudo bash deploy/server-postgres.sh 运行。"
  [ -f "${REPO_ROOT}/Dockerfile" ] || fail "未找到 Dockerfile，请从项目根目录中的 deploy 脚本运行。"
  [ -f "${COMPOSE_FILE}" ] || fail "未找到 docker-compose.server.yml。"
}

install_host_packages() {
  log "检查服务器依赖"
  if command -v apt-get >/dev/null 2>&1; then
    apt-get update
    DEBIAN_FRONTEND=noninteractive apt-get install -y \
      ca-certificates certbot curl git iproute2 jq nginx openssl \
      postgresql-client python3-certbot-nginx
    return
  fi
  if command -v dnf >/dev/null 2>&1; then
    dnf install -y ca-certificates curl git iproute jq nginx openssl postgresql
    dnf install -y certbot python3-certbot-nginx || {
      dnf install -y epel-release
      dnf install -y certbot python3-certbot-nginx
    }
    return
  fi
  fail "仅自动支持 Debian/Ubuntu 和 RHEL/Rocky/AlmaLinux。请先安装 curl、git、jq、nginx、openssl、psql、certbot 和 Nginx 插件。"
}

install_docker() {
  if ! command -v docker >/dev/null 2>&1; then
    log "安装 Docker Engine"
    curl -fsSL https://get.docker.com -o /tmp/get-docker.sh
    sh /tmp/get-docker.sh
    rm -f /tmp/get-docker.sh
  fi
  systemctl enable --now docker
  docker compose version >/dev/null 2>&1 || fail "Docker Compose 插件不可用。"
}

update_source() {
  [ -d "${REPO_ROOT}/.git" ] || fail "当前目录不是 Git 仓库。"
  if ! git -C "${REPO_ROOT}" diff --quiet || ! git -C "${REPO_ROOT}" diff --cached --quiet; then
    fail "服务器源码存在未提交修改，为避免覆盖已停止。请先处理 git status。"
  fi
  log "拉取 origin/main 最新源码"
  git -C "${REPO_ROOT}" fetch origin main
  local current_commit remote_commit
  current_commit="$(git -C "${REPO_ROOT}" rev-parse HEAD)"
  remote_commit="$(git -C "${REPO_ROOT}" rev-parse origin/main)"
  git -C "${REPO_ROOT}" merge-base --is-ancestor "${current_commit}" "${remote_commit}" ||
    fail "服务器分支不能快进到 origin/main，请人工处理 Git 分支。"
  git -C "${REPO_ROOT}" merge --ff-only "${remote_commit}"
}

env_value() {
  local key="$1"
  sed -n "s/^${key}=//p" "${ENV_FILE}" 2>/dev/null | tail -n 1
}

port_is_used() {
  local port="$1"
  ss -H -ltn | awk '{print $4}' | grep -Eq ":${port}$"
}

port_belongs_to_app() {
  local port="$1"
  docker inspect "${CONTAINER_NAME}" \
    --format '{{(index (index .NetworkSettings.Ports "3001/tcp") 0).HostPort}}' \
    2>/dev/null | grep -qx "${port}"
}

find_app_port() {
  local requested_port
  requested_port="$(env_value APP_PORT)"
  if [[ "${requested_port}" =~ ^[0-9]+$ ]]; then
    if ! port_is_used "${requested_port}" || port_belongs_to_app "${requested_port}"; then
      APP_PORT="${requested_port}"
      return
    fi
    PORT_CHANGED=1
  fi

  APP_PORT=3001
  while port_is_used "${APP_PORT}"; do
    APP_PORT=$((APP_PORT + 1))
    [ "${APP_PORT}" -le 3099 ] || fail "3001-3099 端口均被占用。"
  done
  [ "${APP_PORT}" -eq 3001 ] || PORT_CHANGED=1
}

load_or_create_secrets() {
  if [ -f "${ENV_FILE}" ]; then
    EXISTING_ENV=1
    JWT_SECRET="$(env_value JWT_SECRET)"
    DB_PASSWORD="$(sed -nE 's#^DATABASE_DSN=postgres://infinite_canvas:([^@]+)@.*#\1#p' "${ENV_FILE}" | tail -n 1)"
  else
    JWT_SECRET=""
    DB_PASSWORD=""
  fi
  [ -n "${JWT_SECRET}" ] || JWT_SECRET="$(openssl rand -hex 32)"
  [ -n "${DB_PASSWORD}" ] || DB_PASSWORD="$(openssl rand -hex 24)"
}

write_environment() {
  log "写入生产环境配置"
  cat >"${ENV_FILE}" <<EOF
APP_PORT=${APP_PORT}
ADMIN_USERNAME=${ADMIN_USERNAME}
ADMIN_PASSWORD=${ADMIN_PASSWORD}
JWT_SECRET=${JWT_SECRET}
JWT_EXPIRE_HOURS=168
STORAGE_DRIVER=postgres
DATABASE_DSN=postgres://${DB_USER}:${DB_PASSWORD}@host.docker.internal:5432/${DB_NAME}?sslmode=disable
PUBLIC_BASE_URL=https://${DOMAIN}
AI_LOG_DIR=/app/data/logs/ai-calls
EOF
  chmod 600 "${ENV_FILE}"
  install -d -m 755 "${REPO_ROOT}/data"
}

ensure_network() {
  if ! docker network inspect "${NETWORK_NAME}" >/dev/null 2>&1; then
    docker network create "${NETWORK_NAME}" >/dev/null
  fi
  DOCKER_SUBNET="$(docker network inspect "${NETWORK_NAME}" --format '{{(index .IPAM.Config 0).Subnet}}')"
  DOCKER_GATEWAY="$(docker network inspect "${NETWORK_NAME}" --format '{{(index .IPAM.Config 0).Gateway}}')"
  [ -n "${DOCKER_SUBNET}" ] || fail "无法读取 Docker 网络子网。"
  [ -n "${DOCKER_GATEWAY}" ] || fail "无法读取 Docker 网络网关。"
}

find_psql() {
  id postgres >/dev/null 2>&1 || fail "未找到 postgres 系统用户。"
  PSQL_BIN="$(runuser -u postgres -- sh -lc 'command -v psql' | tail -n 1)"
  [ -x "${PSQL_BIN}" ] || fail "未找到 PostgreSQL psql 命令。"
}

psql_admin() {
  runuser -u postgres -- "${PSQL_BIN}" -v ON_ERROR_STOP=1 "$@"
}

restart_postgres() {
  if systemctl restart postgresql.service >/dev/null 2>&1; then
    return
  fi
  local unit
  unit="$(systemctl list-unit-files --type=service --no-legend |
    awk '{print $1}' |
    grep -E '^postgresql(-[0-9]+)?\.service$' |
    head -n 1 || true)"
  [ -n "${unit}" ] || fail "无法识别 PostgreSQL systemd 服务，请人工重启 PostgreSQL 后重新运行脚本。"
  systemctl restart "${unit}"
}

configure_postgres() {
  log "配置本机 PostgreSQL"
  find_psql

  local database_exists table_count
  database_exists="$(psql_admin -Atqc "SELECT 1 FROM pg_database WHERE datname='${DB_NAME}'")"
  if [ "${database_exists}" = "1" ] && [ "${EXISTING_ENV}" -eq 0 ]; then
    table_count="$(psql_admin -d "${DB_NAME}" -Atqc "SELECT count(*) FROM pg_tables WHERE schemaname='public'")"
    [ "${table_count}" = "0" ] ||
      fail "数据库 ${DB_NAME} 已包含业务表。脚本不会修改数据库账号、管理员或现有数据，请先备份并确认部署策略。"
  fi

  if [ "$(psql_admin -Atqc "SELECT 1 FROM pg_roles WHERE rolname='${DB_USER}'")" = "1" ]; then
    psql_admin -c "SET password_encryption='scram-sha-256'; ALTER ROLE ${DB_USER} WITH LOGIN PASSWORD '${DB_PASSWORD}' NOSUPERUSER NOCREATEDB NOCREATEROLE;"
  else
    psql_admin -c "SET password_encryption='scram-sha-256'; CREATE ROLE ${DB_USER} LOGIN PASSWORD '${DB_PASSWORD}' NOSUPERUSER NOCREATEDB NOCREATEROLE;"
  fi

  if [ "${database_exists}" != "1" ]; then
    psql_admin -c "CREATE DATABASE ${DB_NAME} OWNER ${DB_USER};"
  fi

  psql_admin -c "ALTER DATABASE ${DB_NAME} OWNER TO ${DB_USER};"
  psql_admin -d "${DB_NAME}" -c "GRANT USAGE, CREATE ON SCHEMA public TO ${DB_USER};"
  # 容器只需要访问专用网桥；禁止为了省事让 PostgreSQL 监听所有公网网卡。
  psql_admin -c "ALTER SYSTEM SET listen_addresses='localhost,${DOCKER_GATEWAY}';"

  local hba_file hba_errors
  hba_file="$(psql_admin -Atqc "SHOW hba_file" | xargs)"
  [ -f "${hba_file}" ] || fail "找不到 pg_hba.conf：${hba_file}"
  cp -n "${hba_file}" "${hba_file}.before-infinite-canvas" 2>/dev/null || true
  sed -i '/^# BEGIN infinite-canvas$/,/^# END infinite-canvas$/d' "${hba_file}"
  {
    printf '\n# BEGIN infinite-canvas\n'
    printf 'host    %s    %s    %s    scram-sha-256\n' "${DB_NAME}" "${DB_USER}" "${DOCKER_SUBNET}"
    printf '# END infinite-canvas\n'
  } >>"${hba_file}"

  psql_admin -Atqc "SELECT pg_reload_conf()" >/dev/null
  hba_errors="$(psql_admin -Atqc "SELECT coalesce(string_agg(error, '; '), '') FROM pg_hba_file_rules WHERE error IS NOT NULL")"
  [ -z "${hba_errors}" ] || fail "pg_hba.conf 配置错误：${hba_errors}"
  restart_postgres
  psql_admin -Atqc "SELECT 1" >/dev/null
}

configure_firewall() {
  log "配置主机防火墙"
  if command -v ufw >/dev/null 2>&1 && ufw status | grep -q '^Status: active'; then
    ufw allow 80/tcp
    ufw allow 443/tcp
    ufw allow from "${DOCKER_SUBNET}" to any port 5432 proto tcp
  fi
  if command -v firewall-cmd >/dev/null 2>&1 && firewall-cmd --state >/dev/null 2>&1; then
    firewall-cmd --permanent --add-service=http
    firewall-cmd --permanent --add-service=https
    firewall-cmd --permanent \
      --add-rich-rule="rule family=ipv4 source address=${DOCKER_SUBNET} port port=5432 protocol=tcp accept"
    firewall-cmd --reload
  fi
}

build_and_start_app() {
  log "构建并启动无限画布"
  "${COMPOSE[@]}" config --quiet
  "${COMPOSE[@]}" build --pull app
  "${COMPOSE[@]}" up -d --remove-orphans

  local attempt
  for attempt in $(seq 1 90); do
    if curl -fsS "http://127.0.0.1:${APP_PORT}/api/health" >/dev/null 2>&1; then
      return
    fi
    sleep 2
  done
  "${COMPOSE[@]}" logs --tail=200 app || true
  fail "应用在 180 秒内未通过健康检查。"
}

configure_nginx() {
  log "配置 Nginx 反向代理"
  local conflicts
  conflicts="$(grep -RIlF "${DOMAIN}" /etc/nginx --include='*.conf' 2>/dev/null |
    grep -Fxv "${NGINX_CONF}" || true)"
  [ -z "${conflicts}" ] ||
    fail "以下 Nginx 配置已使用 ${DOMAIN}，请先合并或停用后再运行：${conflicts}"

  install -d -m 755 /etc/nginx/conf.d
  if [ -f "${NGINX_CONF}" ]; then
    cp "${NGINX_CONF}" "${NGINX_CONF}.bak.$(date +%Y%m%d%H%M%S)"
  fi
  cat >"${NGINX_CONF}" <<EOF
server {
    listen 80;
    listen [::]:80;
    server_name ${DOMAIN};

    client_max_body_size 80m;

    location / {
        proxy_pass http://127.0.0.1:${APP_PORT};
        proxy_http_version 1.1;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_buffering off;
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
    }
}
EOF
  nginx -t
  systemctl enable --now nginx
  systemctl reload nginx
  curl -fsS -H "Host: ${DOMAIN}" "http://127.0.0.1/api/health" >/dev/null
}

confirm_public_network() {
  log "等待公网放行确认"
  printf '应用实际端口：127.0.0.1:%s\n' "${APP_PORT}"
  if [ "${PORT_CHANGED}" -eq 1 ]; then
    printf '检测到 3001 或原端口被占用，已自动切换到 %s，并已同步更新 Nginx。\n' "${APP_PORT}"
  fi
  printf '请在云服务器安全组中放行 TCP 80 和 443。\n'
  printf '不要向公网放行 %s，也不要向公网放行 PostgreSQL 5432。\n' "${APP_PORT}"
  printf '请确认域名 %s 已解析到当前服务器。\n' "${DOMAIN}"

  if [ "${CONFIRM_NETWORK:-0}" != "1" ]; then
    local answer
    read -r -p "完成后输入 yes 继续申请 HTTPS 证书：" answer
    [ "${answer}" = "yes" ] || fail "尚未确认安全组和域名解析。"
  fi

  getent ahostsv4 "${DOMAIN}" >/dev/null 2>&1 ||
    fail "域名 ${DOMAIN} 当前无法解析。"
}

configure_https() {
  log "申请或更新 HTTPS 证书"
  local -a email_args
  if [ -n "${LETSENCRYPT_EMAIL:-}" ]; then
    email_args=(--email "${LETSENCRYPT_EMAIL}")
  else
    email_args=(--register-unsafely-without-email)
  fi
  certbot --nginx \
    --non-interactive \
    --agree-tos \
    --redirect \
    "${email_args[@]}" \
    -d "${DOMAIN}"
  nginx -t
  systemctl reload nginx
}

wait_for_https() {
  local attempt
  for ((attempt = 1; attempt <= 30; attempt++)); do
    if curl -fsS "https://${DOMAIN}/api/health" >/dev/null 2>&1; then
      return
    fi
    sleep 2
  done
  fail "HTTPS 健康检查失败，请检查 DNS、证书和 Nginx。"
}

sync_prompts() {
  log "登录管理员并同步全部提示词"
  local login_json token sync_since sync_json categories_json category encoded total failed sync_logs
  login_json="$(curl -fsS \
    -H 'Content-Type: application/json' \
    -d "$(jq -nc --arg username "${ADMIN_USERNAME}" --arg password "${PROMPT_SYNC_PASSWORD}" \
      '{username: $username, password: $password}')" \
    "http://127.0.0.1:${APP_PORT}/api/admin/login")"
  token="$(jq -er 'if .code == 0 then .data.token else error(.msg) end' <<<"${login_json}")"

  sync_since="$(date --iso-8601=seconds)"
  sync_json="$(curl -fsS --max-time 1800 \
    -X POST \
    -H 'Content-Type: application/json' \
    -H "Authorization: Bearer ${token}" \
    -d '{}' \
    "http://127.0.0.1:${APP_PORT}/api/admin/prompt-categories/sync-all")"
  jq -e 'if .code == 0 then true else error(.msg) end' <<<"${sync_json}" >/dev/null

  categories_json="$(curl -fsS \
    -H "Authorization: Bearer ${token}" \
    "http://127.0.0.1:${APP_PORT}/api/admin/prompt-categories")"
  jq -e 'if .code == 0 and (.data | type == "array") then true else error(.msg // "提示词分类接口返回格式错误") end' \
    <<<"${categories_json}" >/dev/null
  failed=0
  while IFS= read -r category; do
    encoded="$(jq -rn --arg value "${category}" '$value | @uri')"
    total="$(curl -fsS \
      -H "Authorization: Bearer ${token}" \
      "http://127.0.0.1:${APP_PORT}/api/admin/prompts?category=${encoded}&page=1&pageSize=1" |
      jq -er 'if .code == 0 then .data.total else error(.msg) end')"
    printf '提示词分类 %-36s %s 条\n' "${category}" "${total}"
    if [ "${total}" -le 0 ]; then
      failed=1
    fi
  done < <(jq -r '.data[] | select(.remote == true) | .category' <<<"${categories_json}")

  sync_logs="$("${COMPOSE[@]}" logs --since "${sync_since}" app)"
  if grep -q 'scheduled prompt sync failed' <<<"${sync_logs}"; then
    failed=1
  fi
  if [ "${failed}" -ne 0 ]; then
    printf '%s\n' "${sync_logs}"
    fail "至少一个远程提示词分类同步失败。"
  fi
}

print_result() {
  local commit
  commit="$(git -C "${REPO_ROOT}" rev-parse --short HEAD)"
  cat <<EOF

部署完成。
访问地址：https://${DOMAIN}
管理后台：https://${DOMAIN}/admin
提示词管理：https://${DOMAIN}/admin/prompts
管理员用户名：${ADMIN_USERNAME}
管理员初始密码：${ADMIN_PASSWORD}
应用本机端口：127.0.0.1:${APP_PORT}
源码提交：${commit}

请登录后立即修改管理员密码。公网只保留 TCP 80/443，禁止开放 ${APP_PORT} 和 5432。
EOF
}

main() {
  require_root
  install_host_packages
  install_docker
  update_source
  find_app_port
  load_or_create_secrets
  ensure_network
  configure_postgres
  write_environment
  configure_firewall
  build_and_start_app
  configure_nginx
  confirm_public_network
  configure_https
  wait_for_https
  sync_prompts
  print_result
}

main "$@"
