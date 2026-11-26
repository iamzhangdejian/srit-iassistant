#!/usr/bin/env bash
#
# macOS 打包脚本：把 Wails 产出的 .app 补充运行资源，并生成 DMG
# ---------------------------------------------------------------------------
# 为什么需要这个脚本：Wails 只产出可执行文件与 Info.plist，而 WeKnora 运行时
# 还需要三样东西才能正常初始化（见 cmd/desktop/main.go 的 resourceRoot 逻辑）：
#   1. config/config.yaml    —— 数据库/检索/存储等配置
#   2. migrations/sqlite/    —— 首次启动建表用的迁移脚本
#   3. .env（或 .env.lite）   —— 运行参数
# 缺任何一样，双击打开时会白屏或直接崩溃。
#
# 可选：设置 SIGN_IDENTITY 与（可选）NOTARY_PROFILE 做签名与公证
#   SIGN_IDENTITY="Developer ID Application: 你的公司 (TEAMID)"
#   NOTARY_PROFILE="notary-profile-name"   # 由 xcrun notarytool store-credentials 创建
# ---------------------------------------------------------------------------
set -euo pipefail

APP_NAME="${1:-国研智研体}"
VERSION="${2:-$(cat VERSION 2>/dev/null || echo 0.9.0)}"
repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

APP_SRC="cmd/desktop/build/bin/${APP_NAME}.app"
if [ ! -d "$APP_SRC" ]; then
  echo "✗ 未找到 $APP_SRC，请先执行 wails build" >&2
  exit 1
fi

RESOURCES="${APP_SRC}/Contents/Resources"
mkdir -p "$RESOURCES"

# 版本号也放进 Resources：cmd/desktop/update.go 的 desktopAboutVersion() 会在
# 运行时按相对路径找 VERSION，而 .app 启动后 cwd 已切到 Resources，
# 把文件放这里能让「关于」对话框和标题栏拿到同一版本号（二进制里还有
# go:embed 兜底，这条是为了人工排查时能直接看到）。
printf '%s\n' "$VERSION" > "$RESOURCES/VERSION"

# ── 1. 注入运行资源 ────────────────────────────────────────────────────
echo "→ 注入运行资源到 .app"
if [ -d config ]; then
  mkdir -p "$RESOURCES/config"
  cp -R config/* "$RESOURCES/config/"
fi
if [ -d migrations/sqlite ]; then
  mkdir -p "$RESOURCES/migrations/sqlite"
  cp -R migrations/sqlite/* "$RESOURCES/migrations/sqlite/"
fi
if [ -f .env.lite.example ]; then
  # 打包的是「默认参数模板」；用户的密钥在首次启动时生成，不打进制品
  cp .env.lite.example "$RESOURCES/.env"
fi

# 前端静态资源必须一起装进来：
# Wails 的 AssetServer 把请求反代到进程内 Gin，而 Gin 只有在 WEKNORA_WEB_DIR
# 指向有效目录（含 index.html）时才注册静态路由。缺了它，窗口一打开就是
# {"error":"Unauthorized: missing authentication"}。
if [ -f frontend/dist/index.html ]; then
  rm -rf "$RESOURCES/web"
  mkdir -p "$RESOURCES/web"
  cp -R frontend/dist/* "$RESOURCES/web/"
  echo "  ✓ 已注入前端静态资源（frontend/dist → Resources/web）"
else
  echo "  ✗ 未找到 frontend/dist，请先在 frontend/ 执行 vite build" >&2
  exit 1
fi
# 授权公钥：唯一正式来源是版本库里的 config/license/public.key。
#
# 为什么不再优先读发码端数据目录：见 scripts/license-key.sh 顶部的事故回顾。
# 公钥是信任锚，换它等于换身份；「文件不存在就顺手用别处的」这类便利，
# 代价是历史授权码集体失效而日志一片绿。
. "$repo_root/scripts/license-key.sh"
if KEY_SRC=$(license_key_resolve); then
  license_key_inject "$KEY_SRC" "$RESOURCES/public.key" || exit 1
else
  if [ "${LICENSE_ALLOW_UNSIGNED:-0}" = "1" ]; then
    echo "  ⚠ LICENSE_ALLOW_UNSIGNED=1：本次打包不含授权公钥，客户端不启用授权闸门"
    echo "    该包只能用于内部测试，不得交付客户"
  else
    echo "  ✗ 未找到授权公钥 $LICENSE_KEY_CANONICAL" >&2
    echo "    正式发行包必须内置授权公钥。请把发码端的 public.key 写入该文件并提交；" >&2
    echo "    或用 LICENSE_PUBLIC_KEY_FILE=<路径> 指定；内部测试构建可显式设置 LICENSE_ALLOW_UNSIGNED=1。" >&2
    exit 1
  fi
fi

# 技能包：管理端「导出发布包」产出的 zip，解到 Resources/skills/<name>/。
#
# 客户端启动时（cmd/desktop/skills_boot.go）会把还没装过的技能装进
# ~/.weknora/skills。不传就跳过：内部构建与不带技能的交付都合法。
SKILLS_BUNDLE="${SKILLS_BUNDLE:-}"
if [ -z "$SKILLS_BUNDLE" ] && [ -f "./skills-bundle.zip" ]; then
  SKILLS_BUNDLE="./skills-bundle.zip"
fi
if [ -n "$SKILLS_BUNDLE" ]; then
  if [ ! -f "$SKILLS_BUNDLE" ]; then
    echo "  ✗ 指定的技能包不存在：$SKILLS_BUNDLE" >&2
    exit 1
  fi
  rm -rf "$RESOURCES/skills"
  mkdir -p "$RESOURCES/skills"
  unzip -q -o "$SKILLS_BUNDLE" -d "$RESOURCES/skills"
  # 清单是给发版核对用的元数据，运行时用不到，留在目录里会被当成技能名。
  rm -f "$RESOURCES/skills/.manifest.json"
  echo "  ✓ 已注入技能包（$(find "$RESOURCES/skills" -mindepth 1 -maxdepth 1 -type d | wc -l | tr -d ' ') 个技能）"
fi

if [ -f scripts/copy-licenses.sh ]; then
  # 该脚本会用 go 工具链拉取许可清单，环境不全时跳过即可，不应阻断打包
  GO111MODULE=on bash scripts/copy-licenses.sh "$RESOURCES" 2>/dev/null || \
    echo "  （跳过第三方许可清单收集：依赖 Go 模块缓存，不影响运行）"
fi

# ── 2. 修正 Info.plist（版本号与版权）──────────────────────────────────
PLIST="${APP_SRC}/Contents/Info.plist"
if [ -f "$PLIST" ]; then
  /usr/libexec/PlistBuddy -c "Set :CFBundleShortVersionString $VERSION" "$PLIST" 2>/dev/null || true
  /usr/libexec/PlistBuddy -c "Set :CFBundleVersion $VERSION" "$PLIST" 2>/dev/null || true
fi

# ── 3. 代码签名（可选）─────────────────────────────────────────────────
if [ -n "${SIGN_IDENTITY:-}" ]; then
  echo "→ 使用证书签名：$SIGN_IDENTITY"
  codesign --force --deep --options runtime \
    --sign "$SIGN_IDENTITY" \
    --entitlements cmd/desktop/build/darwin/entitlements.plist \
    "$APP_SRC" 2>/dev/null || \
  codesign --force --deep --options runtime --sign "$SIGN_IDENTITY" "$APP_SRC"
else
  # 上面注入 Resources/web、public.key 与改写 Info.plist 之后，wails 在 build 阶段
  # 打的 ad-hoc 签名就失效了（codesign 会报 "invalid Info.plist"），此时双击 .app 会被
  # LaunchServices 直接拒绝——表现为「应用已损坏」或点了没反应，只有命令行直接跑
  # Contents/MacOS/<app> 才能启动。所以这里必须重新做一次 ad-hoc 签名。
  echo "→ 未设置 SIGN_IDENTITY：以 ad-hoc 签名重新签名（注入资源后签名必失效）"
  if codesign --force --deep --sign - "$APP_SRC" 2>/tmp/oem_codesign.log; then
    echo "  ✓ 已重新签名（ad-hoc）"
  else
    echo "  ✗ 重新签名失败，产物双击将无法启动："
    sed -n '1,5p' /tmp/oem_codesign.log
    exit 1
  fi
  echo "   分发给外部用户时仍需右键 →「打开」，或执行："
  echo "   xattr -dr com.apple.quarantine \"${APP_NAME}.app\""
fi

# ── 4. 生成 DMG ────────────────────────────────────────────────────────
echo "→ 生成 DMG"
DMG_NAME="${APP_NAME}-${VERSION}-macOS-$(uname -m)"
# 用临时目录做暂存：避免把暂存目录放在 build/ 下，
# 那样 STAGING 自身会被 -srcfolder 递归计入，且历史残留导致 "Directory not empty"
STAGING="$(mktemp -d "${TMPDIR:-/tmp}/oem-dmg.XXXXXX")"
trap 'rm -rf "$STAGING"' EXIT
cp -R "$APP_SRC" "$STAGING/"
ln -s /Applications "$STAGING/Applications"

# 说明：macOS 26/27 上 hdiutil create -srcfolder 已标记弃用，且在某些环境
# （含 CI 沙箱、无图形会话的远程构建）会报 "Directory not empty" 或
# OSStatus error 1。dmg 只是分发容器，不影响程序本身，故做成「尽力而为」：
#   1) 优先 hdiutil / diskutil 生成 DMG
#   2) 失败则回退 zip —— 任何环境都能成功，且解压后同样是 .app
# 两种产物都已写入 bin 目录，供后续分发使用。
DMG_OUT="cmd/desktop/build/bin/${DMG_NAME}.dmg"
ZIP_OUT="cmd/desktop/build/bin/${APP_NAME}-${VERSION}-macOS-$(uname -m).zip"
rm -f "$DMG_OUT" "$ZIP_OUT"

dmg_ok=0
if hdiutil create -volname "$APP_NAME" -srcfolder "$STAGING" \
     -ov -format UDZO "$DMG_OUT" >/tmp/oem_dmg.log 2>&1; then
  dmg_ok=1
elif diskutil image create from --volumeName "$APP_NAME" --format UDZO \
     "$STAGING" "$DMG_OUT" >/tmp/oem_dmg.log 2>&1; then
  dmg_ok=1
fi

if [ "$dmg_ok" = "1" ]; then
  echo "✓ 已生成 DMG"
else
  echo "⚠ 当前环境无法生成 DMG（$(tail -1 /tmp/oem_dmg.log 2>/dev/null)）"
  echo "  → 回退生成 zip 分发包"
fi

# 无论 DMG 是否成功，都额外产出一份 zip：便于脚本化分发与 CI 归档
#
# 这里用 ditto 而不是 zip：
#   1) Info-ZIP 写中文文件名时常不带 UTF-8 标记，macOS 解压后得到乱码目录名
#      （国研智研体.app 变成 ???.app），但 Info.plist 里的 CFBundleExecutable 仍是中文，
#      于是解压出来的包点了打不开 —— 客户侧最常见的一类「解压后无法启动」；
#   2) ditto 会保留签名、扩展属性与符号链接，不会把 DMG 暂存目录里的
#      /Applications 软链一起打进去（zip 打包 STAGING/. 会把它带进包里）。
echo "→ 生成 zip 分发包"
ditto -c -k --sequesterRsrc --keepParent "$APP_SRC" "$ZIP_OUT"
echo "✓ zip：${ZIP_OUT}"


# ── 5. 公证（可选）─────────────────────────────────────────────────────
if [ -n "${NOTARY_PROFILE:-}" ]; then
  echo "→ 提交苹果公证（约 2~5 分钟）"
  xcrun notarytool submit "cmd/desktop/build/bin/${DMG_NAME}.dmg" \
    --keychain-profile "$NOTARY_PROFILE" --wait
  xcrun stapler staple "cmd/desktop/build/bin/${DMG_NAME}.dmg"
fi

echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo " macOS 打包完成，产物："
# 没有 .dmg 时 glob 不展开会让 set -e 直接退出，这里刻意忽略
ls -lh cmd/desktop/build/bin/*.dmg cmd/desktop/build/bin/*.zip 2>/dev/null | awk '{print "   "$9" ("$5")"}' || true
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"