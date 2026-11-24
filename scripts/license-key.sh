#!/usr/bin/env bash
# 授权公钥的唯一取用与校验入口。
# ---------------------------------------------------------------------------
# 被 scripts/package-macos.sh 与 scripts/package-linux.sh source，也是
# scripts/check-license-keys.sh 的实现基础。
#
# 事故回顾（2026-11）：打包脚本原先按「发码端用户数据目录 → ./licadmin-data」
# 的优先级找公钥，于是发码端换过一次数据目录、顺带生成了新密钥之后，同一天
# 打出的客户端悄悄内置了新公钥：已发出的授权码全部验签失败，客户端报「授权码
# 无效或已被篡改」，而打包日志里只有一行 ✓ 看起来一切正常。
#
# 结论：公钥是**信任锚**，必须是版本库里的资产（config/license/public.key），
# 不能是某个运行目录的副产品。运行目录只在「交叉校验」时被读取——两边不一致
# 就当场拦住打包。
# ---------------------------------------------------------------------------

# 版本库里的唯一正式来源。
LICENSE_KEY_CANONICAL="config/license/public.key"

# 发码端运行目录（双击版默认位置）：只用于交叉校验，绝不作为注入来源。
LICENSE_KEY_RUNTIME_DIR="${LICENSE_RUNTIME_DIR:-${HOME}/Library/Application Support/国研智研体授权管理/licadmin-data}"

# license_key_resolve 输出应当注入客户端的公钥文件路径。
#
# 顺序：版本库资产 → 显式指定的 LICENSE_PUBLIC_KEY_FILE（离线/临时构建）。
# 两者都没有就返回非零，由调用方决定「内部构建是否放行」——放行必须显式。
license_key_resolve() {
  if [ -f "${LICENSE_KEY_CANONICAL}" ]; then
    printf '%s\n' "${LICENSE_KEY_CANONICAL}"
    return 0
  fi
  if [ -n "${LICENSE_PUBLIC_KEY_FILE:-}" ]; then
    if [ ! -f "${LICENSE_PUBLIC_KEY_FILE}" ]; then
      echo "✗ LICENSE_PUBLIC_KEY_FILE 指向的文件不存在：${LICENSE_PUBLIC_KEY_FILE}" >&2
      return 1
    fi
    printf '%s\n' "${LICENSE_PUBLIC_KEY_FILE}"
    return 0
  fi
  return 1
}

# license_key_validate <path> 校验文件确实是 Ed25519 公钥，并输出其 KID。
#
# 只做一件事但必须做：少一个字节、多一个换行、base64 抄错一位，都会让客户端
# 在运行时把**所有**授权码判成伪造，而错误信息指向用户手里的码，不指向公钥。
license_key_validate() {
  local path="${1:-}" kid
  if [ -z "${path}" ] || [ ! -f "${path}" ]; then
    echo "✗ 公钥文件不存在：${path:-（空）}" >&2
    return 1
  fi
  if ! kid=$(python3 - "${path}" <<'PY'
import base64
import hashlib
import sys

path = sys.argv[1]
raw = open(path, "r", encoding="utf-8").read().strip()
try:
    data = base64.b64decode(raw, validate=True)
except Exception as exc:  # noqa: BLE001 - 报给用户看就够
    print("不是合法的 base64：%s" % exc)
    sys.exit(1)
if len(data) != 32:
    print("长度 %d 字节，Ed25519 公钥必须是 32 字节" % len(data))
    sys.exit(1)
print(hashlib.sha256(data).hexdigest()[:16])
PY
  ); then
    echo "✗ ${path} 不是可用的 Ed25519 公钥：${kid}" >&2
    return 1
  fi
  printf '%s\n' "${kid}"
}

# license_key_cross_check <injected-key> 比对「要注入的公钥」与「发码端正在用的公钥」。
#
# 不一致说明两边已经分叉：这样打出去的包能装上、能启动，但用户手里的码
# 一律验不过。宁可让打包失败，也不要让这种包流到客户手上。
license_key_cross_check() {
  local injected="${1:-}" runtime_key a b
  runtime_key="${LICENSE_KEY_RUNTIME_DIR}/public.key"
  if [ ! -f "${runtime_key}" ]; then
    echo "  · 未找到发码端运行目录公钥，跳过交叉校验（${runtime_key}）"
    return 0
  fi
  a=$(license_key_validate "${injected}") || return 1
  # 运行目录里的文件可能本来就是坏的；那就没有可比的基准，不要用它去否掉正式资产。
  if ! b=$(license_key_validate "${runtime_key}"); then
    echo "  · 发码端运行目录公钥不可用，跳过交叉校验"
    return 0
  fi
  if [ "${a}" = "${b}" ]; then
    return 0
  fi
  if [ "${LICENSE_ALLOW_KEY_MISMATCH:-0}" = "1" ]; then
    echo "  ⚠ 公钥不一致（注入 ${a} / 发码端 ${b}），已按 LICENSE_ALLOW_KEY_MISMATCH=1 继续" >&2
    return 0
  fi
  echo "✗ 要注入的公钥与发码端当前公钥不一致：" >&2
  echo "    注入（${injected}）：KID ${a}" >&2
  echo "    发码端（${runtime_key}）：KID ${b}" >&2
  echo "  这样打出来的客户端会拒绝该发码端签发的所有授权码。" >&2
  echo "  请确认「正式密钥」是哪一把：把发码端 public.key 复制到 ${LICENSE_KEY_CANONICAL} 后重试；" >&2
  echo "  临时构建可设置 LICENSE_ALLOW_KEY_MISMATCH=1 跳过此检查。" >&2
  return 1
}

# license_key_inject <src> <dst> 注入公钥并打印一致的日志。
license_key_inject() {
  local src="${1:?源文件}" dst="${2:?目标路径}" kid
  kid=$(license_key_validate "${src}") || return 1
  license_key_cross_check "${src}" || return 1
  cp "${src}" "${dst}"
  echo "  ✓ 已嵌入授权公钥（KID ${kid}，来源 ${src}）"
}
