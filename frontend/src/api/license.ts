import { get, post } from '@/utils/request'

// 授权状态。字段名与后端 `licenseStatusResponse` 一一对应。
export type LicenseState = 'valid' | 'grace' | 'readonly' | 'missing' | 'invalid'

export interface LicenseStatus {
  enabled: boolean
  state: LicenseState
  reason?: string
  // 机器指纹：用户把它报给运维，运维据此发码。
  machine_fingerprint: string
  // 物理网卡出厂地址清单，授权页主展示这一组。
  physical_macs?: PhysicalMACInfo[]
  // 参与绑定校验的全部地址。通常是 physical_macs 的子集/超集，
  // 排查「机器不匹配」时看它。
  local_macs: string[]
  bound_macs?: string[]
  features: string[]
  all_features: string[]
  // 到期时间（Unix 秒）。0 表示永久。
  expires_at: number
  remaining_days: number
  serial?: string
  user?: string
  org?: string
  // key_id 是本机当前信任的公钥指纹；code_key_id 是已存授权码记录里记载的
  // 签发者指纹。两者不一致（且 code_key_id 非空）说明现有那张码是旧密钥
  // 签的——排「授权码无效」时先看这一对，能省掉大量猜测。
  key_id?: string
  code_key_id?: string
}

// PhysicalMACInfo 是一块物理网卡的出厂地址。
//
// 用三元组而不是裸字符串：用户看到 "fc:b2:14:ba:44:f0" 时无法判断
// 这是 Wi-Fi 还是有线网卡，而出现授权失效时，「是哪块网卡变了」恰恰
// 是最关键的信息。
export interface PhysicalMACInfo {
  device: string // 接口名，如 en0
  port: string // 硬件端口名，如 Wi-Fi
  mac: string // 出厂地址
}

// 功能位到「能否使用」的语义化键。
//
// 与后端 license.Feature 常量保持一致；用字符串而不是数字，
// 方便日志和 localStorage 里直接看懂。
export const LICENSE_FEATURES = ['kb', 'chat', 'ocr', 'office'] as const
export type LicenseFeature = (typeof LICENSE_FEATURES)[number]

// 功能位的中文名。放在这里而不是 i18n，是因为授权页只有一个，
// 而功能位名称需要与后端提示语里的叫法完全一致，避免两处漂移。
export const FEATURE_LABELS: Record<LicenseFeature, string> = {
  kb: '知识库',
  chat: '对话问答',
  ocr: 'OCR 识别',
  office: '文档生成',
}

// 请求超时要比常规接口长：授权校验读盘 + 枚举网卡，
// 在机械硬盘或网卡很多的老机器上偶发超过 5 秒。
const LICENSE_TIMEOUT = 15000

// getLicenseStatus 读取当前授权状态。
export async function getLicenseStatus(): Promise<LicenseStatus> {
  const res = await get<{ data: LicenseStatus }>('/api/v1/license', {
    timeout: LICENSE_TIMEOUT,
  })
  // 后端返回 { success, data }，这里取 data；缺失时给出一个明确的
  // 「未启用」兜底，避免上层每处都判 undefined。
  const payload = (res as any)?.data
  if (!payload) {
    return {
      enabled: false,
      state: 'valid',
      machine_fingerprint: '',
      physical_macs: [],
      local_macs: [],
      features: [...LICENSE_FEATURES],
      all_features: [...LICENSE_FEATURES],
      expires_at: 0,
      remaining_days: 0,
    }
  }
  return payload as LicenseStatus
}

// applyLicense 导入授权码。
//
// 失败时后端返回 403 + { error: { code, message } }，这里把 message
// 原样抛出——它已经是中文且区分了「不是本机」「已过期」「格式错」等
// 情形，前端不需要再翻译一遍（重写一次文案迟早会和后端不一致）。
export async function applyLicense(code: string): Promise<LicenseStatus> {
  const res = await post<{ data: LicenseStatus }>(
    '/api/v1/license',
    { code: code.trim() },
    { timeout: LICENSE_TIMEOUT },
  )
  return (res as any)?.data as LicenseStatus
}