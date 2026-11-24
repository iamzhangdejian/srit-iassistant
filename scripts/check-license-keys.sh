#!/usr/bin/env bash
# 授权公钥一致性体检：把「版本库里的公钥」「发码端正在用的公钥」
# 「已构建安装包里内置的公钥」摆在一起比对。
# ---------------------------------------------------------------------------
# 用法：
#   bash scripts/check-license-keys.sh [安装包路径]
#
# 安装包路径可选，默认探测 dist_install/ 等常见位置；找不到就跳过该项。
#
# 退出码：0 一致（或无可比对对象）；1 发现分叉。
#
# 为什么需要它：2026-11 的事故里，三个位置各有一份公钥且没有任何一处报错——
# 发码端用新钥签、客户端内置新钥、历史码是老钥签的。症状只出现在客户端
# （「授权码无效或已被篡改」），排查方向被彻底带偏。这个脚本把
# 「三方是不是同一把钥」变成一条命令。
# ---------------------------------------------------------------------------
set -uo pipefail

caller_pwd="${PWD}"
repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "${repo_root}"
. "${repo_root}/scripts/license-key.sh"

bundle="${1:-}"
# 相对路径按「调用者当时所在目录」解析，否则仓库根下传 dist_install/... 会被当成
# weknora_src/dist_install/...（不存在）而误报「安装包公钥缺失」。
if [ -n "${bundle}" ] && [ "${bundle#/}" = "${bundle}" ]; then
  if [ -d "${caller_pwd}/${bundle}" ]; then
    bundle="${caller_pwd}/${bundle}"
  fi
fi
if [ -z "${bundle}" ]; then
  for candidate in \
    "dist_install/国研智研体.app" \
    "cmd/desktop/build/bin/国研智研体.app" \
    "/Applications/国研智研体.app"; do
    if [ -d "${candidate}" ]; then bundle="${candidate}"; break; fi
  done
fi

fail=0
rows=""

# describe <角色> <路径> <KID>：KID 为空表示文件缺失或不可用。
describe() {
  local role="$1" path="$2" kid="$3"
  # 用 	 分隔而不是按列对齐：中文宽度在不同终端差异很大，硬对齐全变成歪的。
  # 命令替换会吞掉行尾换行，所以显式补一个，否则所有行会挤成一条。
  rows+=$(printf '%s\t%s\t%s' "${role}" "${kid:-缺失}" "${path}")$'\n'
}

# read_kid <角色> <路径>：把 KID 写进全局 READ_KID，失败时 READ_KID 为空。
#
# 不用「echo 出结果再 $(...)」的写法：命令替换会开子 shell，函数里对 rows
# 与 fail 的修改全部丢失，最后打印出来的是空白表格——这本身就是一个
# 「看起来跑成功了其实什么都没检查」的陷阱。
READ_KID=""
read_kid() {
  local role="$1" path="$2" kid
  READ_KID=""
  if [ ! -f "${path}" ]; then
    describe "${role}" "${path}" ""
    return 1
  fi
  if ! kid=$(license_key_validate "${path}" 2>/dev/null); then
    describe "${role}" "${path}" "不可用"
    fail=1
    return 1
  fi
  describe "${role}" "${path}" "${kid}"
  READ_KID="${kid}"
  return 0
}

canonical_kid=""
read_kid "版本库" "${LICENSE_KEY_CANONICAL}" && canonical_kid="${READ_KID}"
runtime_kid=""
read_kid "发码端" "${LICENSE_KEY_RUNTIME_DIR}/public.key" && runtime_kid="${READ_KID}"

bundle_kid=""
if [ -n "${bundle}" ]; then
  case "${bundle}" in
    *.app) bundle_key_path="${bundle}/Contents/Resources/public.key" ;;
    *)     bundle_key_path="${bundle}" ;;
  esac
  read_kid "安装包" "${bundle_key_path}" && bundle_kid="${READ_KID}"
else
  rows+=$(printf '%s\t%s\t%s' "安装包" "未指定" "（传入 .app 路径可一并校验）")$'\n'
fi

echo "授权公钥一致性体检"
echo "--------------------------------------------------------------------"
printf '%s' "${rows}"
echo "--------------------------------------------------------------------"

# 遗留位置：旧数据目录里的密钥对。它一旦被误用，就是下一次事故。
if [ -d "licadmin-data" ]; then
  echo "⚠ 检测到仓库内的旧密钥目录 licadmin-data/"
  legacy_kid=""
  if [ -f "licadmin-data/public.key" ]; then
    legacy_kid=$(license_key_validate "licadmin-data/public.key" 2>/dev/null) || legacy_kid=""
  fi
  if [ -n "${legacy_kid}" ]; then
    echo "  其中公钥 KID：${legacy_kid}"
    if [ -n "${canonical_kid}" ] && [ "${legacy_kid}" != "${canonical_kid}" ]; then
      echo "  与版本库 KID（${canonical_kid}）不同 —— 是旧密钥残留，只会误导人。"
    fi
  fi
  echo "  该目录已被 .gitignore 忽略；确认无用后移到仓库外的备份目录。"
  echo
fi

if [ -z "${canonical_kid}" ]; then
  echo "✗ 版本库里没有可用的 ${LICENSE_KEY_CANONICAL} —— 打包脚本会直接失败。"
  echo "  请把发码端 public.key 复制过来并提交。"
  exit 1
fi

if [ -n "${runtime_kid}" ] && [ "${runtime_kid}" != "${canonical_kid}" ]; then
  echo "✗ 发码端正在用的公钥（${runtime_kid}）与版本库（${canonical_kid}）不一致。"
  echo "  当前签出的授权码，客户端会全部判为无效。"
  fail=1
fi
if [ -n "${bundle_kid}" ] && [ "${bundle_kid}" != "${canonical_kid}" ]; then
  echo "✗ 安装包内置公钥（${bundle_kid}）与版本库（${canonical_kid}）不一致。"
  echo "  这个包会拒绝版本库公钥签出的授权码，请重新打包。"
  fail=1
fi

if [ "${fail}" = "0" ]; then
  echo "✓ 可比对的公钥全部一致（KID ${canonical_kid}）"
fi
exit "${fail}"
