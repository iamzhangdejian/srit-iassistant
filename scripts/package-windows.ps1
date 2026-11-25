# Windows 打包脚本：生成 NSIS 安装包
# ---------------------------------------------------------------------------
# 前置条件：
#   1. NSIS：choco install nsis -y  或从 https://nsis.sourceforge.io 下载
#   2. WebView2 Runtime：Windows 10 1809+ 与 Windows 11 已内置；
#      更老的系统需随包附带引导安装器（见下方 WEBVIEW2_BOOTSTRAP）
#
# 说明：Wails 的 `wails build -nsis` 已能产出安装包，本脚本在它之上补齐
#      运行资源（config/migrations/.env）并统一品牌与版本号。
#
# 用法：
#   pwsh -File scripts/package-windows.ps1 -AppName "国研智研体" -Version "0.9.0"
#
# -StageOnly：只组装安装目录（含 public.key / 许可清单），不调用 makensis。
#              用于在非 Windows 机器上校验资源注入是否完整。
# ---------------------------------------------------------------------------
param(
    [string]$AppName = "国研智研体",
    [string]$Version = "0.9.0",
    [switch]$StageOnly
)

$ErrorActionPreference = "Stop"
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
Set-Location $repoRoot

Write-Host "→ 检查 NSIS 是否可用"
if (-not $StageOnly -and -not (Get-Command makensis -ErrorAction SilentlyContinue)) {
    Write-Warning "未检测到 makensis。安装：choco install nsis -y"
    Write-Host "  .exe 本体已生成，只是没有安装包。"
    exit 0
}

$exe = Join-Path $repoRoot "cmd\desktop\build\bin\$AppName.exe"
$placeholderExe = $false
if (-not (Test-Path $exe) -and $StageOnly) {
    # 非 Windows 主机也能校验资源注入：空文件占位，结束后删除
    New-Item -ItemType Directory -Path (Split-Path $exe) -Force | Out-Null
    Set-Content -Path $exe -Value "" -NoNewline
    $placeholderExe = $true
    Write-Host "  （StageOnly：$exe 不存在，用空占位文件校验资源注入）"
}
if (-not (Test-Path $exe)) {
    Write-Error "未找到 $exe，请先执行 oem-build-lite.sh"
}

# ── 1. 组装安装目录 ────────────────────────────────────────────────────
$stage = Join-Path $repoRoot "cmd\desktop\build\windows_staging"
if (Test-Path $stage) { Remove-Item $stage -Recurse -Force }
New-Item -ItemType Directory -Path $stage | Out-Null

Copy-Item $exe (Join-Path $stage "$AppName.exe")

# 运行资源：与 macOS/Linux 同理，缺了会启动失败
foreach ($dir in @("config", "migrations\sqlite")) {
    $src = Join-Path $repoRoot $dir
    if (Test-Path $src) {
        $dst = Join-Path $stage $dir
        New-Item -ItemType Directory -Path $dst -Force | Out-Null
        Copy-Item "$src\*" $dst -Recurse -Force
    }
}
$envExample = Join-Path $repoRoot ".env.lite.example"
if (Test-Path $envExample) {
    Copy-Item $envExample (Join-Path $stage ".env")
}

# 授权公钥：与 macOS/Linux 同一套查找顺序
$keyCandidates = @()
if ($env:LICADMIN_PUBLIC_KEY) { $keyCandidates += $env:LICADMIN_PUBLIC_KEY }
# 发码端（licadmin）在 macOS 上写 ~/Library/...，在 Windows 上写 %APPDATA%\...；
# 两边都列出来，这样在任一平台打包都能自动取到公钥。
if ($env:APPDATA) {
    $keyCandidates += (Join-Path $env:APPDATA "国研智研体授权管理/licadmin-data/public.key")
}
if ($env:LOCALAPPDATA) {
    $keyCandidates += (Join-Path $env:LOCALAPPDATA "国研智研体授权管理/licadmin-data/public.key")
}
$keyCandidates += @(
    (Join-Path $HOME "Library/Application Support/国研智研体授权管理/licadmin-data/public.key"),
    (Join-Path $repoRoot "licadmin-data/public.key"),
    (Join-Path $repoRoot "config/license/public.key")
)
$keySrc = $keyCandidates | Where-Object { Test-Path $_ } | Select-Object -First 1
if ($keySrc) {
    Copy-Item $keySrc (Join-Path $stage "public.key")
    Write-Host "  ✓ 已嵌入授权公钥"
} else {
    Write-Warning "未找到授权公钥，本次打包的客户端不会启用授权闸门（内部构建正常现象）"
    Write-Host "    正式发行前请先启动授权管理端生成密钥，或设置 LICADMIN_PUBLIC_KEY=<路径>"
}

# 版本号与许可清单
foreach ($f in @("VERSION", "LICENSE", "THIRD_PARTY_NOTICES.md")) {
    $src = Join-Path $repoRoot $f
    if (Test-Path $src) { Copy-Item $src (Join-Path $stage $f) }
}
$licDir = Join-Path $repoRoot "licenses"
if (Test-Path $licDir) {
    Copy-Item $licDir (Join-Path $stage "licenses") -Recurse -Force
}

# 技能包：管理端导出的 skills-bundle.zip → <安装目录>/skills/<name>/
# 客户端启动时按版本装进 ~/.weknora/skills（见 cmd/desktop/skills_boot.go）。
$skillsBundle = $env:SKILLS_BUNDLE
if (-not $skillsBundle -and (Test-Path (Join-Path $repoRoot "skills-bundle.zip"))) {
    $skillsBundle = Join-Path $repoRoot "skills-bundle.zip"
}
if ($skillsBundle) {
    if (-not (Test-Path $skillsBundle)) {
        Write-Error "指定的技能包不存在：$skillsBundle"
        exit 1
    }
    $skillsDir = Join-Path $stage "skills"
    if (Test-Path $skillsDir) { Remove-Item $skillsDir -Recurse -Force }
    Expand-Archive -Path $skillsBundle -DestinationPath $skillsDir -Force
    # 清单是给发版核对用的元数据，运行时用不到。
    Remove-Item (Join-Path $skillsDir ".manifest.json") -Force -ErrorAction SilentlyContinue
    $count = (Get-ChildItem $skillsDir -Directory).Count
    Write-Host "  ✓ 已注入技能包（$count 个技能）"
}

# 前端静态资源：Gin 需要 WEKNORA_WEB_DIR 指向含 index.html 的目录，否则窗口打开即 401
# 安装程序与快捷方式用同一个图标（wails build 会在 build/windows 下生成 icon.ico）
$iconSrc = Join-Path $repoRoot "cmd\desktop\build\windows\icon.ico"
$hasIcon = Test-Path $iconSrc
if ($hasIcon) {
    Copy-Item $iconSrc (Join-Path $stage "app.ico")
} else {
    Write-Warning "未找到 cmd\desktop\build\windows\icon.ico，安装包将使用 NSIS 默认图标"
}

# WebView2 引导安装器（可选）：放到仓库根或设 WEBVIEW2_BOOTSTRAP 指向它，
# 缺运行时的老系统会静默装上，而不是装完打不开。
$webview2 = $env:WEBVIEW2_BOOTSTRAP
if (-not $webview2 -and (Test-Path (Join-Path $repoRoot "MicrosoftEdgeWebview2Setup.exe"))) {
    $webview2 = Join-Path $repoRoot "MicrosoftEdgeWebview2Setup.exe"
}
if ($webview2 -and (Test-Path $webview2)) {
    Copy-Item $webview2 (Join-Path $stage "MicrosoftEdgeWebview2Setup.exe")
    Write-Host "  ✓ 已内置 WebView2 引导安装器"
}

# 兜底：万一静态链接没生效（换成别的工具链时会发生），把 MinGW 运行时 DLL
# 一起装进安装目录。缺了它们，exe 双击只会弹「找不到 libstdc++-6.dll」。
$mingwBin = $null
$mingwCandidates = @()
if ($env:MINGW_PREFIX) { $mingwCandidates += (Join-Path $env:MINGW_PREFIX "bin") }
# setup-msys2 / UCRT64 终端会把 ucrt64\bin 加进 PATH，但安装位置可能是
# D:\a\_temp\msys64（不能假设 C:\msys64），所以直接扫 PATH。
foreach ($entry in ($env:PATH -split ';')) {
    if ($entry -and (Test-Path (Join-Path $entry "libstdc++-6.dll"))) { $mingwCandidates += $entry }
}
$gccCmd = Get-Command gcc -ErrorAction SilentlyContinue
if ($gccCmd) { $mingwCandidates += (Split-Path $gccCmd.Source -Parent) }
$mingwBin = $mingwCandidates | Where-Object { $_ -and (Test-Path (Join-Path $_ "libstdc++-6.dll")) } | Select-Object -First 1
if ($mingwBin) {
    foreach ($dll in @("libstdc++-6.dll", "libgcc_s_seh-1.dll", "libwinpthread-1.dll")) {
        $dllSrc = Join-Path $mingwBin $dll
        if (Test-Path $dllSrc) {
            Copy-Item $dllSrc (Join-Path $stage $dll)
            Write-Host "  ✓ 附带 MinGW 运行时：$dll"
        }
    }
} else {
    Write-Warning "未定位到 MinGW 运行时目录，若 exe 仍依赖 libstdc++-6.dll，客户机会启动失败"
}

$webSrc = Join-Path $repoRoot "frontend\dist"
if (Test-Path (Join-Path $webSrc "index.html")) {
    Copy-Item $webSrc (Join-Path $stage "web") -Recurse -Force
    Write-Host "  ✓ 已注入前端静态资源"
} else {
    Write-Error "未找到 frontend\dist，请先在 frontend\ 执行 vite build"
}

if ($StageOnly) {
    Write-Host "✓ StageOnly：已组装 $stage"
    Get-ChildItem $stage | Select-Object -ExpandProperty Name
    if ($placeholderExe) { Remove-Item $exe -Force }
    exit 0
}

# 产物名必须能被 cmd/desktop/update.go 的 findBestAsset 认出来：
# 它按 "win" + "amd64" 匹配可执行文件，早先叫 -windows-x64-Setup.exe 会匹配失败。
$outFile = "${AppName}-${Version}-windows-amd64-Setup.exe"
$outName = "cmd\desktop\build\bin\$outFile"
$outAbs  = Join-Path $repoRoot $outName

# ── 2. 生成 NSIS 脚本 ──────────────────────────────────────────────────
# 版本号要四段才能进 VIProductVersion（0.9.0 → 0.9.0.0）
$verParts = ($Version -split '[.\-+]') | Where-Object { $_ -match '^\d+$' }
while ($verParts.Count -lt 4) { $verParts += "0" }
$verQuad = ($verParts[0..3]) -join "."

$iconDefine = ''
$iconLine = ''
if ($hasIcon) {
    $iconDefine = '!define MUI_ICON "app.ico"' + "`n" + '!define MUI_UNICON "app.ico"'
    $iconLine = '  Icon "app.ico"' + "`n"
}

$nsi = @"
!include "MUI2.nsh"
!include "x64.nsh"
!include "FileFunc.nsh"

; ── 版本资源：在「应用和功能」里显示版本与厂商，而不是空白 ─────────
VIProductVersion "${verQuad}"
VIAddVersionKey /LANG=2052 "ProductName" "$AppName"
VIAddVersionKey /LANG=2052 "CompanyName" "国研智研体"
VIAddVersionKey /LANG=2052 "FileDescription" "$AppName 安装程序"
VIAddVersionKey /LANG=2052 "FileVersion" "$Version"
VIAddVersionKey /LANG=2052 "ProductVersion" "$Version"
VIAddVersionKey /LANG=2052 "LegalCopyright" "© 2025 国研智研体"

Name "$AppName"
OutFile "$outAbs"
InstallDir "`$PROGRAMFILES64\$AppName"
; 记住上次安装目录，覆盖安装时不会再问一遍
InstallDirRegKey HKLM "Software\$AppName" "InstallDir"
RequestExecutionLevel admin
Unicode true
SetCompressor /SOLID lzma
${iconDefine}

!define MUI_ABORTWARNING
!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "SimpChinese"
!insertmacro MUI_LANGUAGE "English"

Section "Install"
  SetShellVarContext all
  SetOutPath "`$INSTDIR"
  File /r "*.*"

  WriteRegStr HKLM "Software\$AppName" "InstallDir" "`$INSTDIR"
  WriteUninstaller "`$INSTDIR\uninstall.exe"

  CreateDirectory "`$SMPROGRAMS\$AppName"
  CreateShortcut "`$SMPROGRAMS\$AppName\$AppName.lnk" "`$INSTDIR\$AppName.exe" "" "" "" "" "" "${AppName}"
  CreateShortcut "`$DESKTOP\$AppName.lnk" "`$INSTDIR\$AppName.exe" "" "" "" "" "" "${AppName}"

  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\$AppName" "DisplayName" "$AppName"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\$AppName" "DisplayVersion" "$Version"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\$AppName" "DisplayIcon" "`$INSTDIR\$AppName.exe"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\$AppName" "Publisher" "国研智研体"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\$AppName" "InstallLocation" "`$INSTDIR"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\$AppName" "UninstallString" '"$INSTDIR\uninstall.exe"'
  WriteRegDWORD HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\$AppName" "NoModify" 1
  WriteRegDWORD HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\$AppName" "NoRepair" 1
SectionEnd

; ── WebView2 运行时：Wails 的前端容器，缺了整个窗口打不开 ───────────
; Windows 11 与 Win10 1809+ 自带；企业精简镜像可能被裁掉。
; 带 bootstrap 就静默安装，没带就提示去下载，安装本身继续。
Section "WebView2"
  ReadRegStr `$0 HKLM "SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}" "pv"
  StrCmp `$0 "" 0 webview2_done
  ReadRegStr `$0 HKCU "SOFTWARE\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}" "pv"
  StrCmp `$0 "" 0 webview2_done
  IfFileExists "`$INSTDIR\MicrosoftEdgeWebview2Setup.exe" 0 webview2_missing
    DetailPrint "正在安装 Microsoft Edge WebView2 运行时…"
    ExecWait '"$INSTDIR\MicrosoftEdgeWebview2Setup.exe" /silent /install'
    Goto webview2_done
  webview2_missing:
    IfSilent webview2_done
    MessageBox MB_ICONEXCLAMATION|MB_OK "未检测到 Microsoft Edge WebView2 运行时，应用可能无法启动。请安装后重试：https://go.microsoft.com/fwlink/p/?LinkId=2124703"
  webview2_done:
SectionEnd

Section "Uninstall"
  SetShellVarContext all
  Delete "`$SMPROGRAMS\$AppName\$AppName.lnk"
  RMDir "`$SMPROGRAMS\$AppName"
  Delete "`$DESKTOP\$AppName.lnk"
  DeleteRegKey HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\$AppName"
  DeleteRegKey HKLM "Software\$AppName"

  ; 用户数据默认保留：知识库、会话、授权码都在里面，误删代价远大于残留。
  IfSilent keep_userdata
  MessageBox MB_YESNO|MB_ICONQUESTION "是否同时删除本机数据（知识库、对话记录、已导入的授权码）？$\r$\n选择「否」将保留，重新安装后可直接继续使用。" IDNO keep_userdata
  RMDir /r "`$APPDATA\$AppName"
  keep_userdata:
  RMDir /r "`$INSTDIR"
SectionEnd
"@

$nsiPath = Join-Path $stage "installer.nsi"
Set-Content -Path $nsiPath -Value $nsi -Encoding UTF8

# ── 3. 编译安装包 ──────────────────────────────────────────────────────
Write-Host "→ 编译 NSIS 安装包"
Push-Location $stage
# OutFile 在 NSI 里已写成绝对路径（$outAbs），makensis 不受当前目录影响；
# 早先用相对路径导致产物落到 staging 目录、随后被 Remove-Item 一起删掉。
$outDir = Split-Path $outAbs -Parent
if (-not (Test-Path $outDir)) { New-Item -ItemType Directory -Path $outDir -Force | Out-Null }
& makensis $nsiPath
$makensisExit = $LASTEXITCODE
Pop-Location

Remove-Item $stage -Recurse -Force
$out = $outAbs

if ($makensisExit -ne 0) {
    Write-Error "makensis 失败（退出码 $makensisExit）"
    exit $makensisExit
}
if (Test-Path $out) {
    $size = [math]::Round((Get-Item $out).Length / 1MB, 1)
    Write-Host "✓ Windows 打包完成：$out（$size MB）"
    Write-Host "  静默安装：$([System.IO.Path]::GetFileName($out)) /S"
} else {
    Write-Warning "安装包未生成，请检查 makensis 输出"
}
