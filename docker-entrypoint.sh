#!/bin/sh
set -eu

GENERATED_MEDIA_DIR="${GENERATED_MEDIA_DIR:-data/generated-images}"
mkdir -p "$GENERATED_MEDIA_DIR"
# 修复旧版本留下的 0600 文件和受 umask 影响的目录，保证后端重启或切换运行用户后仍可读取。
if ! chmod -R u+rwX,go+rX "$GENERATED_MEDIA_DIR"; then
  echo "warning: failed to normalize generated media permissions: $GENERATED_MEDIA_DIR" >&2
fi

PORT=8080 /app/server &
API_PID=$!

cd /app/web
PORT=3001 node server.js &
WEB_PID=$!

shutdown() {
  trap - INT TERM
  kill -TERM "$API_PID" "$WEB_PID" 2>/dev/null || true
  wait "$API_PID" 2>/dev/null || true
  wait "$WEB_PID" 2>/dev/null || true
  exit 0
}

trap shutdown INT TERM

while kill -0 "$API_PID" 2>/dev/null && kill -0 "$WEB_PID" 2>/dev/null; do
  sleep 1
done

kill -TERM "$API_PID" "$WEB_PID" 2>/dev/null || true
wait "$API_PID" 2>/dev/null || true
wait "$WEB_PID" 2>/dev/null || true
exit 1
