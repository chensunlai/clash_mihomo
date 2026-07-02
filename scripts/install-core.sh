#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BASE_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
. "$BASE_DIR/scripts/proxy-env.sh" unset

VERSION="v1.19.27"
ASSET="mihomo-linux-amd64-v1.19.27.gz"
PRIMARY_URL="https://github.com/MetaCubeX/mihomo/releases/download/${VERSION}/${ASSET}"
SF_URL="https://downloads.sourceforge.net/project/mihomo.mirror/${VERSION}/${ASSET}"
OUT_GZ="$BASE_DIR/bin/${ASSET}"
CORE="$BASE_DIR/bin/mihomo"

mkdir -p "$BASE_DIR/bin"
if [ -x "$CORE" ] && "$CORE" -v >/dev/null 2>&1; then
  echo "[ok] mihomo 已存在：$CORE"
  "$CORE" -v || true
  exit 0
fi

need() { command -v "$1" >/dev/null 2>&1 || { echo "[err] 缺少命令：$1" >&2; exit 1; }; }
need gzip
if command -v curl >/dev/null 2>&1; then
  DL=(curl -fL --retry 3 --connect-timeout 20 -o "$OUT_GZ")
elif command -v wget >/dev/null 2>&1; then
  DL=(wget -O "$OUT_GZ")
else
  echo "[err] 需要 curl 或 wget 下载核心。" >&2
  exit 1
fi

echo "[info] 下载 mihomo ${VERSION} linux-amd64..."
if ! "${DL[@]}" "$PRIMARY_URL"; then
  echo "[warn] GitHub 下载失败，切到 SourceForge 镜像..."
  "${DL[@]}" "$SF_URL"
fi

gzip -dc "$OUT_GZ" > "$CORE"
chmod +x "$CORE"
rm -f "$OUT_GZ"

echo "[ok] 已安装：$CORE"
"$CORE" -v || true
