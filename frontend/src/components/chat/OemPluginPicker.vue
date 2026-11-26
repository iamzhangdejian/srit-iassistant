<template>
  <!--
    OEM 改造：对话面板「插件」手动勾选区。

    设计要点（对应需求「插件仅由用户手动选择启用」）：
      1. 这里是插件的**唯一调用入口**，必须是用户亲自点选才会带上 skill_names；
      2. 后端 /agent-chat 端点已被 FeatCutAgents 关闭，Agent 无法自动调度插件，
         当前对话走 /knowledge-chat（纯 RAG）。因此本组件的勾选结果是
         「显式声明本次要用的插件」，而不是让模型自己决定去调；
      3. 未勾选任何插件时，请求体里不会出现 skill_names 字段（见
         frontend/src/api/chat/streame.ts:173-174），也就是说默认不加载任何插件。
  -->
  <div class="oem-plugin-picker">
    <!-- 浮层挂到 body：输入框卡片有 overflow:hidden，内部渲染会被裁切 -->
    <!-- 原生下拉：与 OemChatToolbar 一致，避免 t-popup 受控模式点击失效与裁切 -->
    <div class="oem-plugin-popover">
      <button
        type="button"
        class="oem-plugin-trigger"
        :class="{ 'is-active': selectedSkills.length > 0 }"
        :title="t('oemPlugins.title')"
        @click.stop="visible = !visible"
      >
        <t-icon name="extension" size="14px" />
        <span>{{ t('oemPlugins.trigger') }}</span>
        <span v-if="selectedSkills.length" class="oem-plugin-trigger__count">{{ selectedSkills.length }}</span>
      </button>

      <div v-if="visible" class="oem-plugin-panel" @click.stop>
        <div class="oem-plugin-panel__header">
          <span class="oem-plugin-panel__title">{{ t('oemPlugins.title') }}</span>
          <span class="oem-plugin-panel__hint">{{ t('oemPlugins.manualOnly') }}</span>
        </div>

        <div v-if="loading" class="oem-plugin-panel__state">
          <t-loading size="small" />
        </div>

        <div v-else-if="skills.length === 0" class="oem-plugin-panel__state">
          {{ t('oemPlugins.empty') }}
        </div>

        <div v-else class="oem-plugin-panel__list">
          <label
            v-for="skill in skills"
            :key="skill.name"
            class="oem-plugin-item"
            :class="{ 'is-checked': selectedSkills.includes(skill.name) }"
          >
            <t-checkbox
              :checked="selectedSkills.includes(skill.name)"
              @change="(checked: boolean) => toggleSkill(skill.name, checked)"
            />
            <span class="oem-plugin-item__main">
              <span class="oem-plugin-item__name">{{ skill.name }}</span>
              <span v-if="skill.description" class="oem-plugin-item__desc">{{ skill.description }}</span>
            </span>
          </label>
        </div>

        <div v-if="skills.length > 0" class="oem-plugin-panel__footer">
          <t-button size="small" variant="text" :disabled="selectedSkills.length === 0" @click="clearSkills">
            {{ t('oemPlugins.clear') }}
          </t-button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useSettingsStore } from '@/stores/settings'
import { useDeploymentCapabilitiesStore } from '@/stores/deploymentCapabilities'
import { hostSkillsOnly, mentionSkillTargetId } from '@/utils/skillTarget'
import { listSkills, type SkillInfo } from '@/api/skill'

const { t } = useI18n()
const settingsStore = useSettingsStore()
const deploymentCapabilities = useDeploymentCapabilitiesStore()

const visible = ref(false)
const loading = ref(false)
const skills = ref<SkillInfo[]>([])

/**
 * OEM 修复（2026-11-25）：本组件原先调用 listSkills() 时不传沙箱目标，
 * 后端 /api/v1/skills 在没有 sandbox_config_id 时直接返回空列表
 * （内部 skill_handler.go 的早退分支），于是「已安装插件」在对话框里永远
 * 显示为「暂未安装插件」。
 *
 * 桌面 Lite 的技能跑在本机目录（host 目标），因此这里与输入框 @ 技能选择器
 * 使用同一套解析规则（utils/skillTarget.ts）：
 *   remote 不受支持、host 受支持 → 目标为 "host"
 *   否则沿用当前 Agent 的 sandbox_config_id（无则空，后端仍会返回空列表）
 */
async function resolveSkillTarget(): Promise<string> {
  await deploymentCapabilities.ensureLoaded()
  const hostOnly = hostSkillsOnly(
    deploymentCapabilities.isSupported('settings.sandbox.remote'),
    deploymentCapabilities.isSupported('settings.sandbox.host'),
  )
  return mentionSkillTargetId(hostOnly, '')
}

/**
 * 拉取可用插件；面板每次展开都会重拉，安装/卸载后无需刷新页面。
 * 返回是否真的拿到了应答：拉取失败时不能据此清理已勾选的插件。
 */
async function loadSkills(): Promise<boolean> {
  loading.value = true
  try {
    const target = await resolveSkillTarget()
    const res = await listSkills(target)
    skills.value = res?.data || []
    return true
  } catch {
    // 插件列表拉取失败不应影响对话主流程
    skills.value = []
    return false
  } finally {
    loading.value = false
  }
}

/** 收起时取消勾选已不存在的插件，避免把失效名称带进请求体 */
function pruneSelected() {
  const names = new Set(skills.value.map((s) => s.name))
  for (const name of [...selectedSkills.value]) {
    if (!names.has(name)) settingsStore.removeSkill(name)
  }
}

/**
 * 勾选结果统一存在 settings store 的 selectedSkills 字段，
 * startStream 会把它序列化成请求体的 skill_names（空数组时不下发）。
 */
const selectedSkills = computed(() => settingsStore.settings.selectedSkills || [])

/** 切换勾选：直接复用 store 已有的 addSkill / removeSkill（含 localStorage 持久化） */
function toggleSkill(name: string, checked: boolean) {
  if (checked) {
    settingsStore.addSkill(name)
  } else {
    settingsStore.removeSkill(name)
  }
}

/** 清空勾选：逐个走 store 方法，保证 localStorage 同步 */
function clearSkills() {
  for (const name of [...selectedSkills.value]) {
    settingsStore.removeSkill(name)
  }
}

/** 点击下拉以外的区域时收起 */
function closeOnOutside(ev: MouseEvent) {
  if (!visible.value) return
  const target = ev.target as HTMLElement | null
  if (target && target.closest('.oem-plugin-popover')) return
  visible.value = false
}

onBeforeUnmount(() => {
  document.removeEventListener('click', closeOnOutside, true)
})

onMounted(async () => {
  document.addEventListener('click', closeOnOutside, true)
  if (await loadSkills()) pruneSelected()
})

// 面板每次展开都重新拉取：插件市场里刚装的插件立刻可见，卸载后也会消失
watch(visible, async (open) => {
  if (!open) return
  if (await loadSkills()) pruneSelected()
})
</script>

<style lang="less" scoped>
.oem-plugin-popover {
  position: relative;
  display: inline-flex;
  flex: 0 0 auto;
}

.oem-plugin-picker {
  // 在输入框底部的 flex 工具条里保持自身尺寸，不被拉伸
  display: inline-flex;
  align-items: center;
  flex: 0 0 auto;
}

.oem-plugin-trigger {
  display: inline-flex;
  align-items: center;
  white-space: nowrap;
  flex: 0 0 auto;
  gap: 4px;
  padding: 2px 8px;
  height: 24px;
  box-sizing: border-box;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-md);
  background: transparent;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-sm);
  cursor: pointer;

  &:hover {
    background: var(--td-bg-color-container-hover);
  }

  &.is-active {
    border-color: var(--td-brand-color);
    color: var(--td-brand-color);
  }
}

.oem-plugin-trigger__count {
  min-width: 16px;
  height: 16px;
  padding: 0 4px;
  border-radius: 8px;
  background: var(--td-brand-color);
  color: #fff;
  font-size: 11px;
  line-height: 16px;
  text-align: center;
}

.oem-plugin-panel {
  position: absolute;
  bottom: calc(100% + 6px);
  /* 插件按钮贴近输入框右边缘，面板右对齐才不会溢出视口 */
  right: 0;
  z-index: 1200;
  width: 320px;
  min-width: 320px;
  max-height: 380px;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  background: var(--td-bg-color-container);
  border-radius: var(--app-radius-lg);
  box-shadow: var(--td-shadow-2);
}

.oem-plugin-panel__header {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  padding: var(--app-space-3);
  border-bottom: 1px solid var(--td-component-stroke);
}

.oem-plugin-panel__title {
  font-size: var(--app-text-base);
  font-weight: 600;
}

.oem-plugin-panel__hint {
  font-size: var(--app-text-sm);
  color: var(--td-text-color-placeholder);
}

.oem-plugin-panel__state {
  padding: var(--app-space-5);
  text-align: center;
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-sm);
}

.oem-plugin-panel__list {
  flex: 1;
  overflow-y: auto;
  padding: 6px;
}

.oem-plugin-item {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  padding: 8px;
  border-radius: var(--app-radius-md);
  cursor: pointer;

  &:hover {
    background: var(--td-bg-color-container-hover);
  }

  &.is-checked {
    background: var(--td-brand-color-light);
  }
}

.oem-plugin-item__main {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.oem-plugin-item__name {
  font-size: var(--app-text-sm);
  color: var(--td-text-color-primary);
}

.oem-plugin-item__desc {
  margin-top: 2px;
  font-size: var(--app-text-sm);
  color: var(--td-text-color-placeholder);
  overflow: hidden;
  text-overflow: ellipsis;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
}

.oem-plugin-panel__footer {
  display: flex;
  justify-content: flex-end;
  padding: 6px var(--app-space-3);
  border-top: 1px solid var(--td-component-stroke);
}
</style>