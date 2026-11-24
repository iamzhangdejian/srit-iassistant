#!/usr/bin/env bash
#
# 把授权码发码管理端打包成 macOS 的 .app
# ---------------------------------------------------------------------------
# 目标：让不懂终端的同事双击图标就能用——自动生成密钥、自动开浏览器、
# 口令存成文件，不需要 cd、不需要记命令、不需要看终端输出。
#
# 用法：
#   bash scripts/package-licadmin.sh              # 默认名字与版本（浏览器模式）
#   bash scripts/package-licadmin.sh "授权管理" "1.1.0"
#   NATIVE=1 bash scripts/package-licadmin.sh     # 原生窗口版（内置 WKWebView 窗口）
#
# 浏览器模式与原生窗口版共用同一份服务端与前端资产，区别只在「界面装在哪」：
# 前者启动后调用 open 打开默认浏览器，后者把同一个 http://127.0.0.1:<port>
# 装进一个独立窗口，关掉窗口即退出。
#
# 版本号默认取仓库根目录的 VERSION 文件（与客户端一致）。
# ---------------------------------------------------------------------------
set -euo pipefail

APP_NAME="${1:-国研智研体授权管理}"
# 版本默认跟客户端保持一致（VERSION 文件），避免发版时两套版本号对不上。
VERSION="${2:-$(cat VERSION 2>/dev/null || echo 0.9.0)}"
repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

# 与主应用共用同一套工具链（仓库内 tools/），避免依赖用户机器上的 Go 版本。
TOOLS="${repo_root}/../tools"
if [ ! -x "${TOOLS}/go/bin/go" ]; then
  echo "✗ 未找到 ${TOOLS}/go/bin/go" >&2
  exit 1
fi
export PATH="${TOOLS}/go/bin:${TOOLS}/node24/bin:/usr/bin:/bin:/usr/sbin:/sbin"
export GOCACHE="${TOOLS}/gocache"
export GOMODCACHE="${TOOLS}/gomodcache"
export GO111MODULE=on
export GOFLAGS="-mod=mod"

BUILD_DIR="cmd/licadmin/build"
APP_DIR="${BUILD_DIR}/${APP_NAME}.app"
BIN_DIR="${APP_DIR}/Contents/MacOS"
RES_DIR="${APP_DIR}/Contents/Resources"

echo "→ 编译 licadmin"
mkdir -p "${BIN_DIR}" "${RES_DIR}"
# 阶段 2 起账号库用 SQLite（database/sql + mattn/go-sqlite3），必须开 CGO。
# 代价是产物不再能跨架构静态分发，但发码端本来只发 macOS .app，
# 而且换纯 Go 的 SQLite 实现要引一个十几 MB 的新依赖，不划算。
# NATIVE=1 时多编一个 WebKit 窗口（构建标签 licadmin_native）。
# 这个标签把 webview_go 挡在默认构建之外：命令行/CI 环境不开 CGO 也能编过，
# 产物也不会平白多背一个 WebKit 依赖。
BUILD_TAGS=""
if [ "${NATIVE:-0}" = "1" ]; then
  BUILD_TAGS="-tags licadmin_native"
  echo "  模式：原生窗口（WKWebView）"
else
  echo "  模式：浏览器（双击后打开默认浏览器）"
fi
CGO_ENABLED=1 go build -trimpath ${BUILD_TAGS} \
  -ldflags="-s -w -X main.buildVersion=${VERSION}" \
  -o "${BIN_DIR}/licadmin" ./cmd/licadmin

# Info.plist：LSMinimumSystemVersion 用 11.0 —— 低于这个版本的系统上
# osascript 的 display dialog 行为不一致，不值得支持。
echo "→ 写入 Info.plist"
cat > "${APP_DIR}/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>${APP_NAME}</string>
  <key>CFBundleDisplayName</key><string>${APP_NAME}</string>
  <key>CFBundleIdentifier</key><string>com.guoyan.licadmin</string>
  <key>CFBundleVersion</key><string>${VERSION}</string>
  <key>CFBundleShortVersionString</key><string>${VERSION}</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleExecutable</key><string>licadmin</string>
  <key>CFBundleIconFile</key><string>AppIcon</string>
  <key>LSMinimumSystemVersion</key><string>11.0</string>
  <key>NSHighResolutionCapable</key><true/>
  <!-- 原生窗口版把界面装在窗口里，加载的是 http://127.0.0.1:<port>；
       ATS 默认拦截明文 http，本地回环要显式放行（浏览器模式不受影响）。 -->
  <key>NSAppTransportSecurity</key>
  <dict>
    <key>NSAllowsLocalNetworking</key><true/>
  </dict>
  <key>LSUIElement</key><false/>
</dict>
</plist>
PLIST

# 启动脚本：以 -app-mode 运行，参数与日志都由程序自己处理。
# 不放可执行逻辑进 Info.plist，保持 Go 二进制是唯一入口，便于排查。
printf '%s\n' "${VERSION}" > "${RES_DIR}/VERSION"

# 图标：有 assets 里的图标就转成 icns，没有就跳过（系统显示默认图标）。
ICON_SRC=""
for candidate in assets/icon/logo.png assets/logo.png resources/appicon.png; do
  if [ -f "${candidate}" ]; then ICON_SRC="${candidate}"; break; fi
done
if [ -n "${ICON_SRC}" ]; then
  echo "→ 生成图标（${ICON_SRC}）"
  ICONSET="${BUILD_DIR}/AppIcon.iconset"
  rm -rf "${ICONSET}"
  mkdir -p "${ICONSET}"
  for size in 16 32 128 256 512; do
    sips -z $size $size "${ICON_SRC}" --out "${ICONSET}/icon_${size}x${size}.png" >/dev/null 2>&1 || true
    double=$((size * 2))
    sips -z $double $double "${ICON_SRC}" --out "${ICONSET}/icon_${size}x${size}@2x.png" >/dev/null 2>&1 || true
  done
  iconutil -c icns "${ICONSET}" -o "${RES_DIR}/AppIcon.icns" 2>/dev/null || \
    echo "  （iconutil 失败，跳过图标）"
  rm -rf "${ICONSET}"
fi

# 代码签名：默认 adhoc。分发到别人机器上会被 Gatekeeper 拦，
# 需要 Developer ID 时设置 SIGN_IDENTITY 环境变量。
echo "→ 签名"
if [ -n "${SIGN_IDENTITY:-}" ]; then
  codesign --force --deep --options runtime --sign "${SIGN_IDENTITY}" "${APP_DIR}"
else
  codesign --force --deep --sign - "${APP_DIR}" 2>&1 | grep -v "replacing existing signature" || true
fi
# 签完必须验一遍：签名失效的 .app 在命令行还能跑，双击却会被 Gatekeeper
# 拒绝，用户只看到「已损坏」。这类问题只有落到用户手里才会暴露。
codesign --verify --deep --strict "${APP_DIR}" || {
  echo "✗ 代码签名校验失败，产物不可用" >&2
  exit 1
}

# 打成 zip 便于传输（.app 是目录，直接压缩会丢符号链接与权限位）。
echo "→ 打包 zip"
ZIP_PATH="$(pwd)/${BUILD_DIR}/${APP_NAME}-${VERSION}-macOS.zip"
rm -f "${ZIP_PATH}"
# 用 ditto 而不是 zip：Info-ZIP 不写中文文件名的 UTF-8 标记，解压出来会变成
# 「???.app」，而 Info.plist 里的 CFBundleExecutable 仍是中文 → 解压后同样打不开。
ditto -c -k --sequesterRsrc --keepParent "${APP_DIR}" "${ZIP_PATH}"

echo ""
echo "✓ 打包完成"
echo "  App：${APP_DIR}"
echo "  Zip：${ZIP_PATH}（$(du -h "${ZIP_PATH}" | cut -f1)）"
echo ""
if [ "${NATIVE:-0}" = "1" ]; then
  echo "  双击 ${APP_NAME}.app 即可启动，界面在独立窗口里打开。"
else
  echo "  双击 ${APP_NAME}.app 即可启动，界面在浏览器里打开。"
fi
# v3.18 起私钥缺失**不会**自动生成（换钥事故的根因）。首次部署必须显式 -init-key，
# 或双击后点确认框里的「生成新密钥」。
echo "  ⚠ 首次部署不会自动生成密钥：命令行用 -init-key，双击版会弹确认框。"
echo "    密钥只生成一次；生成的公钥要写入客户端 config/license/public.key 后重新打包。"
echo "  第一次打开会引导创建管理员账号（之后用账号密码登录）。"
echo "  自动化令牌（供脚本调用）在"
echo "  ~/Library/Application Support/国研智研体授权管理/licadmin-data/自动化令牌.txt"
