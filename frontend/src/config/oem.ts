/**
 * =============================================================================
 * OEM 白标配置中心（全部可替换点位集中在此文件）
 * -----------------------------------------------------------------------------
 * 私有化交付时，只需要改这一个文件即可完成品牌替换：
 *   • BRAND.name        —— 产品名称（侧边栏、浏览器标题、登录页）
 *   • BRAND.logo        —— Logo 图片路径（放到 frontend/public/ 下）
 *   • BRAND.favicon     —— favicon 路径
 *   • BRAND.copyright   —— 页脚版权
 *   • OEM_MENU_WHITELIST—— 侧边栏保留哪些菜单（决定产品形态）
 *   • FEATURES         —— 功能开关（前后端保持一致，后端见 internal/router/router.go 的 FeatCut*）
 *
 * 与后端的对应关系：
 *   后端 internal/router/router.go 里的 FeatCut* 常量决定「接口是否存在」，
 *   本文件的 FEATURES 决定「界面是否展示」。两者必须一起改，
 *   否则会出现「界面上有入口但接口 404」或「接口还能调但界面已隐藏」。
 * =============================================================================
 */

/** 品牌信息（白标点位） */
export const BRAND = {
  /** 产品名称：显示在侧边栏 Logo 旁、浏览器标题、登录页 */
  name: '国研智研体',
  /** 产品简称：空间不足时的替代文案 */
  shortName: '智研体',
  /**
   * 侧栏等「横向狭窄」位置用的 Logo：应当是**纯图形、不含文字**的图标。
   *
   * 为什么强调纯图形：侧栏顶部要同时放 Logo、搜索、折叠三个按钮，横向空间只有
   * 200 出头。带文字的竖版素材（462x508 那种）被压进 34px 高之后宽度只剩 34px，
   * 文字完全糊掉——这正是旧版 Logo 又大又被裁的原因。
   * 因此这里指向纯图标，品牌名由旁边的文字标签负责（见模板 .logo-name）。
   */
  logo: '/oem/logo-icon.png',
  /** 完整版 Logo（含品牌名），用于登录页等有充裕空间的场景 */
  logoPng: '/oem/logo.png',
  /** 折叠态 Logo（仅图形）路径 */
  logoCollapsed: '/oem/logo-icon.png',
  /** favicon 路径 */
  favicon: '/favicon.ico',
  /** 版权信息，显示在侧边栏底部 */
  copyright: '© 2025 国研智研体',
  /** 登录页副标题 */
  loginSubtitle: '企业知识管理与智能问答',
  /** 产品官网（留空则不展示外链入口） */
  website: '',
  /**
   * Logo 的固有宽高比（宽 / 高）。
   *
   * 用途：图片没加载出来、或 CSS 还没应用时，用 aspect-ratio 按这个比例先把
   * 位置占住，避免侧栏顶部在加载瞬间从 32px 跳到 96px 再跳回来。
   * 换成自己的素材后如果比例不同，**这里必须一起改**，否则会有一帧形变。
   * 当前素材 logo.png 为 462x508（竖版：图标 + SRIT 字样），故约 0.91。
   */
  logoAspectRatio: '228 / 202',
} as const

/**
 * 侧边栏尺寸令牌（窄屏自适应用）。
 *
 * 为什么要放在这里而不是纯 CSS：侧栏的图标尺寸、行高、内边距分散在
 * menu.vue / TenantSelector.vue / 会话列表三处，纯 CSS 断点只能各改各的，
 * 很容易出现「图标缩了但行高没缩」的错位。这里给出三档密度，
 * menu.vue 通过 :style 把对应一档写成 CSS 变量，三处样式统一读变量。
 *
 * 取值依据：
 *   • compact  —— 视口 ≤ 900px 或侧栏宽 ≤ 232px：给对话区让出空间
 *   • regular  —— 默认
 *   • relaxed  —— 视口 ≥ 1600px：宽屏下适当放大，避免显得空旷
 */
export const SIDEBAR_DENSITY = {
  compact: {
    icon: 16,
    gap: 8,
    inset: 10,
    rowHeight: 32,
    logoHeight: 30,
    logoMaxWidth: 104,
    sectionGap: 12,
  },
  regular: {
    icon: 18,
    gap: 8,
    inset: 12,
    rowHeight: 34,
    logoHeight: 34,
    logoMaxWidth: 120,
    sectionGap: 16,
  },
  relaxed: {
    icon: 20,
    gap: 10,
    inset: 14,
    rowHeight: 38,
    logoHeight: 38,
    logoMaxWidth: 136,
    sectionGap: 20,
  },
} as const

export type SidebarDensityKey = keyof typeof SIDEBAR_DENSITY

/**
 * 根据视口宽度与侧栏宽度选密度档位（窄屏优先）。
 *
 * 判定顺序刻意如此：先看用户有没有把侧栏拖窄（这是主观意图），
 * 再看视口。否则用户把 260px 的侧栏拖到 220px 以腾出对话区，
 * 密度却仍停在 regular，图标和行高不跟着缩，等于白拖。
 */
export function pickSidebarDensity(viewportWidth: number, sidebarWidth: number): SidebarDensityKey {
  if (sidebarWidth <= 232) return 'compact'
  if (viewportWidth < 1024) return 'compact'
  if (viewportWidth >= 1600) return 'relaxed'
  return 'regular'
}

/**
 * 计算侧栏实际可用宽度。
 *
 * 问题背景：视口缩到 760px 时，侧栏仍是写死的 260px，对话区只剩 500px，
 * 正文被挤成一列窄字（实测截图里"Hi, 你可以问…"直接换行截断）。
 * 这里让侧栏宽度随视口收缩，但不低于 SIDEBAR_MIN_WIDTH，避免窄到放不下文案。
 *
 * 规则：
 *   • 视口 ≥ 1024px  —— 用用户拖拽的宽度（原行为，不打扰）
 *   • 视口 < 1024px  —— 按视口的 26% 收缩，夹在 [MIN, 用户宽度] 之间
 *   • 视口 < 720px   —— 进一步压到 200px，把空间让给对话
 */
export function resolveSidebarWidth(viewportWidth: number, preferredWidth: number, minWidth = 220): number {
  if (viewportWidth >= 1024) return preferredWidth
  const scaled = Math.round(viewportWidth * 0.26)
  const floor = viewportWidth < 720 ? 200 : minWidth
  return Math.max(floor, Math.min(preferredWidth, scaled))
}

/** 功能开关 */
export const FEATURES = {
  /** 模型管理 */
  models: true,
  /** 知识库 + Wiki */
  knowledge: true,
  /** 插件市场 */
  plugins: true,
  /** 对话问答 */
  chat: true,

  // ---- 以下为关闭项，界面上不出现任何入口 ----
  /** 智能体 / ReAct Agent / 编排 */
  agents: false,
  /** 自动化 / 多步骤工作流 */
  automations: false,
  /** 知识图谱 */
  graph: false,
  /** 第三方数据源同步 */
  dataSource: false,
  /** MCP */
  mcp: false,
  /** 浏览器自动化 */
  browserSkill: false,
  /** 网页搜索（联网搜索）。对话输入框有开关，默认关闭，需要先配置搜索引擎实例。 */
  webSearch: true,
  /** 组织 / 邀请成员 */
  organization: false,
  /** 审计报表 */
  auditReport: false,
  /** 通知 */
  notification: false,
  /** 记忆模块 */
  memory: false,
} as const

/**
 * 侧边栏菜单白名单：只有列在这里的菜单项会被渲染。
 * 顺序即展示顺序。
 *
 * 对应关系：
 *   'creatChat'       -> 对话问答
 *   'knowledge-bases' -> 知识库（含 Wiki）
 *   'toolbox'         -> 插件市场
 *   'settings'        -> 模型管理（复用系统设置页，仅保留模型相关 Tab）
 *   'logout'          -> 退出登录（固定在底部，不参与排序）
 */
export const OEM_MENU_WHITELIST: string[] = [
  'creatChat',
  'knowledge-bases',
  'toolbox',
  'settings',
]

/** 固定在侧边栏底部的菜单项 */
/**
 * 底部菜单：OEM 交付形态没有账号体系（授权码即凭证），
 * 侧栏底部不再有用户头像 / 个人设置 / 退出登录，故为空。
 */
export const OEM_MENU_BOTTOM: string[] = []

/**
 * 实际生效的完整白名单（顶部菜单 + 底部菜单）。
 * 侧边栏渲染时会用这个集合过滤，因此 logout 必须包含在内，
 * 否则「退出登录」按钮会一起消失。
 */
export const OEM_MENU_ALL: string[] = [...OEM_MENU_WHITELIST, ...OEM_MENU_BOTTOM]

/**
 * 侧边栏菜单自定义标题（覆盖 i18n 默认文案）。
 * 键为菜单 path，值为要显示的中文标题。
 * 留空则使用 i18n 里的默认文案。
 */
export const OEM_MENU_TITLES: Record<string, string> = {
  creatChat: '对话问答',
  'knowledge-bases': '知识库',
  toolbox: '插件市场',
  // 左侧主菜单入口显示为「系统设置」，进入后仍是设置页的模型管理分区。
  settings: '系统设置',
}

// 这里曾有一份 OEM_SETTINGS_ALLOWED_TABS（'models'/'model-settings'/'modelCredentials'），
// 全仓库从未引用、key 也对不上真实分区；真正的设置页白名单在
// frontend/src/views/settings/Settings.vue 的 OEM_SETTINGS_SECTIONS 里，已于 v3.22 删除以免误导。

/**
 * 沙箱面板允许的 Tab。
 * 保留「产物」与「项目文件」；「桌面」与「交互终端」均已移除。
 *
 * 「项目文件」读的是本项目目录（host_dir）下的真实文件，与「产物」的
 * 区别是：产物只列对话生成并登记过的文件，项目文件列目录里实际存在的
 * 全部文件（含用户手动放进来的）。
 *
 * 终端 Tab 移除原因：它连的不是本机 shell，而是会话绑定沙箱（Cube/E2B
 * 微虚拟机）里的 PTY。Lite 版没有沙箱配置、智能体路由也被裁剪，用户点
 * 「创建并启动」必然落到 SANDBOX_NOT_BOUND，提示语还误导人「请切换到
 * 配置了沙箱的智能体」。露半截不如不露。
 */
export const OEM_SANDBOX_TABS: Array<'artifacts' | 'projectFiles' | 'terminal' | 'desktop'> = [
  'artifacts',
  'projectFiles',
]

/** 首页路由：登录后默认进入的页面 */
export const OEM_HOME_ROUTE = '/platform/creatChat'

/** 登录后跳转 */
export const OEM_AFTER_LOGIN_ROUTE = '/platform/creatChat'

/**
 * 功能位到侧边栏菜单的映射。
 *
 * 用途：授权里没有某个功能位时，把对应菜单从侧栏移除。
 *
 * 为什么菜单要跟着功能位走：后端已经用 403 拦住了接口，但如果界面上
 * 还留着入口，用户点进去只会看到一堆报错，第一反应是「软件坏了」
 * 而不是「我没有买这个功能」。隐藏入口 + 授权页写明已开通功能，
 * 才是让人看得懂的表达。
 *
 * 没有列在这里的菜单（如 settings / logout）不受功能位影响——
 * 设置页必须始终可达，否则用户没法换模型、也没法退出。
 */
export const OEM_MENU_FEATURE: Record<string, 'kb' | 'chat' | 'ocr' | 'office'> = {
  'knowledge-bases': 'kb',
  creatChat: 'chat',
  toolbox: 'office',
}
