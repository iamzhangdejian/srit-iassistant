import { get, post } from '@/utils/request'

export function dirFromPickerResponse(body: { data?: { dir?: unknown } } | null | undefined): string {
  const dir = body?.data?.dir
  return typeof dir === 'string' ? dir.trim() : ''
}

// Lite's SPA is reverse-proxied; Wails JS bindings are not injected. The
// folder picker is a same-process HTTP call that opens the native dialog.
export async function pickHostProjectDir(): Promise<string> {
  const res = await post<{ data?: { dir?: unknown } }>('/api/v1/system/host-project-dir', {}, { timeout: 0 })
  return dirFromPickerResponse(res)
}

// dirsFromListResponse 兼容后端返回 {"data":{"dirs":[...]}} 的形状，
// 缺字段时返回空数组而不是 undefined，调用方无需判空。
export function dirsFromListResponse(body: { data?: { dirs?: unknown } } | null | undefined): string[] {
  const dirs = body?.data?.dirs
  if (!Array.isArray(dirs)) {
    return []
  }
  return dirs.filter((item): item is string => typeof item === 'string' && item.trim() !== '')
}

// listHostProjectDirs 读取当前已被授权的目录列表。
// 用于向用户如实展示「沙箱现在能访问哪些目录」。
export async function listHostProjectDirs(): Promise<string[]> {
  const res = await get<{ data?: { dirs?: unknown } }>('/api/v1/system/project-dirs')
  return dirsFromListResponse(res)
}

// revokeHostProjectDir 收回某个目录的访问授权。
//
// 这一步是真正的「移除」：只清空界面上的选择并不会撤销后端白名单，
// 沙箱仍可读写该目录。必须调用此接口改写本地 prefs 才能收回权限。
export async function revokeHostProjectDir(dir: string): Promise<boolean> {
  const trimmed = dir.trim()
  if (!trimmed) {
    return false
  }
  const res = await post<{ data?: { revoked?: unknown } }>('/api/v1/system/project-dirs/revoke', { dir: trimmed })
  return res?.data?.revoked === true
}

// revealHostProjectDir 在系统文件管理器中打开并选中该目录。
//
// 为什么不能直接 window.open('file://…')：
//   1. cmd/desktop/main.go 注入的脚本把 window.open 改写成只处理 http/https，
//      file:// 会落到浏览器原生实现；
//   2. WKWebView 不允许 webview 导航到 file:// 路径，也没有实现对应的
//      UIDelegate 回调，于是点击后静默失败、连报错都没有。
// 所以这个动作交给后端进程以原生方式执行；后端只放行已授权的目录。
export async function revealHostProjectDir(dir: string): Promise<boolean> {
  const trimmed = dir.trim()
  if (!trimmed) {
    return false
  }
  const res = await post<{ data?: { revealed?: unknown } }>('/api/v1/system/project-dirs/reveal', { dir: trimmed })
  return res?.data?.revealed === true
}