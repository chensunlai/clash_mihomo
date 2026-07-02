#!/usr/bin/env bash
set -euo pipefail

BASE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
. "$BASE_DIR/scripts/proxy-env.sh" unset

CORE="$BASE_DIR/bin/mihomo"
CONFIG_DIR="$BASE_DIR/config"
CONFIG="$CONFIG_DIR/config.yaml"
PID="$BASE_DIR/mihomo.pid"
LOG="$BASE_DIR/logs/mihomo.log"

print_urls() {
  echo "HTTP/SOCKS/MIXED：127.0.0.1:17890"
  echo "控制 API：127.0.0.1:19090"
  echo "MetaCubeXD：http://127.0.0.1:19090/ui/metacubexd/"
  echo "Yacd：http://127.0.0.1:19090/ui/yacd/"
}

ensure_core() {
  if [ ! -x "$CORE" ]; then
    "$BASE_DIR/scripts/install-core.sh"
  fi
}

ensure_config() {
  if [ -s "$CONFIG" ]; then
    return 0
  fi

  if ! "$BASE_DIR/scripts/update-config.sh"; then
    echo "[warn] 未能更新订阅，生成无节点基础配置，面板仍可打开。"
    echo "       可稍后执行：./update-config.sh <SUB_URL> && ./core.sh restart"
    python3 "$BASE_DIR/scripts/merge_config.py" /dev/null "$CONFIG" "$BASE_DIR/config/ui"
  fi
}

is_running() {
  [ -f "$PID" ] && kill -0 "$(cat "$PID")" 2>/dev/null
}

start_mihomo() {
  mkdir -p "$CONFIG_DIR" "$BASE_DIR/logs"
  ensure_core
  ensure_config

  if is_running; then
    echo "[ok] mihomo 已在运行，PID=$(cat "$PID")"
    print_urls
    return 0
  fi

  echo "[info] 使用 nohup 启动 mihomo..."
  nohup "$CORE" -d "$CONFIG_DIR" > "$LOG" 2>&1 &
  echo $! > "$PID"
  sleep 1

  if is_running; then
    echo "[ok] 已启动，PID=$(cat "$PID")"
    print_urls
    echo "日志：$LOG"
    return 0
  fi

  echo "[err] 启动失败，日志如下：" >&2
  tail -80 "$LOG" >&2 || true
  return 1
}

stop_mihomo() {
  if is_running; then
    kill "$(cat "$PID")"
    rm -f "$PID"
    echo "[ok] 已停止 mihomo"
    return 0
  fi

  pkill -f "$BASE_DIR/bin/mihomo" 2>/dev/null || true
  rm -f "$PID"
  echo "[ok] 没有检测到运行中的 mihomo"
}

status_mihomo() {
  if is_running; then
    echo "[ok] mihomo 正在运行，PID=$(cat "$PID")"
    print_urls
  else
    echo "[ok] mihomo 未运行"
  fi
}

case "${1:-start}" in
  start)
    start_mihomo
    ;;
  stop)
    stop_mihomo
    ;;
  restart)
    stop_mihomo || true
    start_mihomo
    ;;
  status)
    status_mihomo
    ;;
  *)
    echo "usage: $0 {start|stop|restart|status}" >&2
    exit 2
    ;;
esac
