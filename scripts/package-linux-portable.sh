#!/usr/bin/env bash
# 生成 Linux (aarch64/x86_64) 可分发产物：便携 tar.gz + deb 安装包。
#
# 前置：已用 wails build 产出 cmd/desktop/build/bin/<APP_NAME>，
#       且 frontend/dist 已构建（make build-lite 或 npm run build）。
# 用法：bash scripts/package-linux-portable.sh
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

APP_NAME="${APP_NAME:-国研智研体}"
VERSION="${VERSION:-$(cat VERSION 2>/dev/null || echo 0.9.0)}"
BIN="cmd/desktop/build/bin/${APP_NAME}"
DEB_PKG="${DEB_PKG:-guoyan-desktop}"
OUT_DIR="${OUT_DIR:-$repo_root/dist}"
ARCH="$(dpkg --print-architecture 2>/dev/null || uname -m)"

[ -f "$BIN" ] || { echo "✗ 缺少 $BIN，请先执行 wails build" >&2; exit 1; }
[ -f frontend/dist/index.html ] || { echo "✗ 缺少 frontend/dist，请先构建前端" >&2; exit 1; }

# ── 运行目录（便携版与 deb 共用） ───────────────────────────────────────
STAGE="$OUT_DIR/stage/${APP_NAME}"
rm -rf "$STAGE"
mkdir -p "$STAGE"
cp "$BIN" "$STAGE/${APP_NAME}"
cp -r config "$STAGE/config"
cp -r migrations "$STAGE/migrations"
cp -r frontend/dist "$STAGE/web"
cp config/license/public.key "$STAGE/public.key"
cp cmd/desktop/build/appicon.png "$STAGE/appicon.png"
# 客户端配置：优先用本地 .env.lite（必须包含 WK_LICENSE_PUBLIC_KEY），否则用模板
if [ -f .env.lite ]; then
  cp .env.lite "$STAGE/.env.lite"
else
  cp .env.lite.example "$STAGE/.env.lite"
fi

cat > "$STAGE/run.sh" <<'EOF'
#!/bin/sh
DIR=$(cd "$(dirname "$0")" && pwd)
cd "$DIR" || exit 1
export WEBKIT_DISABLE_COMPOSITING_MODE=1
export LIBGL_ALWAYS_SOFTWARE=1
exec "$DIR/国研智研体" "$@"
EOF

cat > "$STAGE/install-desktop.sh" <<'EOF'
#!/bin/sh
DIR=$(cd "$(dirname "$0")" && pwd)
APPS="$HOME/.local/share/applications"
DESK=$(xdg-user-dir DESKTOP 2>/dev/null || echo "$HOME/Desktop")
mkdir -p "$APPS"
cat > "$APPS/国研智研体.desktop" <<DESKTOP
[Desktop Entry]
Type=Application
Version=1.0
Name=国研智研体
Name[zh_CN]=国研智研体
Comment=企业知识库与智能问答一体化平台（桌面版）
Exec=$DIR/run.sh
Path=$DIR
Icon=$DIR/appicon.png
Terminal=false
StartupWMClass=国研智研体
Categories=Office;Utility;Education;
DESKTOP
chmod +x "$APPS/国研智研体.desktop"
if [ -d "$DESK" ]; then
  cp "$APPS/国研智研体.desktop" "$DESK/国研智研体.desktop"
  chmod +x "$DESK/国研智研体.desktop"
  gio set "$DESK/国研智研体.desktop" metadata::trusted true 2>/dev/null || true
fi
echo "已注册桌面入口: $APPS/国研智研体.desktop"
EOF

chmod +x "$STAGE/${APP_NAME}" "$STAGE/run.sh" "$STAGE/install-desktop.sh"

# ── 便携包 ─────────────────────────────────────────────────────────────
TARBALL="$OUT_DIR/${APP_NAME}-linux-${ARCH}-v${VERSION}-portable.tar.gz"
tar -czf "$TARBALL" -C "$OUT_DIR/stage" "$APP_NAME"
echo "✓ 便携包: $TARBALL"

# ── deb 安装包 ─────────────────────────────────────────────────────────
if command -v dpkg-deb >/dev/null 2>&1; then
  DEBROOT="$OUT_DIR/debroot"
  rm -rf "$DEBROOT"
  mkdir -p "$DEBROOT/DEBIAN" "$DEBROOT/opt"
  cp -r "$STAGE" "$DEBROOT/opt/${APP_NAME}"
  size_kb=$(du -sk "$DEBROOT/opt/${APP_NAME}" | cut -f1)
  cat > "$DEBROOT/DEBIAN/control" <<EOF
Package: ${DEB_PKG}
Version: ${VERSION}
Section: utils
Priority: optional
Architecture: ${ARCH}
Installed-Size: ${size_kb}
Depends: libc6 (>= 2.31), libgtk-3-0, libwebkit2gtk-4.0-37, libsoup2.4-1, libstdc++6
Maintainer: SRIT <maintainer@localhost>
Description: ${APP_NAME} desktop app (WeKnora Lite based)
 Enterprise knowledge base and AI Q&A desktop client.
 Bundled anydoc office parser, SQLite storage, no external services.
EOF
  cat > "$DEBROOT/DEBIAN/postinst" <<EOF
#!/bin/sh
set -e
mkdir -p /usr/share/applications
cat > /usr/share/applications/${DEB_PKG}.desktop <<DESKTOP
[Desktop Entry]
Type=Application
Version=1.0
Name=${APP_NAME}
Name[zh_CN]=${APP_NAME}
Comment=企业知识库与智能问答一体化平台（桌面版）
Exec=/opt/${APP_NAME}/run.sh
Path=/opt/${APP_NAME}
Icon=/opt/${APP_NAME}/appicon.png
Terminal=false
StartupWMClass=${APP_NAME}
Categories=Office;Utility;Education;
DESKTOP
chmod +x /usr/share/applications/${DEB_PKG}.desktop
ln -sf "/opt/${APP_NAME}/run.sh" /usr/local/bin/${DEB_PKG}
update-desktop-database /usr/share/applications >/dev/null 2>&1 || true
exit 0
EOF
  cat > "$DEBROOT/DEBIAN/prerm" <<EOF
#!/bin/sh
set -e
rm -f /usr/share/applications/${DEB_PKG}.desktop /usr/local/bin/${DEB_PKG}
update-desktop-database /usr/share/applications >/dev/null 2>&1 || true
exit 0
EOF
  chmod 755 "$DEBROOT/DEBIAN/postinst" "$DEBROOT/DEBIAN/prerm"
  DEB="$OUT_DIR/${APP_NAME}_${VERSION}_${ARCH}.deb"
  # 用 fakeroot 以 root:root 属主打包，避免安装后文件归属构建用户
  if command -v fakeroot >/dev/null 2>&1; then
    fakeroot -- bash -c 'chown -R root:root "$1/opt" && dpkg-deb --build "$1" "$2" >/dev/null' _ "$DEBROOT" "$DEB"
  else
    dpkg-deb --build "$DEBROOT" "$DEB" >/dev/null
  fi
  echo "✓ deb 安装包: $DEB"
  rm -rf "$DEBROOT"
else
  echo "⚠ 未找到 dpkg-deb，跳过 deb"
fi

rm -rf "$OUT_DIR/stage"

echo
echo "目标机要求：${ARCH}；glibc >= 2.31；libwebkit2gtk-4.0；libgtk-3。"
echo "授权：每台机器需各自的授权码（绑定网卡/机器指纹）。"