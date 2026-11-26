// 桌面端「唯一入口 = 授权码」之后，会话不再由用户输入获得：
// 授权码通过校验后，前端用桌面桥（Wails）提供的一次性令牌静默换取
// JWT 会话。这个模块把这些步骤收在一处，路由守卫与登录页共用，
// 避免两边各写一遍导致「守卫说没登录、页面说能进」。
import { autoSetup, userInfoFromApi } from '@/api/auth'
import { useAuthStore } from '@/stores/auth'

type AuthStore = ReturnType<typeof useAuthStore>

/** 把 auto-setup / 登录响应落进 store（user、token、tenant、memberships）。 */
export function persistLoginResponse(authStore: AuthStore, response: any) {
  const activeTenant = response.active_tenant || response.tenant
  if (response.user && response.token) {
    const homeTenantId = response.user.tenant_id ?? activeTenant?.id ?? ''
    authStore.setUser(userInfoFromApi(response.user, homeTenantId))
    authStore.setToken(response.token)
    if (response.refresh_token) {
      authStore.setRefreshToken(response.refresh_token)
    }
    if (activeTenant) {
      authStore.setTenant({
        id: String(activeTenant.id) || '',
        name: activeTenant.name || '',
        owner_id: response.user.id || '',
        created_at: activeTenant.created_at || new Date().toISOString(),
        updated_at: activeTenant.updated_at || new Date().toISOString(),
      })
    } else {
      authStore.setTenant(null)
    }
    if (Array.isArray(response.memberships)) {
      authStore.setMemberships(response.memberships)
    }
  }
}

/** 用 localStorage 里的 token 复原会话（硬刷新、重启 WebView 都会走这里）。 */
export async function hydrateSessionFromToken(authStore: AuthStore): Promise<boolean> {
  const token = localStorage.getItem('weknora_token')
  if (!token) return false

  if (!authStore.token) {
    authStore.setToken(token)
  }

  const storedRefreshToken = localStorage.getItem('weknora_refresh_token')
  if (storedRefreshToken && !authStore.refreshToken) {
    authStore.setRefreshToken(storedRefreshToken)
  }

  // /auth/me 的落库逻辑只在 auth store 里维护一份，这里只负责把 token
  // 放进 store，请求本身与启动、侧栏共用去重。
  return authStore.refreshFromAuthMe()
}

/**
 * 保证有一个可用的桌面会话。
 *
 * 顺序：已有会话 → 用存量 token 复原 → 桌面桥静默建号。
 * 三条都不成立时返回 false（浏览器里直接开前端、或打包漏了
 * public.key / 桌面桥时），由调用方决定如何提示。
 */
export async function ensureDesktopSession(): Promise<boolean> {
  const authStore = useAuthStore()

  if (authStore.isLoggedIn && authStore.hasValidTenant) return true

  if (authStore.isLoggedIn && !authStore.hasValidTenant) {
    // 会话在，但空间还没落定：让调用方送去 onboarding，不要重复建号。
    return true
  }

  const restored = await hydrateSessionFromToken(authStore)
  if (restored) return true

  try {
    const response = await autoSetup()
    if (response?.success && response.token) {
      persistLoginResponse(authStore, response)
      authStore.setLiteMode(true)
      return true
    }
  } catch {
    // 浏览器形态没有 Go 桥，autoSetup 会抛错；交给调用方提示。
  }
  return false
}