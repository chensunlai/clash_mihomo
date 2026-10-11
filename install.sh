#!/usr/bin/env bash
set -euo pipefail
umask 077

install_dir="${HOME}/.local/clash_mihomo"
release_version="latest"
release_repo="chensunlai/clash_mihomo"
local_binary=""
skip_core=0
link_command=1
prompt_mode=auto
bashrc_mode=ask
bashrc_file="$HOME/.bashrc"

usage() {
  cat <<'HELP'
安装 clashctl（Linux amd64 / arm64，无需 Go、Node 或 Python）

bash install.sh [--dir DIR] [--version v1.0.2] [--repo OWNER/REPO]
                [--binary LOCAL_FILE] [--skip-core] [--no-link]
                [--yes] [--init-bashrc|--no-init-bashrc] [--bashrc FILE]

默认安装目录：~/.local/clash_mihomo
默认进入安装向导：选择目录、确认安装、选择是否写入 bashrc 和打开 TUI。
从 GitHub 最新 Release 下载当前架构的二进制并校验 SHA256。
--yes 非交互安装，除非显式指定 --init-bashrc，否则不修改 bashrc。
--dir 预设向导的安装目录；--bashrc 指定初始化文件（默认 ~/.bashrc）。
--binary 使用本地构建产物，适合首次发布前安装或离线安装。
--skip-core 暂不下载 mihomo；之后在 TUI 中按 i 安装。
--no-link 不创建 ~/.local/bin/clashctl 链接。
HELP
}

while (($#)); do
  case "$1" in
    --dir|--version|--repo|--binary|--bashrc)
      (($# >= 2)) || { echo "缺少 $1 的参数" >&2; exit 2; }
      case "$1" in
        --dir) install_dir="$2" ;;
        --version) release_version="$2" ;;
        --repo) release_repo="$2" ;;
        --binary) local_binary="$2" ;;
        --bashrc) bashrc_file="$2" ;;
      esac
      shift 2 ;;
    --skip-core) skip_core=1; shift ;;
    --no-link) link_command=0; shift ;;
    -y|--yes) prompt_mode=off; shift ;;
    --interactive) prompt_mode=on; shift ;;
    --init-bashrc) bashrc_mode=yes; shift ;;
    --no-init-bashrc) bashrc_mode=no; shift ;;
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

interactive=0
if [[ "$prompt_mode" != off ]]; then
  if [[ "$prompt_mode" == on || -t 0 ]]; then
    exec {prompt_fd}<&0
    interactive=1
  elif { exec {prompt_fd}<>/dev/tty; } 2>/dev/null; then
    interactive=1
  else
    echo '无法读取终端。自动化安装请加 --yes；交互安装可用 bash <(curl -fsSL URL)。' >&2
    exit 2
  fi
fi

ask() {
  printf '%s' "$1" >&2
  if ! IFS= read -r answer <&"$prompt_fd"; then
    echo '未收到输入，安装已取消。' >&2
    exit 1
  fi
}

confirm() {
  local label="$1" default="$2"
  while true; do
    ask "$label [$default] "
    case "${answer:-$default}" in
      y|Y|yes|YES) return 0 ;;
      n|N|no|NO) return 1 ;;
      *) echo '请输入 y 或 n。' >&2 ;;
    esac
  done
}

if ((interactive)); then
  printf '\nclashctl 安装向导 · Linux %s\n\n' "$target_arch"
  ask "安装目录 [$install_dir]："
  install_dir="${answer:-$install_dir}"
fi
case "$install_dir" in '~') install_dir="$HOME" ;; '~/'*) install_dir="$HOME/${install_dir:2}" ;; esac
[[ -n "$install_dir" ]] || { echo "安装目录不能为空" >&2; exit 2; }
if ((interactive)); then
  echo "将安装到：$install_dir"
  if ! confirm '确认安装？' Y; then
    echo '安装已取消。'
    exit 0
  fi
fi
mkdir -p -- "$install_dir/bin"
install_dir="$(cd -- "$install_dir" && pwd -P)"
temporary_dir="$(mktemp -d "$install_dir/.install-XXXXXXXX")"
cleanup() {
  for name in clashctl SHA256SUMS checksum bashrc.new; do
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

initialize_bashrc=0
if [[ "$bashrc_mode" == yes ]]; then
  initialize_bashrc=1
elif [[ "$bashrc_mode" == ask ]] && ((interactive)); then
  if confirm "是否将 clashctl 加入 $bashrc_file，让新终端可以直接运行？" N; then
    initialize_bashrc=1
  fi
fi

if ((initialize_bashrc)); then
  case "$bashrc_file" in '~/'*) bashrc_file="$HOME/${bashrc_file:2}" ;; esac
  mkdir -p -- "$(dirname -- "$bashrc_file")"
  [[ -e "$bashrc_file" ]] || : > "$bashrc_file"
  begin_marker='# >>> clashctl initialize >>>'
  end_marker='# <<< clashctl initialize <<<'
  if ! awk -v begin="$begin_marker" -v end="$end_marker" '
    $0 == begin { if (inside) exit 2; inside = 1; next }
    $0 == end && inside { inside = 0; next }
    !inside { print }
    END { if (inside) exit 2 }
  ' "$bashrc_file" > "$temporary_dir/bashrc.new"; then
    echo 'bashrc 中的 clashctl 初始化标记不完整，原文件未修改。' >&2
    exit 1
  fi
  printf -v quoted_bin '%q' "$install_dir/bin"
  printf '\n%s\nexport PATH=%s:"$PATH"\n%s\n' "$begin_marker" "$quoted_bin" "$end_marker" >> "$temporary_dir/bashrc.new"
  # Keep the existing file's permissions and follow a user-managed symlink.
  cat "$temporary_dir/bashrc.new" > "$bashrc_file"
  echo "已写入 $bashrc_file；新终端可以直接运行 clashctl。"
  printf '当前终端生效：source %q\n' "$bashrc_file"
fi

printf '打开 TUI：%q\n' "$install_dir/bin/clashctl"
if ((interactive)) && confirm '现在打开 TUI？' N; then
  "$install_dir/bin/clashctl" <&"$prompt_fd"
fi
