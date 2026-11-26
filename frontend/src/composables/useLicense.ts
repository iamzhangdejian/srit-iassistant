import { ref, computed } from 'vue'
import {
  applyLicense,
  getLicenseStatus,
  LICENSE_FEATURES,
  type LicenseFeature,
  type LicenseState,
  type LicenseStatus,
} from '@/api/license'

// 授权状态的全局单例。
//
// 为什么不用 Pinia store：授权状态在应用生命周期里几乎是静态的
// （只有「导入新码」和「自然到期」两种变化），不需要 devtools、
// 不需要持久化、也不需要在 SSR 下序列化。用一个模块级 ref 更轻，
// 也避免了 store 初始化顺序问题（路由守卫比 store 先执行）。
//
// 注意：这里不缓存到 localStorage。授权是机器级事实，缓存在浏览器
// 里既不能加速（后端判定本身是一次内存读），又会在换机器、清缓存
// 等场景下给出错误答案。

const status = ref<LicenseStatus | null>(null)
const loading = ref(false)
let inflight: Promise<LicenseStatus> | null = null

// 是否已经拉取过（用于区分「还没查」与「查过且未启用」）。
const loaded = ref(false)

// ensureLicenseStatus 保证只拉一次。
//
// 并发调用共享同一个 Promise：应用启动时路由守卫、App 初始化、
// 聊天页可能同时问一次，串行化后只打一个请求。
export async function ensureLicenseStatus(force = false): Promise<LicenseStatus> {
  if (!force && loaded.value && status.value) return status.value
  if (!force && inflight) return inflight

  loading.value = true
  inflight = getLicenseStatus()
    .then((value) => {
      status.value = value
      loaded.value = true
      return value
    })
    .finally(() => {
      loading.value = false
      inflight = null
    })
  return inflight
}

// refreshLicenseStatus 在导入授权码后强制刷新。
export function refreshLicenseStatus(): Promise<LicenseStatus> {
  return ensureLicenseStatus(true)
}

// 供导入后直接写入，省一次往返。
export function setLicenseStatus(next: LicenseStatus): void {
  status.value = next
  loaded.value = true
}

// bindLicense 在登录页里完成「绑定授权码」这一步。
//
// 单独包一层而不是让页面直接调 applyLicense：绑定成功后必须同步更新
// 全局单例，否则路由守卫手里的还是旧状态，用户会在登录页和主页之间
// 来回弹。顺带把报文里的 reason 提成异常信息，页面不用理解后端结构。
export async function bindLicense(code: string): Promise<LicenseStatus> {
  const trimmed = code.trim()
  if (!trimmed) {
    throw new Error('请粘贴授权码')
  }
  const next = await applyLicense(trimmed)
  setLicenseStatus(next)
  return next
}

export function useLicense() {
  const state = computed<LicenseState>(() => status.value?.state ?? 'valid')
  const enabled = computed(() => status.value?.enabled === true)

  // 授权闸门开着，且当前状态不足以正常使用。
  //
  // readonly 不算「被挡住」：那是过期降级，用户仍应能进系统看资料，
  // 只是生成类功能不可用（由 canUse 逐项判断）。grace 是宽限期，同理。
  //
  // 判定写成「黑名单」而不是「白名单」：新增一个状态时，默认行为应当
  // 是放行，否则会出现「后端加了状态、前端把所有人挡在门外」的事故。
  const blocked = computed(
    () =>
      enabled.value &&
      state.value !== 'valid' &&
      state.value !== 'grace' &&
      state.value !== 'readonly',
  )

  const needsRenewal = computed(() => enabled.value && (state.value === 'grace' || state.value === 'readonly'))

  // canUse 判断某个功能位当前是否可用。
  //
  // 闸门关闭时一律为 true——这样业务代码不需要到处写
  // `if (!licenseEnabled || ...)`。
  function canUse(feature: LicenseFeature): boolean {
    if (!enabled.value) return true
    return (status.value?.features ?? []).includes(feature)
  }

  // 剩余天数：永久授权返回 -1，便于 UI 区分。
  const remainingDays = computed(() => {
    const s = status.value
    if (!s) return 0
    if (s.expires_at === 0) return -1
    return s.remaining_days
  })

  const allFeatures = computed<LicenseFeature[]>(
    () => (status.value?.all_features as LicenseFeature[]) ?? [...LICENSE_FEATURES],
  )

  return {
    status,
    loading,
    loaded,
    state,
    enabled,
    blocked,
    needsRenewal,
    remainingDays,
    allFeatures,
    canUse,
  }
}