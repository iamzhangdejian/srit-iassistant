<template>
  <!--
    OEM：侧栏底部的「授权」入口。

    交付形态里没有账号体系（授权码就是凭证），所以这里不放头像、用户名、
    工作空间、退出登录这些东西，只留一行授权状态：点进去就是
    「系统设置 → 授权」，能看序列号/有效期、也能续期换码。
    高度压到 28px、左对齐、纯文字，不抢对话列表的空间。
  -->
  <button v-if="visible" type="button" class="license-bar"
    :class="[toneClass, { 'license-bar--icon': collapsed }]"
    :title="tooltip" @click="openLicense">
    <span class="license-bar__dot" aria-hidden="true"></span>
    <span class="license-bar__label">{{ stateLabel }}</span>
    <span v-if="remainingLabel" class="license-bar__meta">{{ remainingLabel }}</span>
  </button>
</template>

<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { ensureLicenseStatus, useLicense } from '@/composables/useLicense'
import { useUIStore } from '@/stores/ui'

const router = useRouter()
const license = useLicense()
const uiStore = useUIStore()
// 折叠态侧栏只有 48px，放不下「已授权 · 剩余 N 天」，退化成一颗状态点
const collapsed = computed(() => uiStore.sidebarCollapsed)

const STATE_TEXT: Record<string, string> = {
  valid: '已授权',
  grace: '宽限期',
  readonly: '只读',
  missing: '未授权',
  invalid: '授权无效',
}

const visible = computed(() => license.enabled.value !== false)
const state = computed(() => license.state.value)
const stateLabel = computed(() => STATE_TEXT[state.value] ?? '授权')
const remainingLabel = computed(() => {
  if (!license.status.value) return ''
  const days = license.remainingDays.value
  // -1 是「永久授权」的约定（见 useLicense.remainingDays）
  if (days === -1) return '永久'
  if (days < 0) return '已过期'
  if (days === 0) return '今天到期'
  return `剩余 ${days} 天`
})

const toneClass = computed(() => `is-${state.value}`)

const tooltip = computed(() => {
  const parts = ['授权状态与续期', stateLabel.value, remainingLabel.value].filter(Boolean)
  return parts.join(' · ')
})

const openLicense = () => {
  void router.push({ path: '/platform/settings', query: { section: 'about' } })
}

onMounted(() => {
  // 设置页/入口页通常已经拉过一次，这里只在没值时补一次，避免额外请求。
  if (license.status.value === null) {
    void ensureLicenseStatus()
  }
})
</script>

<style scoped lang="less">
.license-bar {
  display: flex;
  align-items: center;
  gap: 6px;
  width: 100%;
  height: 28px;
  padding: 0 10px;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: var(--td-text-color-placeholder);
  font-size: 12px;
  line-height: 28px;
  text-align: left;
  cursor: pointer;
  transition: background-color 0.15s ease, color 0.15s ease;

  &:hover {
    background: var(--td-bg-color-container-hover);
    color: var(--td-text-color-secondary);
  }

  &__dot {
    flex: 0 0 auto;
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--td-success-color);
  }

  &__label {
    flex: 0 0 auto;
  }

  &__meta {
    flex: 1 1 auto;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: 11px;
    opacity: 0.75;
  }

  // 折叠态：只留状态点，居中显示；文字靠 title 提示
  &--icon {
    justify-content: center;
    padding: 0;

    .license-bar__label,
    .license-bar__meta {
      display: none;
    }
  }

  &.is-grace &__dot,
  &.is-readonly &__dot {
    background: var(--td-warning-color);
  }

  &.is-missing &__dot,
  &.is-invalid &__dot {
    background: var(--td-error-color);
  }
}
</style>