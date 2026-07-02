#!/usr/bin/env bash
set -euo pipefail

BASE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SUB_FILE="$BASE_DIR/config/subscription.url"
SERVICE_NAME="mihomo.service"
SERVICE_PATH="/etc/systemd/system/$SERVICE_NAME"

read_subscription_url() {
  local url="${1:-${CLASH_SUB_URL:-}}"
  local current=""

  mkdir -p "$BASE_DIR/config"
  if [ -z "$url" ] && [ -s "$SUB_FILE" ]; then
    current="$(sed -n 's/[[:space:]]*$//; /^[[:space:]]*#/d; /^[[:space:]]*$/d; p; q' "$SUB_FILE")"
  fi

  while [ -z "$url" ]; do
    if [ ! -t 0 ]; then
      echo "[err] 未提供订阅 URL。用法：$0 <SUB_URL>" >&2
      exit 2
    fi

    if [ -n "$current" ]; then
      read -r -p "订阅 URL [回车保留当前]: " url
      url="${url:-$current}"
    else
      read -r -p "订阅 URL: " url
    fi

    case "$url" in
      http://*|https://*) ;;
      *)
        echo "[warn] 请输入 http:// 或 https:// 开头的订阅 URL。"
        url=""
        ;;
    esac
  done

  case "$url" in
    http://*|https://*) ;;
    *)
      echo "[err] 订阅 URL 必须以 http:// 或 https:// 开头。" >&2
      exit 2
      ;;
  esac

  umask 077
  printf '%s\n' "$url" > "$SUB_FILE"
  chmod 600 "$SUB_FILE"
}

run_root() {
  if [ "$(id -u)" -eq 0 ]; then
    "$@"
  elif command -v sudo >/dev/null 2>&1; then
    sudo "$@"
  else
    echo "[err] 需要 root 权限或 sudo：$*" >&2
    exit 1
  fi
}

systemd_available() {
  command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]
}

choose_mode() {
  local requested="${1:-${INIT_MODE:-}}"
  local default="nohup"
  local input=""

  case "$requested" in
    ""|1|nohup|NOHUP|2|systemd|SYSTEMD) ;;
    *)
      echo "[err] 无效后台运行方式：$requested" >&2
      exit 2
      ;;
  esac

  case "$requested" in
    1|nohup|NOHUP)
      echo "nohup"
      return
      ;;
    2|systemd|SYSTEMD)
      echo "systemd"
      return
      ;;
  esac

  if systemd_available; then
    default="systemd"
  fi

  if [ ! -t 0 ]; then
    echo "$default"
    return
  fi

  {
    echo
    echo "请选择后台运行方式："
    echo "  1) nohup   - 简单后台运行，适合临时或普通用户环境"
    echo "  2) systemd - 开机自启和失败自动重启，适合服务器"
    printf "选择 [默认：%s]: " "$default"
  } >&2
  read -r input

  case "${input:-$default}" in
    1|nohup|NOHUP)
      echo "nohup"
      ;;
    2|systemd|SYSTEMD)
      echo "systemd"
      ;;
    *)
      echo "[err] 无效选择：$input" >&2
      exit 2
      ;;
  esac
}

disable_systemd_if_present() {
  if ! command -v systemctl >/dev/null 2>&1; then
    return 0
  fi

  if systemctl list-unit-files "$SERVICE_NAME" >/dev/null 2>&1; then
    run_root systemctl disable --now "$SERVICE_NAME" >/dev/null 2>&1 || true
  fi
}

start_with_nohup() {
  disable_systemd_if_present
  "$BASE_DIR/core.sh" restart
}

render_service() {
  local tmp="$1"
  local escaped
  escaped="$(printf '%s' "$BASE_DIR" | sed 's/[\/&]/\\&/g')"
  sed "s/<ABS_PATH>/$escaped/g" "$BASE_DIR/systemd/mihomo.service.template" > "$tmp"
}

start_with_systemd() {
  if ! systemd_available; then
    echo "[err] 当前环境不可用 systemd，请选择 nohup。" >&2
    exit 1
  fi

  "$BASE_DIR/core.sh" stop >/dev/null 2>&1 || true

  local tmp
  tmp="$(mktemp)"
  render_service "$tmp"

  run_root install -m 0644 "$tmp" "$SERVICE_PATH"
  rm -f "$tmp"
  run_root systemctl daemon-reload
  run_root systemctl enable "$SERVICE_NAME"
  run_root systemctl restart "$SERVICE_NAME"

  echo "[ok] systemd 已启动：$SERVICE_NAME"
  echo "MetaCubeXD：http://127.0.0.1:19090/ui/metacubexd/"
  echo "Yacd：http://127.0.0.1:19090/ui/yacd/"
}

main() {
  local mode

  echo "== Mihomo 初始化 =="
  read_subscription_url "${1:-}"

  echo "[info] 更新订阅并生成配置..."
  "$BASE_DIR/update-config.sh"

  mode="$(choose_mode "${2:-}")"
  case "$mode" in
    nohup)
      start_with_nohup
      ;;
    systemd)
      start_with_systemd
      ;;
  esac
}

main "$@"
