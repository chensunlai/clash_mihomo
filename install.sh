#!/usr/bin/env bash
set -euo pipefail
umask 077

install_dir="${HOME}/.local/clash_mihomo"
release_version="latest"
release_repo="chensunlai/clash_mihomo"
local_binary=""
skip_core=0
link_command=1

usage() {
  cat <<'HELP'
安装 clashctl（Linux amd64 / arm64，无需 Go、Node 或 Python）

bash install.sh [--dir DIR] [--version v1.0.0] [--repo OWNER/REPO]
                [--binary LOCAL_FILE] [--skip-core] [--no-link]

默认安装目录：~/.local/clash_mihomo
默认从 GitHub 最新 Release 下载当前架构的二进制并校验 SHA256。
--binary 使用本地构建产物，适合首次发布前安装或离线安装。
--skip-core 暂不下载 mihomo；之后在 TUI 中按 i 安装。
--no-link 不创建 ~/.local/bin/clashctl 链接。
HELP
}

while (($#)); do
  case "$1" in
    --dir|--version|--repo|--binary)
      (($# >= 2)) || { echo "缺少 $1 的参数" >&2; exit 2; }
      case "$1" in
        --dir) install_dir="$2" ;;
        --version) release_version="$2" ;;
        --repo) release_repo="$2" ;;
        --binary) local_binary="$2" ;;
      esac
      shift 2 ;;
    --skip-core) skip_core=1; shift ;;
    --no-link) link_command=0; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "未知选项：$1" >&2; usage >&2; exit 2 ;;
  esac
done

[[ "$(uname -s)" == Linux ]] || { echo "目前支持 Linux" >&2; exit 1; }
case "$(uname -m)" in
  x86_64|amd64) target_arch=amd64 ;;
  aarch64|arm64) target_arch=arm64 ;;
  *) echo "不支持的架构：$(uname -m)" >&2; exit 1 ;;
esac
case "$install_dir" in '~') install_dir="$HOME" ;; '~/'*) install_dir="$HOME/${install_dir:2}" ;; esac
[[ -n "$install_dir" ]] || { echo "安装目录不能为空" >&2; exit 2; }
mkdir -p -- "$install_dir/bin"
install_dir="$(cd -- "$install_dir" && pwd -P)"
temporary_dir="$(mktemp -d "$install_dir/.install-XXXXXXXX")"
cleanup() {
  for name in clashctl SHA256SUMS checksum; do
    [[ ! -e "$temporary_dir/$name" ]] || unlink "$temporary_dir/$name"
  done
  rmdir "$temporary_dir" 2>/dev/null || true
}
trap cleanup EXIT

if [[ -n "$local_binary" ]]; then
  [[ -f "$local_binary" ]] || { echo "本地二进制不存在" >&2; exit 1; }
  cp -- "$local_binary" "$temporary_dir/clashctl"
else
  command -v curl >/dev/null || { echo "需要 curl" >&2; exit 1; }
  command -v sha256sum >/dev/null || { echo "需要 sha256sum" >&2; exit 1; }
  if [[ "$release_version" == latest ]]; then
    download_base="https://github.com/$release_repo/releases/latest/download"
  else
    [[ "$release_version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([.-][a-zA-Z0-9.-]+)?$ ]] || { echo "版本格式示例：v1.0.0" >&2; exit 2; }
    download_base="https://github.com/$release_repo/releases/download/$release_version"
  fi
  asset="clashctl-linux-$target_arch"
  echo "下载 $asset ($release_version)…"
  curl --fail --location --retry 2 --connect-timeout 15 --max-time 180 "$download_base/$asset" -o "$temporary_dir/clashctl"
  curl --fail --location --retry 2 --connect-timeout 15 --max-time 60 "$download_base/SHA256SUMS" -o "$temporary_dir/SHA256SUMS"
  checksum="$(awk -v asset="$asset" '$2 == asset { print $1 }' "$temporary_dir/SHA256SUMS")"
  [[ "$checksum" =~ ^[a-fA-F0-9]{64}$ ]] || { echo "发布包校验信息无效" >&2; exit 1; }
  printf '%s  clashctl\n' "$checksum" > "$temporary_dir/checksum"
  (cd -- "$temporary_dir" && sha256sum --check checksum)
fi

chmod 755 "$temporary_dir/clashctl"
"$temporary_dir/clashctl" version
mv -- "$temporary_dir/clashctl" "$install_dir/bin/clashctl"

if ((link_command)); then
  mkdir -p -- "$HOME/.local/bin"
  command_link="$HOME/.local/bin/clashctl"
  if [[ -e "$command_link" && ! -L "$command_link" ]]; then
    echo "保留已有 $command_link；请直接使用 $install_dir/bin/clashctl"
  else
    ln -sfn -- "$install_dir/bin/clashctl" "$command_link"
  fi
fi

if ((!skip_core)); then
  if ! CLASHCTL_HOME="$install_dir" "$install_dir/bin/clashctl" install-core; then
    echo "管理器已安装，但核心下载失败；稍后执行 $install_dir/bin/clashctl install-core 或在 TUI 中按 i 重试。" >&2
    exit 1
  fi
fi
echo "已安装到 $install_dir"
printf '打开 TUI：%q\n' "$install_dir/bin/clashctl"
case ":$PATH:" in *":$HOME/.local/bin:"*) ;; *) echo '可将 ~/.local/bin 加入 PATH 后直接运行 clashctl。' ;; esac
