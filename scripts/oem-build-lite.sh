#!/usr/bin/env bash
#
# 国研智研体 桌面版统一构建脚本
# ---------------------------------------------------------------------------
# 用途：一条命令产出带完整能力的桌面应用（macOS .app / Windows .exe / Linux AppImage）
#
# 相比上游 `wails build`，本脚本额外保证三件事：
#   1. sqlite_fts5  —— 打开 SQLite 全文索引，恢复关键词（BM25）混合检索
#   2. anydoc       —— 内链 Rust 文档解析引擎，支持 PDF/Word/Excel/PPT 无 Python 依赖
#   3. production   —— 前端静态资源内嵌进二进制，单个可执行文件自包含
#
# 用法：
#   bash scripts/oem-build-lite.sh                 # 给当前平台打包
#   SKIP_ANYDOC=1 bash scripts/oem-build-lite.sh   # 跳过 anydoc（快速验证用）
#   TARGET=darwin/arm64 bash scripts/oem-build-lite.sh
#
# 依赖：
#   - Go 1.26+           （go.mod 要求）
#   - Node 22+           （前端构建）
#   - wails CLI v2.12.0  （go install github.com/wailsapp/wails/v2/cmd/wails@v2.12.0）
#   - Rust/cargo         （仅 anydoc 需要；SKIP_ANYDOC=1 可跳过）
#   - macOS: Xcode Command Line Tools
#   - Linux: libgtk-3-dev libwebkit2gtk-4.1-dev
#   - Windows: NSIS（生成安装包时）
#
# 环境变量隔离说明：本脚本把所有工具链缓存重定向到 <repo>/tools/ 下，
# 避免写入 ~/go、~/Library/Caches 等沙箱受限或污染全局的目录。
# ---------------------------------------------------------------------------
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "${repo_root}"

# ── 可配置项：改品牌只改这里 ────────────────────────────────────────────
APP_NAME="${APP_NAME:-国研智研体}"
BUNDLE_ID="${BUNDLE_ID:-com.srit.knowledge-workbench}"
VERSION="${VERSION:-$(cat VERSION 2>/dev/null || echo 1.0.0)}"
# 可选：自动更新仓库（owner/repo）。为空则客户端不启用自动更新（默认且安全）。
# 例：UPDATE_REPO=srit/knowledge-workbench bash scripts/oem-build-lite.sh
UPDATE_REPO="${UPDATE_REPO:-}"
# 可选：自有更新服务地址（管理端 base URL，例如 http://192.168.1.9:8091）。
# 配了它就优先走 /api/updates/latest，GitHub 通道只在没配时兜底。
# 例：UPDATE_BASE_URL=http://192.168.1.9:8091 bash scripts/oem-build-lite.sh
UPDATE_BASE_URL="${UPDATE_BASE_URL:-}"

# ── 构建标签：四个标签的生命周期必须一起改 ──────────────────────────────
#   desktop    : Wails 桌面壳（cmd/desktop/main.go 的入口约束）
#   production : 前端资源内嵌（缺它 Wails 会拒绝启动）
#   sqlite_fts5: mattn/go-sqlite3 的 FTS5 全文索引
#   anydoc     : 内链 Rust 文档解析库（backend_cgo.go 的 tag 约束）
BUILD_TAGS="desktop production sqlite_fts5"
if [ "${SKIP_ANYDOC:-0}" != "1" ]; then
  BUILD_TAGS="${BUILD_TAGS} anydoc"
fi

# ── 工具链环境 ─────────────────────────────────────────────────────────
# 工具链放在仓库【同级】的 tools/ 目录（与 weknora_src 平级），
# 允许用 TOOLS_ROOT 覆盖，便于其他人把工具链装到任意位置。
TOOLS_ROOT="${TOOLS_ROOT:-$(cd "${repo_root}/.." && pwd)/tools}"
export PATH="${TOOLS_ROOT}/go/bin:${TOOLS_ROOT}/node24/bin:$PATH"
export GO111MODULE=on
export GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"
export GOSUMDB=off                      # ~/go/pkg/sumdb 可能不可写
export GOFLAGS="${GOFLAGS:--mod=mod}"
export GOCACHE="${TOOLS_ROOT}/gocache"
export GOMODCACHE="${TOOLS_ROOT}/gomodcache"
export CARGO_HOME="${TOOLS_ROOT}/cargo"
export RUSTUP_HOME="${TOOLS_ROOT}/rustup"

TARGET_PLATFORM="${TARGET:-$(go env GOOS)/$(go env GOARCH)}"

echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo " 应用名称 : ${APP_NAME}"
echo " 版本     : ${VERSION}"
echo " 目标平台 : ${TARGET_PLATFORM}"
echo " 构建标签 : ${BUILD_TAGS}"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

# ── 步骤 0：前置检查 ───────────────────────────────────────────────────
need() {
  command -v "$1" >/dev/null 2>&1 || { echo "✗ 缺少依赖：$1（$2）" >&2; exit 1; }
}
need go    "安装 Go 1.26+"
need node  "安装 Node 22+"
need wails "go install github.com/wailsapp/wails/v2/cmd/wails@v2.12.0"

if [[ "${BUILD_TAGS}" == *anydoc* ]]; then
  need cargo "安装 Rust（https://rustup.rs），或设 SKIP_ANYDOC=1 跳过文档解析引擎"
fi

# ── 平台相关的交叉编译工具链 ───────────────────────────────────────────
# 这里的依赖全部是 cgo 的（mattn-sqlite3 / sqlite-vec / duckdb / anydoc），
# 所以 windows 目标必须有一个 MinGW-w64 的 CC（wails 只会设 GOOS/GOARCH，
# 不装编译器、也不替你开 CGO）。跨编译时下面会强制 CGO_ENABLED=1 并把
# CC/CXX 指向 MinGW；在 Windows 上原生构建时这两个变量同样生效。
GOOS_TARGET="${TARGET_PLATFORM%%/*}"
GOARCH_TARGET="${TARGET_PLATFORM##*/}"

case "${GOOS_TARGET}/${GOARCH_TARGET}" in
  windows/amd64) RUST_TARGET=x86_64-pc-windows-gnu; ANYDOC_DIR=windows_amd64 ;;
  windows/arm64) RUST_TARGET=aarch64-pc-windows-gnullvm; ANYDOC_DIR=windows_arm64 ;;
  darwin/arm64)  RUST_TARGET=aarch64-apple-darwin;     ANYDOC_DIR=darwin_arm64 ;;
  darwin/amd64)  RUST_TARGET=x86_64-apple-darwin;      ANYDOC_DIR=darwin_amd64 ;;
  linux/amd64)   RUST_TARGET=x86_64-unknown-linux-gnu; ANYDOC_DIR=linux_amd64_gnu ;;
  linux/arm64)   RUST_TARGET=aarch64-unknown-linux-gnu; ANYDOC_DIR=linux_arm64_gnu ;;
  *)             RUST_TARGET="";                       ANYDOC_DIR="" ;;
esac

if [[ "${GOOS_TARGET}" == windows ]]; then
  if [[ "${GOARCH_TARGET}" != amd64 ]]; then
    echo "✗ 目前只支持 TARGET=windows/amd64（其它架构的 anydoc 预编译库不存在）" >&2
    exit 1
  fi
  # Windows 版不编译 DuckDB：duckdb-go-bindings 的 windows 预编译静态库是 clang
  # 「模拟 TLS」(-femulated-tls) 产物，需要 __emutls_v.* 与 __emutls_get_address，
  # 而 UCRT64 的 GCC 用原生 TLS、libgcc 无 emutls 运行时，二者无法链接。
  # 代价：Windows 版「对话内 Excel 数据分析」与「xlsx 表格摄取」不可用
  # （xlsx 预览走前端 SheetJS，不受影响）。恢复办法：去掉 noduckdb 标签。
  BUILD_TAGS="${BUILD_TAGS} noduckdb"
  echo "→ Windows 构建：跳过 DuckDB（-tags noduckdb）"
  # 两种情形：
  #   a) 在 Windows 上原生构建（MSYS2 / Git-Bash）——编译器就叫 gcc/g++，
  #      而且必须是 UCRT64 那一套：duckdb 的 windows-amd64 预编译静态库引用
  #      __stdio_common_vsnprintf_s / __imp___timezone 等 UCRT 符号，用 MSVCRT
  #      工具链链接会报一堆 undefined reference。
  #   b) 在 macOS/Linux 上交叉编译——要用 x86_64-w64-mingw32-* 前缀的交叉编译器。
  host_sys="$(uname -s 2>/dev/null || echo unknown)"
  case "${host_sys}" in
    MINGW*|MSYS*|CYGWIN*)
      NATIVE_WINDOWS=1
      MINGW_CC="${MINGW_CC:-gcc}"
      MINGW_CXX="${MINGW_CXX:-g++}"
      ;;
    *)
      NATIVE_WINDOWS=0
      MINGW_CC="${MINGW_CC:-x86_64-w64-mingw32-gcc}"
      MINGW_CXX="${MINGW_CXX:-x86_64-w64-mingw32-g++}"
      ;;
  esac
  if ! command -v "${MINGW_CC}" >/dev/null 2>&1; then
    if [[ "${NATIVE_WINDOWS}" == 1 ]]; then
      echo "✗ 未找到 gcc：请在 MSYS2 UCRT64 终端里构建（pacman -S mingw-w64-ucrt-x86_64-gcc）" >&2
      echo "  注意不要用 MSVCRT 工具链，duckdb 的预编译库是 UCRT 的。" >&2
    else
      echo "✗ 交叉编译 Windows 需要 MinGW-w64：brew install mingw-w64" >&2
      echo "  （也可设 MINGW_CC/MINGW_CXX 指向已有的 x86_64-w64-mingw32-* 工具链）" >&2
    fi
    exit 1
  fi
  export CGO_ENABLED=1
  export CC="${MINGW_CC}"
  export CXX="${MINGW_CXX}"

  # MinGW 默认把 libstdc++/libgcc/winpthread 做成动态依赖（exe 里 import
  # libstdc++-6.dll / libgcc_s_seh-1.dll），客户机没有这几个 DLL，双击直接弹
  # 「由于找不到 libstdc++-6.dll，无法继续执行代码」。这里静态链接它们，
  # 交付物只有单个 exe。duckdb 在 Windows 上已排除（-tags noduckdb），
  # 其 LDFLAGS 里的 --static 不再参与，静态链接不会冲突。
  # 用完整 -static：只加 -static-libgcc/-static-libstdc++ 时，MinGW 仍会把
  # winpthread 留成动态 import（exe 里照样出现 libwinpthread-1.dll）。
  # 全静态后交付物只依赖系统 UCRT 的 api-ms-win-crt-*（Win10+ 自带）。
  export CGO_LDFLAGS="${CGO_LDFLAGS:-} -static -static-libgcc -static-libstdc++"
  echo "→ Windows 运行库：全静态链接（libgcc / libstdc++ / winpthread）"

  # sqlite-vec 的 cgo 包装 #include "sqlite3.h"，macOS 能从 SDK 里找到，
  # MinGW 的 sysroot 没有。mattn/go-sqlite3 把同一份 amalgamation 头文件
  # 存成 sqlite3-binding.h，这里生成一个只做转发的 sqlite3.h 垫片。
  # 三种方式依次尝试：go list（模块已在缓存）→ go mod download 后再 list →
  # 直接扫 GOMODCACHE（CI 上偶发 go list 在模块尚未下载时直接失败）。
  mattn_dir="$(go list -m -f '{{.Dir}}' github.com/mattn/go-sqlite3 2>/dev/null || true)"
  if [[ ! -d "${mattn_dir}" ]]; then
    echo "→ 定位 mattn/go-sqlite3 头文件：go list 未命中，先 go mod download"
    go mod download github.com/mattn/go-sqlite3 >/dev/null 2>&1 || true
    mattn_dir="$(go list -m -f '{{.Dir}}' github.com/mattn/go-sqlite3 2>/dev/null || true)"
  fi
  if [[ ! -d "${mattn_dir}" ]]; then
    modcache="$(go env GOMODCACHE 2>/dev/null || true)"
    if [[ -n "${modcache}" ]]; then
      mattn_dir="$(ls -d "${modcache}"/github.com/mattn/go-sqlite3@* 2>/dev/null | head -1)"
    fi
  fi
  if [[ -n "${mattn_dir}" && -d "${mattn_dir}" ]]; then
    shim_dir="${TOOLS_ROOT}/cgo-include"
    mkdir -p "${shim_dir}"
    printf '#include "sqlite3-binding.h"\n' > "${shim_dir}/sqlite3.h"
    # -I 的路径必须是 gcc 能认的形式：MinGW/UCRT64 的 gcc 不认 MSYS 的 /d/a/...
    # （实测 "sqlite3.h: No such file or directory"），用 cygpath -m 换成
    # D:/a/... 这种混写形式；非 MSYS 环境（macOS/Linux）没有 cygpath，原样用。
    shim_flag_dir="${shim_dir}"
    mattn_flag_dir="${mattn_dir}"
    if command -v cygpath >/dev/null 2>&1; then
      shim_flag_dir="$(cygpath -m "${shim_dir}")"
      mattn_flag_dir="$(cygpath -m "${mattn_dir}")"
    fi
    export CGO_CFLAGS="${CGO_CFLAGS:-} -I${shim_flag_dir} -I${mattn_flag_dir}"
    echo "  sqlite3 垫片：${shim_dir}/sqlite3.h（mattn 头文件在 ${mattn_dir}）"
  else
    echo "✗ 无法定位 github.com/mattn/go-sqlite3（需要它提供 sqlite3-binding.h）" >&2
    echo "  go list 结果：'${mattn_dir}'；GOMODCACHE=$(go env GOMODCACHE 2>/dev/null)" >&2
    echo "  排查：go list -m -f '{{.Dir}}' github.com/mattn/go-sqlite3" >&2
    exit 1
  fi
  if ! command -v makensis >/dev/null 2>&1; then
    echo "⚠ 未检测到 makensis：只产出 exe，不产出安装包（brew install makensis）"
  fi
fi

# ── 步骤 1：同步版本号到可 embed 的位置 ────────────────────────────────
# cmd/desktop/desktop_version.go 用 go:embed 把版本号打进二进制，
# 保证 .app 启动后 os.Chdir 到 Resources 也能显示正确版本（那里没有 VERSION 文件）。
# 同时下面的 wails build 会用 -ldflags -X main.version 再覆盖一遍。
echo "→ 同步版本号 ${VERSION} → cmd/desktop/VERSION.embedded"
printf '%s\n' "${VERSION}" > cmd/desktop/VERSION.embedded

# ── 步骤 2：编译 anydoc 静态库（Rust）──────────────────────────────────
if [[ "${BUILD_TAGS}" == *anydoc* ]]; then
  # 必须按目标平台取库：库里同时可能有 darwin_arm64 与 windows_amd64，
  # 早先的 `find ... -name "*.a" | head -1` 会把宿主机的 .a 当成已存在，
  # 于是一路跳过编译，最后在链接阶段报一堆 undefined symbol。
  anydoc_lib=""
  if [[ -n "${ANYDOC_DIR}" ]]; then
    for candidate in \
      "third_party/anydoc-go/lib/${ANYDOC_DIR}/libanydoc_go.a" \
      "third_party/anydoc-go/lib/${ANYDOC_DIR}/anydoc_go.lib"; do
      if [[ -f "${candidate}" ]]; then anydoc_lib="${candidate}"; break; fi
    done
  fi
  if [ -n "${anydoc_lib}" ]; then
    echo "✓ anydoc 静态库已存在（${ANYDOC_DIR}），跳过编译：${anydoc_lib}"
  else
    echo "→ 编译 anydoc 文档解析引擎（${RUST_TARGET:-宿主平台}，首次约 10~20 分钟）"
    if [[ -n "${RUST_TARGET}" ]]; then
      TARGET="${RUST_TARGET}" bash scripts/build-anydoc-lib.sh
    else
      bash scripts/build-anydoc-lib.sh
    fi
  fi
else
  echo "⚠ SKIP_ANYDOC=1：不内链 anydoc，PDF/Word/Excel 将无法解析"
fi

# ── 步骤 3：构建桌面应用 ───────────────────────────────────────────────
echo "→ 构建桌面应用（${APP_NAME}）"
pushd cmd/desktop >/dev/null

# macOS 上链接器会因重复 -lc++ 报警，压制掉以免淹没真正错误
if [[ "${TARGET_PLATFORM}" == darwin* ]]; then
  export CGO_LDFLAGS="${CGO_LDFLAGS:--Wl,-no_warn_duplicate_libraries}"
fi
export CGO_CFLAGS="${CGO_CFLAGS:-} -Wno-deprecated-declarations"

# Windows 需 NSIS 打安装包；其他平台不加该参数
NSIS_FLAG=""
if [[ "${TARGET_PLATFORM}" == windows* ]] && command -v makensis >/dev/null 2>&1; then
  NSIS_FLAG="-nsis"
fi

# 后端自身的版本信息：设置页「关于」读的是 internal/handler 里这几个变量，
# 不打进去就会显示 unknown（版本卡几乎空白）。与 main.version 保持同一份 VERSION。
BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
GIT_COMMIT="$(git -C "${repo_root}" rev-parse --short HEAD 2>/dev/null || echo unknown)"
# 工作区有未提交改动时标 -dirty：交付现场看到 cbdceba-dirty 就知道不是干净 tag 打的包。
# 注意这一步在 pushd cmd/desktop 之后，必须用 ${repo_root} 而不是相对路径。
if [[ "${GIT_COMMIT}" != "unknown" ]] && [[ -n "$(git -C "${repo_root}" status --porcelain 2>/dev/null | head -1)" ]]; then
  GIT_COMMIT="${GIT_COMMIT}-dirty"
fi
LDFLAGS="-X 'main.version=${VERSION}'"
LDFLAGS="${LDFLAGS} -X 'github.com/Tencent/WeKnora/internal/handler.Version=${VERSION}'"
LDFLAGS="${LDFLAGS} -X 'github.com/Tencent/WeKnora/internal/handler.CommitID=${GIT_COMMIT}'"
LDFLAGS="${LDFLAGS} -X 'github.com/Tencent/WeKnora/internal/handler.BuildTime=${BUILD_TIME}'"
LDFLAGS="${LDFLAGS} -X 'github.com/Tencent/WeKnora/internal/handler.GoVersion=$(go env GOVERSION 2>/dev/null || echo unknown)'"
if [[ -n "${UPDATE_BASE_URL}" ]]; then
  LDFLAGS="${LDFLAGS} -X 'main.updateBaseURL=${UPDATE_BASE_URL}'"
  echo "→ 更新通道：${UPDATE_BASE_URL}（/api/updates/latest）"
elif [[ -n "${UPDATE_REPO}" ]]; then
  LDFLAGS="${LDFLAGS} -X 'main.updateRepo=${UPDATE_REPO}'"
  echo "→ 自动更新源：${UPDATE_REPO}（GitHub，开发自测用）"
else
  echo "→ 自动更新：未配置（客户端只提示联系管理员）"
fi

# -m 跳过 wails 内部的 go mod tidy：本仓库把 GOMODCACHE 放在 <repo>/tools/ 下，
# 而 go mod tidy 会把 module cache 里的目录当成模块内的包来遍历，报
# "import path ... should not have @version"。go.mod 本身完整，无需 tidy。
# -skipembedcreate 关掉 wails 的 go:embed 目录扫描：它内部用
# golang.org/x/tools/go/packages 以 `./...` 加载整个工程做类型检查，既不看
# `-tags`（desktop/production 等被排除的文件会让包类型不完整），也不带目标平台
# 的 cgo 环境，在 Windows 上必然以
# `internal error: package "github.com/neo4j/neo4j-go-driver/v6/neo4j" without types
# was imported from ".../internal/container"` 收场。本仓库的 embed 目标
# （VERSION.embedded、frontend/dist）在构建前都已存在，不需要它创建。
# Windows 上把中文名传给 wails 的 -o 不可靠（Git-Bash → Go 参数编码），
# 所以先编成 ASCII 名，再在下面改名成品牌名。macOS/Linux 仍直接用品牌名。
# 诊断用：WIN_CONSOLE=1 时产出带控制台的 Windows 版（wails -windowsconsole）。
# GUI 子系统的 exe 在启动早期失败时没有任何输出，客户机只能看到「双击没反应」；
# 控制台版会把 panic/错误直接打印出来，是排查这类问题的第一手证据。
# 诊断开关也可以由仓库内的标记文件触发：CI 的 workflow 不便频繁改动，
# 需要一份「带控制台」的排错版本时，提交 scripts/.win-console 即可。
if [[ "${TARGET_PLATFORM}" == windows* && -f "scripts/win-console.flag" ]]; then
  WIN_CONSOLE=1
fi

CONSOLE_FLAG=""
OUT_SUFFIX=""
if [[ "${TARGET_PLATFORM}" == windows* && "${WIN_CONSOLE:-0}" == "1" ]]; then
  CONSOLE_FLAG="-windowsconsole"
  OUT_SUFFIX="-console"
  echo "→ 诊断构建：带控制台（-windowsconsole）"
fi

if [[ "${TARGET_PLATFORM}" == windows* ]]; then
  OUT_NAME="weknora-desktop-tmp${OUT_SUFFIX}"
else
  OUT_NAME="${APP_NAME}${OUT_SUFFIX}"
fi

wails build \
  -m \
  -clean \
  -skipbindings \
  -skipembedcreate \
  -tags "${BUILD_TAGS}" \
  -platform "${TARGET_PLATFORM}" \
  ${NSIS_FLAG:+"${NSIS_FLAG}"} \
  ${CONSOLE_FLAG:+"${CONSOLE_FLAG}"} \
  -ldflags="${LDFLAGS}" \
  -o "${OUT_NAME}"

popd >/dev/null

# 说明：wails 的 -o 是「完整文件名」，给了 -o 就不会自动补 .exe，
# 所以 Windows 上产物叫 build/bin/weknora-desktop-tmp（没有扩展名）。
# 这里统一改名成 ${APP_NAME}.exe —— package-windows.ps1 与 NSIS 都按这个名字找。
if [[ "${TARGET_PLATFORM}" == windows* ]]; then
  bin_dir="cmd/desktop/build/bin"
  src_bin="${bin_dir}/${OUT_NAME}"
  final_name="${APP_NAME}${OUT_SUFFIX}.exe"
  if [[ -f "${src_bin}" ]]; then
    mv -f "${src_bin}" "${bin_dir}/${final_name}"
    echo "→ 已重命名为 ${final_name}"
  elif [[ -f "${src_bin}.exe" ]]; then
    mv -f "${src_bin}.exe" "${bin_dir}/${final_name}"
    echo "→ 已重命名为 ${final_name}"
  else
    echo "✗ 未找到 ${src_bin}（也没有 .exe 变体）；bin 目录当前内容：" >&2
    ls -l "${bin_dir}" 2>/dev/null >&2 || echo "  （目录不存在）" >&2
  fi
fi

# ── 步骤 4：平台专属打包 ───────────────────────────────────────────────
echo "→ 平台打包（${TARGET_PLATFORM}）"
case "${TARGET_PLATFORM}" in
  darwin/*)
    bash scripts/package-macos.sh "${APP_NAME}" "${VERSION}"
    ;;
  linux/*)
    bash scripts/package-linux.sh "${APP_NAME}" "${VERSION}"
    ;;
  windows/*)
    if [[ -n "${OUT_SUFFIX}" ]]; then
      echo "→ 诊断构建：跳过安装包，只留 ${APP_NAME}${OUT_SUFFIX}.exe"
    elif command -v pwsh >/dev/null 2>&1; then
      pwsh -File scripts/package-windows.ps1 -AppName "${APP_NAME}" -Version "${VERSION}"
    else
      echo "⚠ 未检测到 pwsh，跳过 Windows 安装包（.exe 本体已生成）"
    fi
    ;;
  *)
    echo "⚠ 未知平台 ${TARGET_PLATFORM}，跳过打包"
    ;;
esac

# ── 步骤 5：产物清单 ───────────────────────────────────────────────────
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo " 构建完成，产物："
find cmd/desktop/build/bin -maxdepth 2 \( -name "*.app" -o -name "*.exe" -o -name "*.AppImage" -o -name "*.dmg" -o -name "*.deb" \) -print 2>/dev/null | sed 's/^/   /'
# 万一 find 什么都没匹配到，把目录本身打出来，便于定位产物去哪了
if ! find cmd/desktop/build/bin -maxdepth 2 -name "*.exe" -print -quit 2>/dev/null | grep -q .; then
  ls -l cmd/desktop/build/bin 2>/dev/null | sed 's/^/   /' || echo "   （cmd/desktop/build/bin 不存在）"
fi
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"