<template>
  <!--
    OEM 改造：「召回参数」快捷入口。

    从原 OemChatToolbar（对话页顶部横条）拆出来，改为输入框底部的内联按钮，
    与「插件」按钮排在同一行。拆分的动机：
      1. 顶部横条占掉一整行高度，而空间选择器在单空间部署下没有意义，
         整条横条实际只服务于「模型」和「召回参数」两个设置；
      2. 这两件事都属于「发送消息前的即时调整」，跟着输入框走比放在页面顶部更顺手。

    数据语义完全不变：读写 /api/v1/tenants/kv/retrieval-config，
    与「设置 → 检索配置」是同一份空间级配置，改这里等于改那里。
  -->
  <div class="oem-recall-picker">
    <div class="oem-recall-popover">
      <button
        type="button"
        class="oem-recall-trigger"
        :class="{ 'is-open': visible }"
        :title="t('oemToolbar.recallTitle')"
        :aria-expanded="visible"
        @click.stop="toggleVisible"
        @keydown.esc.stop="visible = false"
      >
        <t-icon name="setting" size="14px" />
        <span>{{ t('oemToolbar.recall') }}</span>
        <!--
          这里曾额外渲染 "top50 / 10" 摘要，把按钮撑到 145px，是输入框底部
          工具条在 680px 窄栏下换行成两行的主因。摘要本身是冗余信息：
          打开面板即可看到精确取值，不打开时也不需要知道当前 top-k。
          去掉后按钮约 72px，为模型名让出横向空间。
        -->
      </button>

      <div v-if="visible" class="oem-recall-panel" @click.stop @keydown.esc.stop="visible = false">
        <div class="oem-recall-panel__title">{{ t('oemToolbar.recallTitle') }}</div>

        <div class="oem-recall-panel__row">
          <label>{{ t('oemToolbar.embeddingTopK') }}</label>
          <t-input-number v-model="recall.embedding_top_k" :min="1" :max="200" :step="5" size="small"
            :disabled="!canEdit" />
        </div>
        <div class="oem-recall-panel__row">
          <label>{{ t('oemToolbar.rerankTopK') }}</label>
          <t-input-number v-model="recall.rerank_top_k" :min="1" :max="50" :step="1" size="small"
            :disabled="!canEdit" />
        </div>
        <div class="oem-recall-panel__row">
          <label>{{ t('oemToolbar.vectorThreshold') }}</label>
          <t-slider v-model="recall.vector_threshold" :min="0" :max="1" :step="0.05" :disabled="!canEdit" />
          <span class="oem-recall-panel__value">{{ recall.vector_threshold.toFixed(2) }}</span>
        </div>
        <div class="oem-recall-panel__row">
          <label>{{ t('oemToolbar.keywordThreshold') }}</label>
          <t-slider v-model="recall.keyword_threshold" :min="0" :max="1" :step="0.05" :disabled="!canEdit" />
          <span class="oem-recall-panel__value">{{ recall.keyword_threshold.toFixed(2) }}</span>
        </div>

        <div class="oem-recall-panel__footer">
          <span v-if="!canEdit" class="oem-recall-panel__hint">{{ t('oemToolbar.adminOnly') }}</span>
          <span v-else-if="saveState === 'saved'" class="oem-recall-panel__hint is-ok">{{ t('oemToolbar.saved') }}</span>
          <span v-else-if="saveState === 'saving'" class="oem-recall-panel__hint">{{ t('oemToolbar.saving') }}</span>
          <span v-else-if="saveState === 'error'" class="oem-recall-panel__hint is-error">{{ t('oemToolbar.saveFailed') }}</span>
          <t-button v-if="canEdit" size="small" theme="primary" variant="outline" @click="resetRecall">
            {{ t('oemToolbar.resetRecall') }}
          </t-button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import {
  getTenantRetrievalConfig,
  updateTenantRetrievalConfig,
  type RetrievalConfig,
} from '@/api/retrieval'

const { t } = useI18n()
const authStore = useAuthStore()

/** 只有管理员可以改空间级召回参数（后端 RBAC 同样会校验） */
const canEdit = computed(() => authStore.hasRole('admin'))

const DEFAULT_RECALL: RetrievalConfig = {
  embedding_top_k: 50,
  vector_threshold: 0.15,
  keyword_threshold: 0.3,
  rerank_top_k: 10,
  rerank_threshold: 0.2,
  rerank_model_id: '',
  rrf_k: 60,
  rrf_vector_weight: 0.7,
  rrf_keyword_weight: 0.3,
}

const recall = reactive<RetrievalConfig>({ ...DEFAULT_RECALL })
const visible = ref(false)
const saveState = ref<'idle' | 'saving' | 'saved' | 'error'>('idle')
let loaded = false
let saveTimer: number | null = null

async function loadRecall() {
  try {
    const res: any = await getTenantRetrievalConfig()
    const cfg = res?.data
    if (cfg) {
      // 空间级 KV 未初始化时后端返回全 0 而不是 null，
      // 所以 top_k 这类字段用「非正值回退默认」，阈值类单独判断（0 是合法值，表示不过滤）。
      const pickPositive = (val: any, fallback: number) =>
        typeof val === 'number' && val > 0 ? val : fallback
      const pickRatio = (val: any, fallback: number) =>
        typeof val === 'number' && val >= 0 ? val : fallback
      Object.assign(recall, {
        embedding_top_k: pickPositive(cfg.embedding_top_k, DEFAULT_RECALL.embedding_top_k),
        rerank_top_k: pickPositive(cfg.rerank_top_k, DEFAULT_RECALL.rerank_top_k),
        vector_threshold: pickRatio(cfg.vector_threshold, DEFAULT_RECALL.vector_threshold),
        keyword_threshold: pickRatio(cfg.keyword_threshold, DEFAULT_RECALL.keyword_threshold),
        rerank_threshold: pickRatio(cfg.rerank_threshold, DEFAULT_RECALL.rerank_threshold),
        rerank_model_id: cfg.rerank_model_id ?? '',
        rrf_k: pickPositive(cfg.rrf_k, DEFAULT_RECALL.rrf_k ?? 60),
        rrf_vector_weight: pickRatio(cfg.rrf_vector_weight, DEFAULT_RECALL.rrf_vector_weight ?? 0.7),
        rrf_keyword_weight: pickRatio(cfg.rrf_keyword_weight, DEFAULT_RECALL.rrf_keyword_weight ?? 0.3),
      })
    }
  } catch {
    // 读不到就用默认值展示，不影响对话主流程
    saveState.value = 'idle'
  } finally {
    loaded = true
  }
}

async function saveRecall() {
  if (!canEdit.value) return
  saveState.value = 'saving'
  try {
    await updateTenantRetrievalConfig({ ...recall, rerank_threshold: recall.rerank_threshold ?? 0.2 })
    saveState.value = 'saved'
  } catch {
    saveState.value = 'error'
  }
}

function resetRecall() {
  Object.assign(recall, DEFAULT_RECALL)
}

/**
 * 点击外部收起。
 *
 * 必须按 capture 阶段监听，且要跳过「刚由触发器打开」的那一次点击：
 * 触发器自己的 @click 先跑（visible 变 true），随后同一个事件冒泡到 document，
 * 若此时 target 不在 .oem-recall-popover 内就会被立刻关掉 —— 表现为
 * 「面板闪一下就没了」。这里用 target.closest 判断即可覆盖该情形，
 * 因为点击触发器的 target 一定在 .oem-recall-popover 内。
 */
function closeOnOutside(ev: MouseEvent) {
  if (!visible.value) return
  const target = ev.target as HTMLElement | null
  if (target && target.closest('.oem-recall-popover')) return
  visible.value = false
}

/** 显式开关：与文档监听解耦，保证「关掉后再点能打开」 */
function toggleVisible() {
  visible.value = !visible.value
}

onMounted(() => {
  void loadRecall()
  // 用 mousedown + capture：比 click 更早，且不受内部按钮 stopPropagation 影响。
  // 触发器自己的 mousedown 也会走到这里，靠 closest 排除。
  document.addEventListener('mousedown', closeOnOutside, true)
})

onBeforeUnmount(() => {
  document.removeEventListener('mousedown', closeOnOutside, true)
  if (saveTimer !== null) window.clearTimeout(saveTimer)
})

// 切空间时重新读取该空间的召回参数
watch(
  () => authStore.effectiveTenantId,
  () => {
    loaded = false
    void loadRecall()
  },
)

// 编辑后 600ms 防抖保存
watch(
  () => [
    recall.embedding_top_k,
    recall.rerank_top_k,
    recall.vector_threshold,
    recall.keyword_threshold,
  ],
  () => {
    if (!loaded || !canEdit.value) return
    if (saveTimer !== null) window.clearTimeout(saveTimer)
    saveTimer = window.setTimeout(() => {
      void saveRecall()
    }, 600)
  },
)
</script>

<style lang="less" scoped>
/* 触发器：视觉规格与同行的「插件」按钮（OemPluginPicker 的 .oem-plugin-trigger）保持一致 */
.oem-recall-popover {
  position: relative;
  display: inline-flex;
}

.oem-recall-trigger {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 8px;
  height: 24px;
  box-sizing: border-box;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-md);
  background: transparent;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-sm);
  white-space: nowrap;
  flex: 0 0 auto;
  cursor: pointer;
  transition: background var(--app-motion-instant), color var(--app-motion-instant);

  &:hover {
    background: var(--td-bg-color-container-hover);
  }

  &.is-open {
    border-color: var(--td-brand-color);
    color: var(--td-brand-color);
  }

  &__summary {
    color: var(--td-text-color-placeholder);
    font-variant-numeric: tabular-nums;
  }
}

/*
 * 面板向上弹出。
 * 输入框贴在窗口底部，向下弹会被视口裁掉；right: 0 让面板向左侧展开，
 * 避免右侧越界（触发器本身就靠右）。
 */
.oem-recall-panel {
  position: absolute;
  bottom: calc(100% + 8px);
  right: 0;
  z-index: 1200;
  width: 320px;
  padding: var(--app-space-3);
  display: flex;
  flex-direction: column;
  gap: 10px;
  background: var(--td-bg-color-container);
  border: .5px solid var(--td-component-stroke);
  border-radius: var(--app-radius-lg);
  box-shadow: 0 8px 28px rgba(0, 0, 0, 0.14);
}

.oem-recall-panel__title {
  font-size: var(--app-text-base);
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.oem-recall-panel__row {
  display: flex;
  align-items: center;
  gap: 8px;

  label {
    flex: 0 0 96px;
    font-size: var(--app-text-sm);
    color: var(--td-text-color-secondary);
  }
}

.oem-recall-panel__value {
  flex: 0 0 36px;
  text-align: right;
  font-size: var(--app-text-sm);
  font-variant-numeric: tabular-nums;
  color: var(--td-text-color-primary);
}

.oem-recall-panel__footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding-top: 6px;
  border-top: 1px solid var(--td-component-stroke);
}

.oem-recall-panel__hint {
  font-size: var(--app-text-sm);
  color: var(--td-text-color-placeholder);

  &.is-ok { color: var(--td-success-color); }
  &.is-error { color: var(--td-error-color); }
}
</style>