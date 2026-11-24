<template>
  <!--
    Teleport 开关说明（推挤模式的关键）：
      • modal  —— 传送到 body，脱离布局树，作为全屏浮层（原行为）
      • drawer —— 不传送，原地渲染成父容器（.main）的一个 flex 子项，
                  占掉真实宽度，主内容区被挤窄但完全可见（推挤而非遮挡）
    这是复用 AssistantPanel 已验证的模式（其 docked 态同样禁用 Teleport）。
  -->
  <Teleport to="body" :disabled="variant === 'drawer'">
    <Transition name="settings-modal-shell">
      <!--
        variant 决定容器形态（2024 改造）：
          • 'modal'  —— 默认，居中浮层 + 半透明遮罩，覆盖整个页面
          • 'drawer' —— 右侧抽屉，贴右侧边缘、不遮挡左侧导航与会话列表
        仅替换外层容器，内部 settings-container（左侧分区导航 + 右侧内容）
        与所有业务逻辑、路由、权限判断完全不变。
      -->
      <div v-if="visible" class="settings-modal-shell" :class="[variant === 'modal' ? 'settings-overlay' : 'settings-drawer-host', overlayClass]"
        :style="{ zIndex }" :data-variant="variant" @click.self="emit('close')">
        <div class="settings-modal" role="dialog" aria-modal="true" :aria-label="title">
          <div v-if="loading" class="editor-initializing" role="status" :aria-label="$t('common.loading')">
            <t-loading size="medium" :text="$t('common.loading')" />
          </div>

          <div class="settings-container">
            <aside class="settings-sidebar">
              <div class="sidebar-header">
                <h2 class="sidebar-title">{{ title }}</h2>
                <slot name="sidebar-header-extra" />
              </div>
              <nav class="settings-nav" :data-guide="navGuide || undefined">
                <slot name="nav">
                  <template v-for="group in navGroups" :key="group.key">
                    <div class="nav-group-title">{{ group.label }}</div>
                    <div v-for="item in group.items" :key="item.key" :class="['nav-item', { active: modelValue === item.key }]"
                      :data-guide="navItemGuidePrefix ? `${navItemGuidePrefix}-${item.key}` : undefined"
                      @click="emit('update:modelValue', item.key)">
                      <slot name="nav-icon" :item="item" :active="modelValue === item.key">
                        <t-icon :name="item.icon" class="nav-icon" />
                      </slot>
                      <span class="nav-label">{{ item.label }}</span>
                      <span v-if="showBadge(item)" :class="['nav-badge', item.badgeClass]">{{ item.badge }}</span>
                    </div>
                  </template>
                </slot>
              </nav>
            </aside>

            <div class="settings-content">
              <!--
                关闭按钮放在内容列内部（而不是面板右上角）。
                原因：面板在 drawer 形态下撑满内容区（可达 1180px），
                按钮若定位在面板右上角，会离正文 700px 远。
                放在内容列里则始终贴着正文右缘，宽屏窄屏都顺手。
              -->
              <button class="close-btn" type="button" @click="emit('close')" :aria-label="$t('common.close')">
                <svg width="20" height="20" viewBox="0 0 20 20" fill="currentColor" aria-hidden="true">
                  <path d="M15 5L5 15M5 5L15 15" stroke="currentColor" stroke-width="2" stroke-linecap="round" />
                </svg>
              </button>
              <div class="settings-body">
                <slot />
              </div>
              <div v-if="$slots.footer || $slots['footer-note']" class="settings-footer">
                <slot name="footer-note" />
                <div class="settings-footer-actions">
                  <slot name="footer" />
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
/**
 * 全屏"设置类"弹窗壳：遮罩 + 1080×780 面板 + 左侧分组导航 + 右侧内容 + 可选底栏。
 * 被 Settings / AgentEditorModal / OrganizationSettingsModal / KnowledgeBaseEditorModal 共用，
 * 关闭行为（Esc / 遮罩 / 未保存守卫）由消费方通过 useModalShell 决定，本组件只发出 `close`。
 *
 * 样式为非 scoped、以 `.settings-modal-shell` 为根前缀，这样通过 `nav` 插槽自定义导航的消费方
 * 也能复用 .nav-item / .nav-icon 等基础样式。
 */
export interface SettingsModalNavItem {
  key: string
  label: string
  icon?: string
  badge?: number | string | null
  /** 额外的徽标 class，例如 'nav-badge-count' */
  badgeClass?: string
  /** 徽标为 0 时是否仍显示 */
  showZeroBadge?: boolean
  [extra: string]: unknown
}

export interface SettingsModalNavGroup {
  key: string
  label: string
  items: SettingsModalNavItem[]
}

withDefaults(
  defineProps<{
    visible: boolean
    title: string
    /** 当前分区 key（v-model） */
    modelValue?: string
    navGroups?: SettingsModalNavGroup[]
    loading?: boolean
    zIndex?: number
    overlayClass?: string
    navGuide?: string
    navItemGuidePrefix?: string
    /**
     * 容器形态。
     * 'modal'  居中浮层（原行为，默认值保持兼容 —— 其它几个调用方不受影响）
     * 'drawer' 右侧抽屉，内嵌在页面里，不遮挡左侧导航
     */
    variant?: 'modal' | 'drawer'
  }>(),
  {
    modelValue: '',
    navGroups: () => [],
    loading: false,
    zIndex: 1100,
    overlayClass: '',
    navGuide: '',
    navItemGuidePrefix: '',
    variant: 'modal',
  },
)

const emit = defineEmits<{
  (e: 'update:modelValue', key: string): void
  (e: 'close'): void
}>()

function showBadge(item: SettingsModalNavItem): boolean {
  if (item.badge == null || item.badge === '') return false
  if (typeof item.badge === 'number' && item.badge <= 0) return !!item.showZeroBadge
  return true
}
</script>

<style lang="less">
.settings-modal-shell.settings-overlay {
  position: fixed;
  // Stops at a docked assistant panel.
  inset: 0 var(--assistant-dock-width, 0px) 0 0;
  background: rgba(0, 0, 0, 0.5);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 20px;
  backdrop-filter: blur(4px);
  overscroll-behavior: none;
}

/*
 * 设置页形态（drawer = 内嵌页）：设置是**页面**，不是叠加在对话上的面板。
 *
 * 路由点击「模型管理」→ /platform/settings，主内容区展示设置页。
 * 因此这里不设固定宽度、不设遮罩，直接撑满父级（.platform-route-outlet 之外的
 * 剩余区域），与侧栏并排，视觉上就是一个带内部分区导航的普通页面。
 *
 * 与浮层的本质区别：
 *   • 无 position: fixed、无遮罩 —— 它占真实的布局空间
 *   • 无圆角、无阴影 —— 它是页面而非卡片
 *   • 左侧栏始终可见可点，可随时切回其他模块
 */
.settings-modal-shell.settings-drawer-host {
  position: relative;
  flex: 1 1 auto;
  width: 100%;
  min-width: 0;
  height: 100%;
  display: flex;
  align-items: stretch;
  overflow: hidden;
  background: var(--td-bg-color-container);

  /*
   * 注意：Settings.vue 尾部有一段非 scoped 样式
   *   .settings-modal-shell.all-settings-overlay > .settings-modal {
   *     max-width: 1280px; height: 900px;
   *   }
   * 它的选择器权重（0,3,0）高于 .settings-drawer-host .settings-modal（0,2,0），
   * 会在 1920 这类大屏上把设置页卡死在 1280px，右侧留出大片空白。
   * 因此这里用 .settings-drawer-host > .settings-modal（0,2,0 + 子选择器）
   * 无法取胜，必须显式加 !important 覆盖。
   */
  > .settings-modal {
    position: relative;
    width: 100%;
    max-width: none !important;
    height: 100% !important;
    max-height: 100%;
    border-radius: 0;
    border-left: 1px solid var(--td-component-stroke);
    box-shadow: none;
  }

  /*
   * 窄屏（< 1100px）导航横排。
   *
   * 分区导航原本是固定 208px 的左侧竖栏，窄屏下会把内容列挤到 400px 以内，
   * 模型卡片、成员表格这类多列内容基本没法看。
   * 窄屏时改为顶部横向条：分组标题隐藏（横排里没有意义），
   * 导航项按 flex-wrap 自然换行，标题行跟随吸顶。
   */
  @media (max-width: 1100px) {
    .settings-container {
      flex-direction: column;
    }

    .settings-sidebar {
      width: 100%;
      // 内容可能换行成两行，高度交给内容决定，不要写死
      max-height: 40vh;
      border-right: none;
      border-bottom: 1px solid var(--td-component-stroke);
    }

    .sidebar-header {
      padding: 12px 14px 8px;
      border-bottom: none;
    }

    .settings-nav {
      display: flex;
      flex-wrap: wrap;
      align-items: center;
      gap: 4px;
      padding: 0 10px 10px;
      // 横排时只可能横向溢出，纵向不再需要滚动
      overflow-x: auto;
      overflow-y: hidden;
    }

    // 横排下分组标题没有视觉意义，直接隐藏
    .nav-group-title {
      display: none;
    }

    .nav-item {
      margin-bottom: 0;
      padding: 5px 10px;
      flex: 0 0 auto;
      border: 1px solid transparent;
      border-radius: var(--app-radius-pill);

      &.active {
        border-color: var(--td-brand-color-light);
      }
    }

    .nav-label {
      flex: 0 1 auto;
    }

    .nav-icon {
      margin-right: 6px;
    }

    // 窄屏时内容区不再需要限宽（本身就窄），取消居中留白
    .settings-content {
      max-width: 100%;
    }

    // 关闭按钮仍在右上角，但横排导航已占满顶部，
    // 内容区上方留白改由 .settings-body 自己控制
  }

  // 全局防横向滚动：任何子元素意外超宽都不应把整页推出可滚动区
  .settings-container,
  .settings-content,
  .settings-body {
    max-width: 100%;
    box-sizing: border-box;
  }
}

.settings-modal-shell {
  .settings-modal {
    position: relative;
    width: 100%;
    // 1080×780：给成员表 / 系统设置这类多列内容留足空间；外层 20px padding 后 1120，
    // 1280+ 的笔记本都放得下；更窄的视口由 width: 100% 收缩。
    max-width: 1080px;
    height: 780px;
    max-height: calc(100vh - 40px);
    background: var(--td-bg-color-container);
    border-radius: var(--app-radius-xl);
    box-shadow: var(--td-shadow-3);
    overflow: hidden;
    display: flex;
    flex-direction: column;
  }

  .editor-initializing {
    position: absolute;
    inset: 0;
    z-index: 20;
    display: flex;
    align-items: center;
    justify-content: center;
    background: var(--td-bg-color-container);
  }

  /*
   * 关闭按钮定位。
   *
   * 原先 right:16px 是相对整个面板定位的 —— 面板撑满内容区后（宽 1180px），
   * 按钮离右侧正文有 700px 远，鼠标要横穿整页才点得到。
   *
   * 现在改为跟随「内容列」的右缘：内容列 max-width 860px 且居中，
   * 右缘位置 = 面板宽的一半 + 430px。用容器查询变量算不出来，
   * 因此直接在 drawer 形态下改为相对 .settings-content 定位，
   * 由它自己承担右上角位置（.settings-content 是 position: relative 的定位上下文）。
   */
  .close-btn {
    position: absolute;
    top: 16px;
    right: 16px;
    width: 32px;
    height: 32px;
    border: none;
    background: transparent;
    border-radius: var(--app-radius-sm);
    cursor: pointer;
    display: flex;
    align-items: center;
    justify-content: center;
    color: var(--td-text-color-secondary);
    transition: background var(--app-motion-base) ease, color var(--app-motion-base) ease;
    z-index: 10;

    &:hover {
      background: var(--td-bg-color-container-hover);
      color: var(--td-text-color-primary);
    }
  }

  .settings-container {
    display: flex;
    height: 100%;
    width: 100%;
    overflow: hidden;
  }

  .settings-sidebar {
    width: 208px;
    background-color: var(--td-bg-color-settings-modal);
    border-right: 1px solid var(--td-component-stroke);
    flex-shrink: 0;
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }

  .sidebar-header {
    padding: 16px 14px 12px;
    border-bottom: 1px solid var(--td-component-stroke);
    flex-shrink: 0;
  }

  .sidebar-title {
    margin: 0;
    font-size: var(--app-text-xl);
    font-weight: 600;
    color: var(--td-text-color-primary);
  }

  .settings-nav {
    flex: 1;
    padding: 8px 8px 12px;
    overflow-y: auto;
    min-height: 0;

    &::-webkit-scrollbar {
      width: 6px;
    }

    &::-webkit-scrollbar-track {
      background: var(--td-bg-color-secondarycontainer);
    }

    &::-webkit-scrollbar-thumb {
      background: var(--td-gray-color-5);
      border-radius: 3px;
    }

    &::-webkit-scrollbar-thumb:hover {
      background: var(--td-gray-color-6);
    }
  }

  .nav-group-title {
    padding: 8px 14px 2px;
    color: var(--td-text-color-placeholder);
    font-size: var(--app-text-sm);
    font-weight: 600;
    letter-spacing: 0.02em;
  }

  .settings-nav > .nav-group-title:first-child {
    padding-top: 2px;
  }

  .nav-item {
    display: flex;
    align-items: center;
    padding: 6px 12px;
    margin-bottom: 2px;
    border-radius: var(--app-radius-sm);
    cursor: pointer;
    transition: background-color var(--app-motion-base) ease, color var(--app-motion-base) ease;
    font-size: var(--app-text-base);
    color: var(--td-text-color-primary);
    user-select: none;

    &:hover {
      background-color: var(--td-bg-color-container-hover);
      color: var(--td-text-color-primary);
    }

    &.active {
      background-color: var(--td-bg-color-secondarycontainer);
      color: var(--td-brand-color);
      font-weight: 500;
    }
  }

  .nav-icon {
    margin-right: 9px;
    font-size: var(--app-text-xl);
    flex-shrink: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    color: inherit;

    &.nav-icon-img {
      width: 16px;
      height: 16px;
    }

    &.nav-icon-emoji {
      font-size: var(--app-text-base);
      line-height: 1;
    }
  }

  .nav-label {
    flex: 1;
  }

  .nav-badge {
    flex-shrink: 0;
    margin-left: 2px;
    padding: 0 6px;
    border-radius: var(--app-radius-md);
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-secondary);
    font-size: var(--app-text-xs);
    line-height: 16px;
    font-weight: 500;
    text-align: center;

    &.nav-badge-count {
      min-width: 20px;
    }
  }

  /*
   * 内容区：限宽 + 居中。
   *
   * 问题：设置页撑满整个内容区后（drawer 形态下宽达 1180px），
   * 内部组件多数是 width:100%，模型卡片网格会把卡片拉到 320px 以上的
   * 大块区域，右侧出现大片空白，读起来很散。
   *
   * 做法：给内容列一个阅读友好的上限（860px）并水平居中。
   * 用 margin-inline:auto 而不是 justify-content:center —— 后者在内容
   * 超出时会导致左侧被裁掉、无法滚动到，前者超宽时自动退化为靠左填满。
   */
  /*
   * 内容区：铺满可用宽度，不做 max-width 限宽。
   *
   * 曾经限宽 860px 居中，是为了控制正文行长；但设置页里的主体是
   * 卡片网格、表格、资源列表这类"宽度越多越好"的内容，
   * 限宽反而造成右侧大片空白。现在改为跟随视口伸缩：
   *   • flex:1 1 auto + min-width:0  —— 吃掉导航栏之外的全部宽度
   *   • width:100%                  —— 不依赖父级对齐方式
   *   • 不设 max-width              —— 大屏也不留空
   * 可读性由内层 .content-wrapper 的内边距与卡片布局自己把握。
   */
  .settings-content {
    flex: 1 1 auto;
    display: flex;
    flex-direction: column;
    min-width: 0;
    min-height: 0;
    width: 100%;
    overflow: hidden;
    background-color: var(--td-bg-color-container);
    // 作为关闭按钮的定位上下文（见 .close-btn 注释）
    position: relative;
  }

  // 既是滚动容器，也是 flex 列：消费方的 .content-wrapper 想自己滚动时设 flex:1 + overflow:auto，
  // 想让整块内容随页面滚动时保持自然高度即可。
  .settings-body {
    flex: 1;
    min-height: 0;
    display: flex;
    flex-direction: column;
    overflow-y: auto;
    // 关闭按钮在内容列右上角（距右 16px、宽 32px）。
    // 这里不再用 padding-right 把整列顶窄 48px（会造成整块右侧留白），
    // 改为只给首个子元素（各设置页的标题区）留避让，正文继续铺满。
    > :first-child {
      padding-right: 48px;
      box-sizing: border-box;
    }

    &::-webkit-scrollbar {
      width: 6px;
    }

    &::-webkit-scrollbar-track {
      background: var(--td-bg-color-container);
    }

    &::-webkit-scrollbar-thumb {
      background: var(--td-gray-color-5);
      border-radius: 3px;
    }

    &::-webkit-scrollbar-thumb:hover {
      background: var(--td-gray-color-6);
    }
  }

  .settings-footer {
    padding: 12px 40px;
    border-top: 1px solid var(--td-component-stroke);
    display: flex;
    align-items: center;
    justify-content: flex-end;
    gap: 16px;
    flex-shrink: 0;
    background-color: var(--td-bg-color-container);
  }

  .settings-footer-note {
    margin: 0;
    margin-right: auto;
    flex: 1;
    min-width: 0;
    display: flex;
    align-items: flex-start;
    gap: 6px;
    font-size: var(--app-text-md);
    line-height: 20px;
    color: var(--td-text-color-secondary);

    strong {
      margin-right: 4px;
      color: var(--td-text-color-primary);
      font-weight: 500;
    }

    &__icon {
      flex-shrink: 0;
      margin-top: 2px;
      font-size: var(--app-text-base);
      color: var(--td-success-color);
    }
  }

  .settings-footer-actions {
    display: flex;
    gap: 12px;
    flex-shrink: 0;
  }
}

.settings-modal-shell-leave-active {
  transition: opacity var(--app-motion-base) ease;

  .settings-modal {
    transition: transform var(--app-motion-base) ease, opacity var(--app-motion-base) ease;
  }
}

.settings-modal-shell-leave-to {
  opacity: 0;

  .settings-modal {
    transform: scale(0.95);
    opacity: 0;
  }
}
</style>
