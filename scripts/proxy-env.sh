#!/usr/bin/env bash
# 用法：
#   source ./proxy-env.sh
#   source ./proxy-env.sh unset
mode="${1:-set}"

finish() {
  return "$1" 2>/dev/null || exit "$1"
}

unset_proxy_vars() {
  for v in \
    http_proxy https_proxy all_proxy no_proxy ftp_proxy rsync_proxy \
    HTTP_PROXY HTTPS_PROXY ALL_PROXY NO_PROXY FTP_PROXY RSYNC_PROXY \
    npm_config_proxy npm_config_https_proxy NPM_CONFIG_PROXY NPM_CONFIG_HTTPS_PROXY \
    GIT_PROXY_COMMAND git_proxy_command; do
    unset "$v" 2>/dev/null || true
  done
}

case "$mode" in
  set|"")
    export http_proxy=http://127.0.0.1:17890
    export https_proxy=http://127.0.0.1:17890
    export HTTP_PROXY=http://127.0.0.1:17890
    export HTTPS_PROXY=http://127.0.0.1:17890
    export all_proxy=socks5://127.0.0.1:17890
    export ALL_PROXY=socks5://127.0.0.1:17890
    ;;
  unset|off|clear)
    unset_proxy_vars
    ;;
  *)
    echo "usage: source ./proxy-env.sh [set|unset]" >&2
    finish 2
    ;;
esac
