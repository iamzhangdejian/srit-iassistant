<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, watch } from 'vue'
// OEM: 白标配置
import { BRAND } from '@/config/oem'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { MessagePlugin, NotifyPlugin } from 'tdesign-vue-next'
import ProtectedResourcePreview from '@/components/ProtectedResourcePreview.vue'
import ManualKnowledgeEditor from '@/components/manual-knowledge-editor.vue'
import UploadConfirmHost from '@/components/UploadConfirmHost.vue'
import { useAuthStore } from '@/stores/auth'
import { useSettingsStore } from '@/stores/settings'
import { consumePendingTenantSwitchToast } from '@/utils/tenantSwitch'
import { useRoleLabel } from '@/composables/useRoleLabel'
import { renderWorkspaceNotifyContent } from '@/utils/workspaceNotifyContent'

// TDesign locale configs
import enUSConfig from 'tdesign-vue-next/esm/locale/en_US'
import zhCNConfig from 'tdesign-vue-next/esm/locale/zh_CN'
import koKRConfig from 'tdesign-vue-next/esm/locale/ko_KR'
import jaJPConfig from 'tdesign-vue-next/esm/locale/ja_JP'
import ruRUConfig from 'tdesign-vue-next/esm/locale/ru_RU'
import zhTWConfig from 'tdesign-vue-next/esm/locale/zh_TW'

const { locale, t, tm } = useI18n()
const { roleIcon } = useRoleLabel()
const router = useRouter()
const authStore = useAuthStore()
const settingsStore = useSettingsStore()

const tdLocaleMap: Record<string, object> = {
  'en-US': enUSConfig,
  'zh-CN': zhCNConfig,
  'zh-TW': zhTWConfig,
  'ko-KR': koKRConfig,
  'ja-JP': jaJPConfig,
  'ru-RU': ruRUConfig,
}

const tdGlobalConfig = computed(() => tdLocaleMap[locale.value] || enUSConfig)

let updateCheckTimer: ReturnType<typeof setInterval> | null = null

// Pending invitations poll: fires once on mount (logged-in case) and
// then every 2 minutes. Light enough to keep the avatar-row badge
// near-live without slamming the API, and avoids the cost of a
// dedicated SSE/WebSocket connection. Stopped on logout via the
// computed below.
let invitationPollTimer: ReturnType<typeof setInterval> | null = null
const INVITATION_POLL_INTERVAL_MS = 2 * 60 * 1000

const startInvitationPolling = () => {
  if (invitationPollTimer || !authStore.isLoggedIn) return
  // Immediate fetch so the badge is correct before the first tick.
  authStore.fetchPendingInvitationCount()
  invitationPollTimer = setInterval(() => {
    if (!authStore.isLoggedIn) return
    authStore.fetchPendingInvitationCount()
  }, INVITATION_POLL_INTERVAL_MS)
}

const stopInvitationPolling = () => {
  if (invitationPollTimer) {
    clearInterval(invitationPollTimer)
    invitationPollTimer = null
  }
}

// React to login/logout via the store's isLoggedIn computed. Watching
// here (rather than only on first mount) handles the OIDC callback
// flow where the user logs in well after App.vue has already mounted.
watch(
  () => authStore.isLoggedIn,
  (logged) => {
    if (logged) startInvitationPolling()
    else stopInvitationPolling()
  },
  { immediate: true },
)

// 切换空间后会 hard reload；切换前 stash 的 toast 这里 consume 并弹出，
// 这样 toast 显示在新页面上，duration 才真正生效。
const showPendingTenantSwitchToast = () => {
  const pending = consumePendingTenantSwitchToast()
  if (!pending) return
  const templateKey = pending.role
    ? 'tenant.switchSuccessContentWithRole'
    : 'tenant.switchSuccessContent'
  // Use tm() not t() — vue-i18n v11's `t()` replaces unspecified named
  // placeholders with empty strings, which would strip {name}/{role}
  // before the chip renderer can split on them. tm() returns the raw
  // message verbatim.
  const rawTemplate = tm(templateKey)
  const template = typeof rawTemplate === 'string' ? rawTemplate : ''
  NotifyPlugin.success({
    title: t('tenant.switchSuccessTitle'),
    content: renderWorkspaceNotifyContent({
      template,
      name: pending.name,
      roleLabel: pending.role,
      roleEnum: pending.roleEnum,
      roleIconName: pending.roleEnum ? roleIcon(pending.roleEnum) : undefined,
    }),
    duration: 6000,
    closeBtn: true,
  })
}

onMounted(() => {
  // OEM: 运行时注入产品名到浏览器标题，保证与 src/config/oem.ts 单一来源一致
  document.title = BRAND.name

  showPendingTenantSwitchToast()

  // Auto check for updates on startup
  setTimeout(() => {
    if (settingsStore.isAutoCheckUpdateEnabled) {
      // @ts-ignore
      if (window.go && window.go.main && window.go.main.App && window.go.main.App.AutoCheckForUpdates) {
        // @ts-ignore
        window.go.main.App.AutoCheckForUpdates()
      }
    }
  }, 2000)

  // Periodically check for updates (every 4 hours)
  updateCheckTimer = setInterval(() => {
    if (settingsStore.isAutoCheckUpdateEnabled) {
      // @ts-ignore
      if (window.go && window.go.main && window.go.main.App && window.go.main.App.AutoCheckForUpdates) {
        // @ts-ignore
        window.go.main.App.AutoCheckForUpdates()
      }
    }
  }, 4 * 60 * 60 * 1000)
})

onUnmounted(() => {
  if (updateCheckTimer) {
    clearInterval(updateCheckTimer)
  }
  stopInvitationPolling()
})

</script>
<template>
  <t-config-provider :globalConfig="tdGlobalConfig">
    <div id="app">
      <RouterView />
      <ManualKnowledgeEditor />
      <ProtectedResourcePreview />
      <UploadConfirmHost />
    </div>
  </t-config-provider>
</template>
<style>
html {
  /* 提示 UA 使用对应配色绘制滚动条等，减少主题切换时的额外重绘 */
  color-scheme: light dark;
}

body,
html,
#app {
  width: 100%;
  height: 100%;
  margin: 0;
  padding: 0;
  font-size: 14px;
  font-family: var(--app-font-family);
  -webkit-font-smoothing: antialiased;
  -moz-osx-font-smoothing: grayscale;
  background: var(--td-bg-color-page);
  color: var(--td-text-color-primary);
}

#app {
  /* 独立合成层，减轻 WebKit 全量重绘时整窗与内容的撕裂感（桌面 WebView 尤其明显） */
  isolation: isolate;
  transform: translateZ(0);
  backface-visibility: hidden;
}
</style>
