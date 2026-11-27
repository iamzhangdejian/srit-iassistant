export function getApiBaseUrl(): string {
  // LocalHub plugin patch (2026-04-29): respect vite's BASE_URL so that
  // axios calls work at `/app/weknora/` (LocalHub reverse proxy). Without
  // this · axios hits `/api/v1/...` at LocalHub root · gets 404 "Cannot
  // POST". Strip trailing slash so axios doesn't produce `/app/weknora//api/v1/...`.
  // See: plugins/weknora/patches/api-base-baseurl.patch
  const base = (import.meta.env?.BASE_URL || '/').replace(/\/+$/, '');
  return base;
}

/**
 * 桌面端注入的 API 绝对地址（由 Wails OnDomReady 写入，形如
 * http://127.0.0.1:8740/api/v1）。非桌面环境或尚未注入时返回 null。
 */
export function getDesktopApiOrigin(): string | null {
  if (typeof window === 'undefined') return null
  const raw = (window as unknown as { __WEKNORA_API_BASE__?: string }).__WEKNORA_API_BASE__
  if (typeof raw !== 'string' || raw === '') return null
  try {
    return new URL(raw).origin
  } catch {
    return null
  }
}

/**
 * API 传输层基地址：桌面端直连本机 HTTP（Wails 自定义协议的 POST 请求体
 * 在 WebKit < 2.40 上会丢失），浏览器端沿用相对路径。
 */
export function getApiTransportBase(): string {
  return getDesktopApiOrigin() ?? getApiBaseUrl()
}