/**
 * 桌面端「交给系统处理」的两个动作。
 *
 * 为什么要走 Wails 绑定而不是 `window.open` / `<a download>`：
 * - `cmd/desktop/main.go` 注入的脚本把 `window.open` 限制在 http/https，
 *   `file://` 会被丢掉；
 * - WKWebView 不允许 webview 自己导航到 file://，也没有实现 UIDelegate
 *   的相应回调，所以前端「打开本地文件」只能由 Go 侧起原生进程。
 *
 * 安全边界在 Go 侧：`resolveApprovedPath` 要求路径落在用户授权过的项目
 * 目录内（软链接会被解析），前端传什么都只是请求，不是权限。
 */
type DesktopFilesWindow = Window & {
  go?: {
    main?: {
      App?: {
        OpenInSystemApp?: (path: string) => Promise<void> | void
        RevealInFinder?: (path: string) => Promise<void> | void
      }
    }
  }
}

function desktopApp() {
  if (typeof window === 'undefined') return undefined
  return (window as DesktopFilesWindow).go?.main?.App
}

/** 当前是否运行在桌面壳里（浏览器打开时为 false）。 */
export function hasDesktopFileBridge(): boolean {
  const app = desktopApp()
  return typeof app?.OpenInSystemApp === 'function' || typeof app?.RevealInFinder === 'function'
}

/** 用系统默认应用打开一个已授权项目目录内的文件。 */
export async function openInSystemApp(path: string): Promise<void> {
  const fn = desktopApp()?.OpenInSystemApp
  if (typeof fn !== 'function') {
    throw new Error('仅桌面版支持用系统应用打开')
  }
  await fn(path)
}

/** 在系统文件管理器中选中一个已授权项目目录内的文件。 */
export async function revealInFileManager(path: string): Promise<void> {
  const fn = desktopApp()?.RevealInFinder
  if (typeof fn !== 'function') {
    throw new Error('仅桌面版支持在文件管理器中显示')
  }
  await fn(path)
}