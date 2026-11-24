<template>
  <Transition name="sandbox-panel" :duration="{ enter: 240, leave: 300 }">
    <div v-if="panel?.visible.value" class="chat-sandbox-panel-clip">
      <aside
        class="chat-sandbox-panel"
        :class="{ 'is-shifted': shifted, 'is-resizing': resizing }"
        :style="{ width: `${panel?.width.value ?? 420}px`, '--references-shift': `${shiftWidth}px` }"
        role="complementary"
        :aria-label="t('chat.sandbox.panelTitle')"
      >
      <!-- 左缘拖拽把手：按住向左/右拖动调整面板宽度。 -->
      <PanelResizeHandle edge="left" :label="t('knowledgeStages.resizeDrawer')"
        :value="panel.width.value" :min="SANDBOX_PANEL_MIN_WIDTH" :max="SANDBOX_PANEL_MAX_WIDTH"
        @start="startResize" @resize="resizePanel" @end="resizing = false" />
      <div class="chat-sandbox-panel__tabs">
        <div class="chat-sandbox-panel__tablist" role="tablist">
          <button
            v-for="tab in tabs"
            :key="tab.id"
            type="button"
            class="chat-sandbox-panel__tab"
            :class="{ 'is-active': panel?.activeTab.value === tab.id }"
            role="tab"
            :aria-selected="panel?.activeTab.value === tab.id"
            @click="panel?.open(tab.id)"
          >
            <t-icon :name="tab.icon" size="16px" />
            <span>{{ tab.label }}</span>
            <span
              v-if="tab.id === 'artifacts' && artifacts.length"
              class="chat-sandbox-panel__tab-count"
              aria-hidden="true"
            >{{ artifacts.length }}</span>
          </button>
        </div>
        <button
          type="button"
          class="chat-sandbox-panel__close"
          :aria-label="t('common.close')"
          @click="panel?.close()"
        >
          <t-icon name="close" size="16px" />
        </button>
      </div>

      <div
        class="chat-sandbox-panel__body"
        :class="{ 'is-flush': panel?.activeTab.value === 'artifacts' }"
      >
        <ChatArtifactsPanel
          v-show="panel?.activeTab.value === 'artifacts'"
          class="chat-sandbox-panel__artifacts"
          :session-id="sessionId"
          :items="artifacts"
          :collecting="artifactsCollecting"
          :active="panel?.activeTab.value === 'artifacts'"
          @deleted="emit('artifactDeleted', $event)"
        />

        <!-- 项目文件：列出本项目目录下的真实文件，与「产物」互补
             （产物只列对话生成并登记过的文件）。 -->
        <ChatProjectFilesPanel
          ref="projectFilesRef"
          v-show="panel?.activeTab.value === 'projectFiles'"
          class="chat-sandbox-panel__artifacts"
          :project-dir="projectDir"
          :active="panel?.activeTab.value === 'projectFiles'"
          :session-id="sessionId"
          :agent-id="agentId"
          @mention="emit('projectFileMention', $event)"
        />

        <!-- 终端：首次激活时惰性挂载；切 tab 用 v-show 保留实例（不丢 PTY）。 -->
        <SandboxTerminal
          v-if="terminalMounted"
          v-show="panel?.activeTab.value === 'terminal'"
          :key="sessionId"
          ref="terminalRef"
          :session-id="sessionId"
          :agent-id="agentId"
          :agent-source-tenant-id="agentSourceTenantId"
          class="chat-sandbox-panel__terminal"
        />
        <div v-else-if="panel?.activeTab.value === 'terminal'" class="chat-sandbox-panel__placeholder">
          <t-skeleton animation="gradient" :row-col="[{ width: '100%', height: '100%', type: 'rect' }]" />
        </div>

        <!-- 桌面：与终端同样的惰性挂载 + v-show 保活。桌面的重连代价比终端高得多
             （要重跑一遍 3–8 秒的懒启动），切 tab 断开是不可接受的。 -->
        <SandboxDesktop
          v-if="desktopMounted && desktopTabVisible"
          v-show="panel?.activeTab.value === 'desktop'"
          :key="sessionId"
          ref="desktopRef"
          :session-id="sessionId"
          :agent-id="agentId"
          :agent-source-tenant-id="agentSourceTenantId"
          class="chat-sandbox-panel__desktop"
        />
        <div v-else-if="panel?.activeTab.value === 'desktop' && desktopTabVisible" class="chat-sandbox-panel__placeholder">
          <t-skeleton animation="gradient" :row-col="[{ width: '100%', height: '100%', type: 'rect' }]" />
        </div>
      </div>
    </aside>
    </div>
  </Transition>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  useChatSandboxPanel,
  SANDBOX_PANEL_MIN_WIDTH,
  SANDBOX_PANEL_MAX_WIDTH,
  type SandboxPanelTab,
} from '@/composables/useChatSandboxPanel'
import SandboxTerminal from '@/views/chat/components/SandboxTerminal.vue'
import SandboxDesktop from '@/views/chat/components/SandboxDesktop.vue'
import ChatArtifactsPanel from '@/views/chat/components/ChatArtifactsPanel.vue'
import ChatProjectFilesPanel from '@/views/chat/components/ChatProjectFilesPanel.vue'
import PanelResizeHandle from '@/components/PanelResizeHandle.vue'
import { useChatResourcesStore } from '@/stores/chatResources'
import { useDeploymentCapabilitiesStore } from '@/stores/deploymentCapabilities'
import type { SessionArtifactItem } from '@/utils/sessionArtifacts'
// OEM: 沙箱面板 Tab 白名单（只保留 artifacts + terminal）
import { OEM_SANDBOX_TABS } from '@/config/oem'

const props = withDefaults(
  defineProps<{
    sessionId: string
    /**
     * 当前会话所属项目的目录（host_dir）。空串表示会话没关联项目，
     * 「项目文件」tab 据此显示未关联提示而不是发请求。
     */
    projectDir?: string
    /** 当前会话选中的 agent（首次连接时按其配置自动创建沙箱）。 */
    agentId?: string
    /** 共享智能体来源空间，缺省表示本空间自有 agent。 */
    agentSourceTenantId?: string | number | null
    /** 参考来源面板同开时整体左移，避免两块 fixed 面板重叠。 */
    shifted?: boolean
    /** 参考来源面板当前宽度（查看原文时会变宽），左移的距离。 */
    shiftWidth?: number
    artifacts?: SessionArtifactItem[]
    artifactsCollecting?: boolean
  }>(),
  {
    // 这些 prop 给默认值时，Vue 会为它们生成运行时类型声明（String/Number/
    // Boolean），所以有默认值的 prop 必须有对应类型的初始化函数。
    // projectDir 没写默认值，但它是可选的 string，同样需要一个空串兜底，
    // 否则模板里拼 class 时会踩到 undefined，且 props 类型表里缺一项。
    projectDir: '',
    artifacts: () => [],
    artifactsCollecting: false,
    shiftWidth: 420,
  },
)

// The artifact list is owned by the chat view (a computed over the loaded
// history), so a delete inside the panel has to travel back up to it.
const emit = defineEmits<{
  (e: 'artifactDeleted', payload: { messageId: string; index: number }): void
  // 项目文件被 @ 引用：面板只管发起，真正塞进输入框的是聊天页。
  (e: 'projectFileMention', payload: { entry: { path: string; rel: string; name: string; kind: 'file' | 'dir'; size: number } }): void
}>()

const { t } = useI18n()
const panel = useChatSandboxPanel()

/** 项目文件面板实例：@ 引用失败时由聊天页回调它放开「已引用」占位。 */
const projectFilesRef = ref<InstanceType<typeof ChatProjectFilesPanel> | null>(null)
defineExpose({
  releaseProjectFileRef: (path: string) => projectFilesRef.value?.clearReference?.(path),
})
const chatResources = useChatResourcesStore()
const deploymentCapabilities = useDeploymentCapabilitiesStore()
const sandboxConfigsReady = ref(false)

// Hide the desktop tab for CLI / Docker configs. Shared agents whose
// sandbox row is not in this workspace still show the tab and let the
// backend return DESKTOP_UNSUPPORTED.
const desktopTabVisible = computed(() => {
  if (!deploymentCapabilities.isSupported('settings.sandbox.remote')) return false
  const agentId = props.agentId?.trim()
  if (!agentId) return false
  const agent = chatResources.agents.find((item) => item.id === agentId)
  if (!agent) {
    return chatResources.agents.length > 0
  }
  const configId = agent.config?.sandbox_config_id?.trim()
  if (!configId) return false
  if (!sandboxConfigsReady.value) return false
  const cfg = chatResources.sandboxConfigs.find((item) => item.id === configId)
  if (!cfg) return true
  return Boolean(cfg.config?.desktop_enabled)
})

// OEM 改造：沙箱面板现在只保留「产物」一个 Tab。
//
// 「桌面」Tab（VNC 远程桌面）已移除：
//   • 前端不再渲染 desktop Tab，用户无法从界面进入；
//   • 后端对应路由 /api/v1/sessions/:id/sandbox/desktop 也已由
//     internal/router/router.go 的 FeatCutSandboxDesktop 开关关闭。
//
// 「交互终端」Tab 也已移除。它连的不是本机 shell，而是会话绑定沙箱
// （Cube/E2B 微虚拟机）里的 PTY；Lite 版没有沙箱配置、智能体路由又被
// 裁剪，点「创建并启动」必然落到 SANDBOX_NOT_BOUND，提示语却让用户
// 「切换到配置了沙箱的智能体」——没有这个智能体。与其露半截误导人，
// 不如不露。
//
// 若某天需要恢复，把对应 id 加回 OEM_SANDBOX_TABS（并打开后端开关）
// 即可，组件与路由都还在。
const tabs = computed(() => {
  const all: Array<{ id: SandboxPanelTab; icon: string; label: string }> = [
    { id: 'artifacts', icon: 'folder', label: t('chat.sandbox.tabArtifacts') },
    { id: 'projectFiles', icon: 'file-word', label: t('chat.sandbox.tabProjectFiles') },
    { id: 'terminal', icon: 'terminal', label: t('chat.sandbox.tabTerminal') },
    { id: 'desktop', icon: 'desktop', label: t('chat.sandbox.tabDesktop') },
  ]
  const allowed = new Set<string>(OEM_SANDBOX_TABS)
  return all.filter((item) => {
    if (!allowed.has(item.id)) return false
    // desktop 仍受沙箱配置开关约束（配置里没开桌面就不显示）
    if (item.id === 'desktop' && !desktopTabVisible.value) return false
    return true
  })
})

// 终端 / 桌面惰性挂载（首次切到对应 tab 时），面板关闭即销毁（v-if），
// 与 ChatReferencesDrawer 的开合行为一致；会话切换时由 :key 重建。
const terminalMounted = ref(false)
const terminalRef = ref<{ focus?: () => void } | null>(null)
const desktopMounted = ref(false)
const desktopRef = ref<{ start?: () => void } | null>(null)

watch(
  () => [panel?.visible.value, panel?.activeTab.value] as const,
  ([visible, tab]) => {
    if (!visible) {
      // Drop the lazy-mount flags so reopening on Files does not remount
      // either panel (which would connect and refresh TTL).
      terminalMounted.value = false
      desktopMounted.value = false
      return
    }
    if (tab === 'terminal') {
      terminalMounted.value = true
      void nextTick(() => terminalRef.value?.focus?.())
      return
    }
    if (tab === 'desktop' && desktopTabVisible.value) {
      // Same as the terminal tab: mount runs a lookup-only connect. A
      // running sandbox attaches; paused or missing stays on the overlay
      // until the user confirms. Opening the panel must never create or
      // resume a microVM as a side effect.
      desktopMounted.value = true
    }
  },
  { immediate: true },
)

watch(
  () => [panel?.visible.value, props.agentId] as const,
  ([visible]) => {
    if (!visible) return
    void chatResources.ensureSandboxConfigs().finally(() => {
      sandboxConfigsReady.value = true
    })
  },
  { immediate: true },
)

watch(desktopTabVisible, (show) => {
  if (show) return
  if (panel?.activeTab.value === 'desktop') {
    panel.activeTab.value = 'artifacts'
  }
  desktopMounted.value = false
})

watch(
  () => props.sessionId,
  () => {
    panel?.clearArtifactFocus()
  },
)

// --- 左缘拖拽调宽 -------------------------------------------------------
const resizing = ref(false)
let resizeStartWidth = 0
function startResize() {
  if (!panel) return
  resizeStartWidth = panel.width.value
  resizing.value = true
}
function resizePanel(delta: number) {
  panel?.setWidth(resizeStartWidth - delta)
}

</script>

<style scoped lang="less">
.chat-sandbox-panel-clip {
  position: fixed;
  // Stops at a docked assistant panel.
  inset: 0 var(--assistant-dock-width, 0px) 0 0;
  z-index: 1201;
  overflow: hidden;
  pointer-events: none;
}

.chat-sandbox-panel {
  pointer-events: auto;
  position: absolute;
  top: 0;
  right: 0;
  bottom: 0;
  width: min(420px, 100vw);
  max-width: 100vw;
  display: flex;
  flex-direction: column;
  background: var(--td-bg-color-container);
  border-left: 1px solid var(--td-component-stroke);
  box-shadow: -8px 0 24px rgba(0, 0, 0, 0.06);

  &.is-shifted {
    @media (min-width: 1400px) {
      right: var(--references-shift, 420px);
    }
  }

  // 拖拽调宽期间关闭过渡与文本选择，保证跟手。
  &.is-resizing {
    transition: none;
    user-select: none;

    .chat-sandbox-panel__body,
    .chat-sandbox-panel__tabs {
      pointer-events: none;
    }
  }
}

.chat-sandbox-panel__close {
  border: 0;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  width: 28px;
  height: 28px;
  border-radius: var(--app-radius-md);
  cursor: pointer;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  transition: background var(--app-motion-fast) ease, color var(--app-motion-fast) ease;

  &:hover {
    background: color-mix(in srgb, var(--td-text-color-primary) 8%, var(--td-bg-color-secondarycontainer));
    color: var(--td-text-color-primary);
  }
}

.chat-sandbox-panel__tabs {
  display: flex;
  align-items: center;
  gap: 8px;
  height: var(--app-chat-header-height);
  box-sizing: border-box;
  padding: 0 12px;
  border-bottom: 1px solid var(--td-component-stroke);
  flex-shrink: 0;
}

.chat-sandbox-panel__tablist {
  display: flex;
  gap: 4px;
  min-width: 0;
  flex: 1;
  overflow-x: auto;
}

.chat-sandbox-panel__tab {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 28px;
  padding: 0 8px;
  line-height: 20px;
  border: 0;
  border-radius: var(--app-radius-sm);
  background: transparent;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-md);
  cursor: pointer;
  white-space: nowrap;
  transition: background-color var(--app-motion-fast) ease, color var(--app-motion-fast) ease;

  &:hover {
    color: var(--td-text-color-primary);
    background: var(--td-bg-color-container-hover);
  }

  &.is-active {
    color: var(--td-text-color-primary);
    background: var(--td-bg-color-secondarycontainer);
    font-weight: 500;
  }

  &:focus-visible {
    outline: 2px solid var(--td-text-color-secondary);
    outline-offset: -2px;
  }
}

.chat-sandbox-panel__tab-count {
  min-width: 16px;
  height: 16px;
  padding: 0 5px;
  border-radius: var(--app-radius-md);
  background: color-mix(in srgb, var(--td-text-color-primary) 8%, transparent);
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-xs);
  line-height: 16px;
  text-align: center;
  font-variant-numeric: tabular-nums;
}

.chat-sandbox-panel__body {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  padding: 8px;

  &.is-flush {
    padding: 0;
  }
}

.chat-sandbox-panel__terminal,
.chat-sandbox-panel__desktop,
.chat-sandbox-panel__artifacts {
  flex: 1;
  min-height: 0;
  min-width: 0;
}

.chat-sandbox-panel__placeholder {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 10px;
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-md);

  p {
    margin: 0;
  }
}

.sandbox-panel-enter-active .chat-sandbox-panel {
  transition:
    transform 0.24s cubic-bezier(0.22, 0.61, 0.36, 1),
    opacity 0.24s cubic-bezier(0.22, 0.61, 0.36, 1);
}

.sandbox-panel-leave-active .chat-sandbox-panel {
  transition:
    transform 0.3s cubic-bezier(0.22, 0.61, 0.36, 1),
    opacity 0.3s cubic-bezier(0.22, 0.61, 0.36, 1);
}

.sandbox-panel-enter-from .chat-sandbox-panel,
.sandbox-panel-leave-to .chat-sandbox-panel {
  transform: translateX(100%);
  opacity: 0.6;
}
</style>
