# 需要让当前 shell 使用 mihomo 代理时：
#   source ./proxy-env.sh
# 取消：
#   source ./proxy-env.sh unset
BASE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
. "$BASE_DIR/scripts/proxy-env.sh" "${1:-set}"
