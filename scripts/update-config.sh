#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BASE_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
. "$BASE_DIR/scripts/proxy-env.sh" unset

SUB_URL="${1:-${CLASH_SUB_URL:-}}"
SUB_FILE="$BASE_DIR/config/subscription.url"
OUT="$BASE_DIR/config/config.yaml"
UI_DIR="$BASE_DIR/config/ui"
mkdir -p "$BASE_DIR/config"

if [ -z "$SUB_URL" ] && [ -s "$SUB_FILE" ]; then
  SUB_URL="$(sed -n 's/[[:space:]]*$//; /^[[:space:]]*#/d; /^[[:space:]]*$/d; p; q' "$SUB_FILE")"
fi

if [ -z "$SUB_URL" ]; then
  echo "[err] 未提供订阅地址。" >&2
  echo "      用法：$0 <SUB_URL>" >&2
  echo "      或：CLASH_SUB_URL=<SUB_URL> $0" >&2
  echo "      或：把订阅地址写入 $SUB_FILE" >&2
  exit 1
fi

RAW="$(mktemp "$BASE_DIR/config/subscription.raw.XXXXXX")"
trap 'rm -f "$RAW"' EXIT

fetch_ok=0
if command -v curl >/dev/null 2>&1; then
  echo "[info] 拉取订阅..."
  if curl -k -fL --retry 2 --connect-timeout 20 -o "$RAW" "$SUB_URL"; then fetch_ok=1; fi
elif command -v wget >/dev/null 2>&1; then
  echo "[info] 拉取订阅..."
  if wget --no-check-certificate -O "$RAW" "$SUB_URL"; then fetch_ok=1; fi
else
  echo "[err] 缺少 curl/wget，无法更新订阅。" >&2
  exit 1
fi

if [ "$fetch_ok" -ne 1 ]; then
  echo "[err] 订阅拉取失败。已保留现有 config.yaml（如存在）。" >&2
  exit 1
fi
python3 "$BASE_DIR/scripts/merge_config.py" "$RAW" "$OUT" "$UI_DIR"
echo "[ok] 配置已生成：$OUT"
