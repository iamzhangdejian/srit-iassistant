#!/usr/bin/env bash
#
# Linux 打包脚本：生成 AppImage
# ---------------------------------------------------------------------------
# 重要限制（务必写进交付文档）：
#   WebView 方案无法把 libwebkit2gtk 完全静态打进 AppImage。目标机必须预先安装：
#     Debian/Ubuntu : sudo apt install libwebkit2gtk-4.1-0 libgtk-3-0
#     Fedora/RHEL   : sudo dnf install webkit2gtk4.1 gtk3
#     Arch          : sudo pacman -S webkit2gtk-4.1 gtk3
#   这是 Wails/Tauri 等所有 WebView 类框架的共性限制，不是本项目的缺陷。
#
# 另外：Linux 上构建需要带 webkit2_41 标签（上游 CI 同样如此），
# 因为较新的发行版只提供 webkit2gtk-4.1，不再提供 4.0。
# ---------------------------------------------------------------------------
set -euo pipefail

# STAGE_ONLY=1 只组装 AppDir（不调用 linuxdeploy/appimagetool），
# 用于在非 Linux 机器上校验资源注入是否完整。
STAGE_ONLY="${STAGE_ONLY:-0}"

APP_NAME="${1:-国研智研体}"
VERSION="${2:-$(cat VERSION 2>/dev/null || echo 0.9.0)}"
repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

BIN="cmd/desktop/build/bin/${APP_NAME}"
PLACEHOLDER_BIN=0
if [ ! -f "$BIN" ] && [ "$STAGE_ONLY" = "1" ]; then
  # 非 Linux 主机上也想验证「资源注入是否完整」时，用一个空文件占位
  mkdir -p "$(dirname "$BIN")"
  : > "$BIN"
  chmod +x "$BIN"
  PLACEHOLDER_BIN=1
  echo "  （STAGE_ONLY：$BIN 不存在，用空占位文件校验资源注入）"
fi
if [ ! -f "$BIN" ]; then
  echo "✗ 未找到 $BIN，请先执行 wails build" >&2
  exit 1
fi

# ── 1. 校验系统依赖 ────────────────────────────────────────────────────
echo "→ 校验 Linux 运行时依赖"
missing=0
for lib in libwebkit2gtk-4.1.so.0 libgtk-3.so.0; do
  if ! ldconfig -p 2>/dev/null | grep -q "$lib"; then
    echo "⚠ 缺失 $lib —— 目标机也必须有，否则无法启动"
    missing=1
  fi
done
[ "$missing" = "1" ] && echo "  本机缺失不影响打包，但请写进使用说明"

# ── 2. 准备 AppDir ─────────────────────────────────────────────────────
echo "→ 准备 AppDir"
AppDir="cmd/desktop/build/AppDir"
rm -rf "$AppDir"
mkdir -p "$AppDir/usr/bin" "$AppDir/usr/share/applications" "$AppDir/usr/share/icons/hicolor/512x512/apps"

cp "$BIN" "$AppDir/usr/bin/${APP_NAME}"

# 运行资源（与 macOS 同理，缺了会启动失败）
mkdir -p "$AppDir/usr/share/${APP_NAME}"
[ -d config ] && cp -R config "$AppDir/usr/share/${APP_NAME}/"
[ -d migrations/sqlite ] && { mkdir -p "$AppDir/usr/share/${APP_NAME}/migrations"; cp -R migrations/sqlite "$AppDir/usr/share/${APP_NAME}/migrations/"; }
[ -f .env.lite.example ] && cp .env.lite.example "$AppDir/usr/share/${APP_NAME}/.env"

# 授权公钥：与 macOS 走同一套取用与校验逻辑（scripts/license-key.sh）。
. "$repo_root/scripts/license-key.sh"
if KEY_SRC=$(license_key_resolve); then
  license_key_inject "$KEY_SRC" "$AppDir/usr/share/${APP_NAME}/public.key" || exit 1
else
  if [ "${LICENSE_ALLOW_UNSIGNED:-0}" = "1" ]; then
    echo "  ⚠ LICENSE_ALLOW_UNSIGNED=1：本次打包不含授权公钥（仅限内部测试）"
  else
    echo "  ✗ 未找到授权公钥 $LICENSE_KEY_CANONICAL，正式包不能缺少它" >&2
    echo "    请把发码端 public.key 写入该文件并提交，或用 LICENSE_PUBLIC_KEY_FILE=<路径> 指定。" >&2
    exit 1
  fi
fi

# 版本号与许可清单：随包交付，便于合规审计与「关于」信息
[ -f VERSION ] && cp VERSION "$AppDir/usr/share/${APP_NAME}/VERSION"
[ -f LICENSE ] && cp LICENSE "$AppDir/usr/share/${APP_NAME}/LICENSE"
[ -f THIRD_PARTY_NOTICES.md ] && cp THIRD_PARTY_NOTICES.md "$AppDir/usr/share/${APP_NAME}/THIRD_PARTY_NOTICES.md"
[ -d licenses ] && cp -R licenses "$AppDir/usr/share/${APP_NAME}/licenses"
# 技能包：管理端导出的 skills-bundle.zip → usr/share/<app>/skills/<name>/
# 客户端启动时按版本装进 ~/.weknora/skills（见 cmd/desktop/skills_boot.go）。
SKILLS_BUNDLE="${SKILLS_BUNDLE:-}"
if [ -z "$SKILLS_BUNDLE" ] && [ -f "./skills-bundle.zip" ]; then
  SKILLS_BUNDLE="./skills-bundle.zip"
fi
if [ -n "$SKILLS_BUNDLE" ]; then
  if [ ! -f "$SKILLS_BUNDLE" ]; then
    echo "✗ 指定的技能包不存在：$SKILLS_BUNDLE" >&2
    exit 1
  fi
  rm -rf "$AppDir/usr/share/${APP_NAME}/skills"
  mkdir -p "$AppDir/usr/share/${APP_NAME}/skills"
  unzip -q -o "$SKILLS_BUNDLE" -d "$AppDir/usr/share/${APP_NAME}/skills"
  rm -f "$AppDir/usr/share/${APP_NAME}/skills/.manifest.json"
  echo "✓ 已注入技能包（$(find "$AppDir/usr/share/${APP_NAME}/skills" -mindepth 1 -maxdepth 1 -type d | wc -l | tr -d ' ') 个技能）"
fi


# 前端静态资源：Gin 需要 WEKNORA_WEB_DIR 指向含 index.html 的目录，
# 否则窗口打开即 401。AppRun 会把该路径导出为 WEKNORA_WEB_DIR。
if [ -f frontend/dist/index.html ]; then
  rm -rf "$AppDir/usr/share/${APP_NAME}/web"
  mkdir -p "$AppDir/usr/share/${APP_NAME}/web"
  cp -R frontend/dist/* "$AppDir/usr/share/${APP_NAME}/web/"
  echo "  ✓ 已注入前端静态资源"
else
  echo "  ✗ 未找到 frontend/dist，请先执行 vite build" >&2
  exit 1
fi

# 桌面入口
cat > "$AppDir/usr/share/applications/${APP_NAME}.desktop" <<DESKTOP
[Desktop Entry]
Type=Application
Name=${APP_NAME}
Comment=企业知识库与智能问答一体化平台
Exec=${APP_NAME} %U
Icon=${APP_NAME}
Terminal=false
Categories=Office;Education;
DESKTOP

# 图标（复用 Wails 提供的 appicon）
if [ -f cmd/desktop/build/appicon.png ]; then
  cp cmd/desktop/build/appicon.png "$AppDir/usr/share/icons/hicolor/512x512/apps/${APP_NAME}.png"
  cp cmd/desktop/build/appicon.png "$AppDir/${APP_NAME}.png"
fi

# AppRun
# AppRun 里写死应用名：AppImage 挂载点目录名是随机的，不能靠 basename 推断
cat > "$AppDir/AppRun" <<APPRUN
#!/bin/bash
HERE="\$(dirname "\$(readlink -f "\$0")")"
export PATH="\$HERE/usr/bin:\$PATH"
# 资源根：config/ 与 public.key 都在这里，二进制靠它 chdir 并加载授权公钥
export WEKNORA_RESOURCE_ROOT="\$HERE/usr/share/${APP_NAME}"
export WEKNORA_WEB_DIR="\$HERE/usr/share/${APP_NAME}/web"
exec "\$HERE/usr/bin/${APP_NAME}" "\$@"
APPRUN
chmod +x "$AppDir/AppRun"

# ── 3. 生成 AppImage ───────────────────────────────────────────────────
if [ "$STAGE_ONLY" = "1" ]; then
  echo "✓ STAGE_ONLY=1：已组装 ${AppDir}（跳过 AppImage 生成）"
  echo "  资源目录：${AppDir}/usr/share/${APP_NAME}"
  ls -1 "$AppDir/usr/share/${APP_NAME}"
  [ "$PLACEHOLDER_BIN" = "1" ] && rm -f "$BIN"
  exit 0
fi

OUT="cmd/desktop/build/bin/${APP_NAME}-${VERSION}-linux-$(uname -m).AppImage"

if command -v linuxdeploy >/dev/null 2>&1; then
  echo "→ 使用 linuxdeploy 生成 AppImage"
  linuxdeploy --appdir "$AppDir" --output appimage
  # linuxdeploy 会输出到当前目录，移动并改名
  produced=$(ls -t *AppImage 2>/dev/null | head -1 || true)
  [ -n "$produced" ] && mv "$produced" "$OUT"
elif command -v appimagetool >/dev/null 2>&1; then
  echo "→ 使用 appimagetool 生成 AppImage"
  ARCH="$(uname -m)" appimagetool "$AppDir" "$OUT"
else
  echo "⚠ 未检测到 linuxdeploy 或 appimagetool"
  echo "   安装方式："
  echo "     curl -sL https://github.com/linuxdeploy/linuxdeploy/releases/download/continuous/linuxdeploy-x86_64.AppImage -o linuxdeploy && chmod +x linuxdeploy && sudo mv linuxdeploy /usr/local/bin/"
  echo "   已生成 AppDir，可手动打包：$AppDir"
  exit 0
fi

echo "✓ Linux 打包完成：$OUT"
echo "  提示：目标机需安装 libwebkit2gtk-4.1-0 与 libgtk-3-0"