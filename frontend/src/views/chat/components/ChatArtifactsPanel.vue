<template>
  <div class="chat-artifacts-panel">
    <div v-if="previewItem" class="artifact-panel-header">
      <t-button
        class="artifact-back"
        variant="text"
        shape="square"
        size="small"
        :title="$t('agent.artifactDrawer.previewBack')"
        :aria-label="$t('agent.artifactDrawer.previewBack')"
        @click="closePreview"
      >
        <template #icon>
          <t-icon name="chevron-left" size="18px" />
        </template>
      </t-button>
      <ArtifactFileIcon class="artifact-panel-header-icon" :file-name="previewItem.file_name" />
      <div class="artifact-panel-header-title" :title="previewItem.file_name">{{ previewItem.file_name }}</div>
      <t-button
        class="artifact-download"
        variant="text"
        shape="square"
        size="small"
        :title="$t('agent.artifactDrawer.download')"
        :aria-label="$t('agent.artifactDrawer.download')"
        :loading="isDownloading(previewItem)"
        @click="handleDownload(previewItem)"
      >
        <template #icon>
          <t-icon name="download" size="16px" />
        </template>
      </t-button>
      <t-button
        class="artifact-download artifact-delete"
        variant="text"
        shape="square"
        size="small"
        :title="$t('agent.artifactDrawer.delete')"
        :aria-label="$t('agent.artifactDrawer.delete')"
        :loading="isDeleting(previewItem)"
        @click="handleDelete(previewItem)"
      >
        <template #icon>
          <t-icon name="delete" size="16px" />
        </template>
      </t-button>
      <div ref="previewActions" class="artifact-preview-actions" />
    </div>

    <div v-if="previewItem" class="artifact-preview-body">
      <DocumentPreview
        :toolbar-target="previewActions"
        :session-id="previewItem.sessionId"
        :message-id="previewItem.messageId"
        :artifact-index="previewItem.index"
        :file-type="previewFileType"
        :file-name="previewItem.file_name"
        :active="active"
        fill-height
      />
    </div>

    <div v-else-if="collecting && !items.length" class="artifact-panel-empty">
      <t-loading size="small" />
      <span>{{ $t('agent.artifactDrawer.collecting') }}</span>
    </div>
    <div v-else-if="!items.length" class="artifact-panel-empty">
      <t-icon name="folder-open" size="32px" />
      <span>{{ $t('chat.sandbox.artifactsEmpty') }}</span>
    </div>
    <template v-else>
      <div class="artifact-filters">
        <div class="artifact-scope" role="group" :aria-label="$t('chat.sandbox.artifactScope')">
          <button
            v-if="focusedMessageId"
            type="button"
            :aria-pressed="scope === 'current'"
            @click="scope = 'current'"
          >
            {{ $t('chat.sandbox.artifactsCurrent') }}
            <span>{{ currentItems.length }}</span>
          </button>
          <button type="button" :aria-pressed="scope === 'all'" @click="scope = 'all'">
            {{ $t('chat.sandbox.artifactsAllSessions') }}
            <span>{{ items.length }}</span>
          </button>
        </div>
        <t-input
          v-model="searchQuery"
          class="artifact-search"
          :placeholder="$t('chat.sandbox.artifactsSearch')"
          :aria-label="$t('chat.sandbox.artifactsSearch')"
          clearable
        >
          <template #prefix-icon><t-icon name="search" size="16px" /></template>
        </t-input>
      </div>
      <div v-if="collecting" class="artifact-panel-banner">
        <t-icon name="loading" class="artifact-panel-banner-spin" />
        <span>{{ $t('agent.artifactDrawer.collecting') }}</span>
      </div>
      <div v-if="!visibleItems.length" class="artifact-panel-empty">
        <t-icon name="search" size="24px" />
        <span>{{ $t('chat.sandbox.artifactsNoMatches') }}</span>
      </div>
      <ul v-show="visibleItems.length" ref="listRef" class="artifact-list">
        <li
          v-for="item in visibleItems"
          :key="`${item.messageId}:${item.index}-${item.file_name}`"
          class="artifact-item is-previewable"
          :data-message-id="item.messageId"
          :data-artifact-index="item.index"
          @click="openPreview(item)"
        >
          <button type="button" class="artifact-open" :title="$t('agent.artifactDrawer.preview')">
            <ArtifactFileIcon :file-name="item.file_name" />
            <span class="artifact-body">
              <span class="artifact-name" :title="item.file_name">{{ item.file_name }}</span>
              <span class="artifact-meta">
                <span>{{ formatArtifactSize(item.file_size) }}</span>
                <span class="artifact-meta-sep">·</span>
                <span>{{ formatArtifactDateTime(item.created_at) }}</span>
              </span>
              <!-- 跨会话列表里同名文件很常见，标出来源会话才知道该回哪一轮找 -->
              <span v-if="item.sessionTitle" class="artifact-origin" :title="item.sessionTitle">
                {{ item.sessionTitle }}
              </span>
            </span>
          </button>
          <t-button
            class="artifact-download"
            variant="text"
            shape="square"
            size="small"
            :title="$t('agent.artifactDrawer.download')"
            :aria-label="$t('agent.artifactDrawer.download')"
            :loading="isDownloading(item)"
            @click.stop="handleDownload(item)"
          >
            <template #icon>
              <t-icon name="download" size="16px" />
            </template>
          </t-button>
          <t-button
            class="artifact-download artifact-delete"
            variant="text"
            shape="square"
            size="small"
            :title="$t('agent.artifactDrawer.delete')"
            :aria-label="$t('agent.artifactDrawer.delete')"
            :loading="isDeleting(item)"
            @click.stop="handleDelete(item)"
          >
            <template #icon>
              <t-icon name="delete" size="16px" />
            </template>
          </t-button>
        </li>
      </ul>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import { deleteMessageArtifact, downloadArtifact } from '@/api/chat'
import { listArtifactLibrary, type ArtifactLibraryItem } from '@/api/artifacts'
import { mergeArtifactPanelRows } from '@/utils/artifactPanelMerge'
import { resolveFilePreviewExt } from '@/utils/filePreview'
import {
  formatArtifactDateTime,
  formatArtifactSize,
  type SessionArtifactItem,
} from '@/utils/sessionArtifacts'
import { useChatSandboxPanel, type ArtifactPanelFocusState } from '@/composables/useChatSandboxPanel'
import { useConfirmDelete } from '@/components/settings/useConfirmDelete'
import DocumentPreview from '@/components/document-preview.vue'
import ArtifactFileIcon from './ArtifactFileIcon.vue'

/**
 * One row of the panel. The list is served from the cross-session artifact
 * library, so every row carries its own session: previewing or downloading it
 * must use that session, not the one the panel happens to be docked in.
 */
interface PanelArtifactRow extends SessionArtifactItem {
  sessionId: string
  sessionTitle?: string
  versionCount?: number
}

const props = withDefaults(
  defineProps<{
    sessionId: string
    items: SessionArtifactItem[]
    collecting?: boolean
    active?: boolean
  }>(),
  {
    collecting: false,
    active: true,
  },
)

// The list is a computed over the loaded history, so the panel reports the
// delete upward instead of mutating its own prop.
const emit = defineEmits<{ (e: 'deleted', payload: { messageId: string; index: number }): void }>()

const { t } = useI18n()
const panel = useChatSandboxPanel()
const confirmDelete = useConfirmDelete()
const previewActions = ref<HTMLElement | null>(null)
const listRef = ref<HTMLElement | null>(null)
const previewItem = ref<PanelArtifactRow | null>(null)
const downloading = reactive<Record<string, boolean>>({})
const deleting = reactive<Record<string, boolean>>({})
const focusedMessageId = ref<string | null>(null)
const scope = ref<'current' | 'all'>('all')
const searchQuery = ref('')

// The panel lists the project-wide artifact library, not just this chat:
// every session in this space contributes, which is what makes a file the
// assistant produced in another conversation findable here. The scope toggle
// still narrows to the focused turn when the panel was opened from one.
const libraryItems = ref<PanelArtifactRow[]>([])
const libraryLoading = ref(false)
const libraryError = ref(false)
// Latest fetch wins: the user can flip scopes or the active tab while a
// request is in flight, and a stale response must not overwrite a newer one.
let libraryTicket = 0

async function loadLibrary() {
  const mine = ++libraryTicket
  libraryLoading.value = true
  libraryError.value = false
  try {
    const res = await listArtifactLibrary({ page: 1, pageSize: 200 })
    if (mine !== libraryTicket) return
    const rows = Array.isArray(res?.data) ? res.data : []
    libraryItems.value = rows.map(toPanelRow)
  } catch (err) {
    if (mine !== libraryTicket) return
    console.error('[ChatArtifactsPanel] load library failed:', err)
    libraryItems.value = []
    libraryError.value = true
  } finally {
    if (mine === libraryTicket) libraryLoading.value = false
  }
}

function toPanelRow(row: ArtifactLibraryItem): PanelArtifactRow {
  return {
    index: row.index,
    handle: row.handle,
    file_name: row.file_name,
    file_type: row.file_type,
    file_size: row.file_size,
    source_path: row.source_path,
    // The library omits mod_time; created_at is the only timestamp the panel
    // renders, so reuse it rather than inventing a value.
    mod_time: row.created_at,
    created_at: row.created_at,
    messageId: row.message_id,
    sessionId: row.session_id,
    sessionTitle: row.session_title,
    versionCount: row.version_count,
  }
}

// Files written in this very turn may not be in the library response yet: the
// panel can open while the collect is still running. The prop list is the
// live view, so merge it over the fetched rows and let it win on collision.
const mergedItems = computed<PanelArtifactRow[]>(() =>
  mergeArtifactPanelRows(libraryItems.value, props.items, props.sessionId),
)

function rowKey(row: Pick<PanelArtifactRow, 'sessionId' | 'messageId' | 'index'>): string {
  return `${row.sessionId}:${row.messageId}:${row.index}`
}

const currentItems = computed(() =>
  mergedItems.value.filter((item) => item.messageId === focusedMessageId.value),
)
const visibleItems = computed(() => {
  const items = scope.value === 'current' ? currentItems.value : mergedItems.value
  const query = searchQuery.value.trim().toLocaleLowerCase()
  return query ? items.filter((item) => item.file_name.toLocaleLowerCase().includes(query)) : items
})
const collecting = computed(() => props.collecting || libraryLoading.value)

const previewFileType = computed(() => {
  const item = previewItem.value
  if (!item) return ''
  return resolveFilePreviewExt(item.file_name, item.file_type)
})

function downloadKey(item: PanelArtifactRow): string {
  return rowKey(item)
}

function isDownloading(item: PanelArtifactRow): boolean {
  return !!downloading[downloadKey(item)]
}

function isDeleting(item: PanelArtifactRow): boolean {
  return !!deleting[downloadKey(item)]
}

// Each row here is one regeneration of a file, so the delete takes just that
// version. The row carries its own session, which in the project-wide list may
// be a different conversation than the panel is docked in.
function handleDelete(item: PanelArtifactRow) {
  if (isDeleting(item)) return
  confirmDelete({
    title: t('agent.artifactDrawer.deleteTitle'),
    body: t('agent.artifactDrawer.deleteConfirm', { name: item.file_name }),
    onConfirm: () => performDelete(item),
  })
}

async function performDelete(item: PanelArtifactRow) {
  // item.sessionId, not props.sessionId: in the project-wide list the row can
  // belong to another conversation, and the delete endpoint is per-session.
  if (!item.sessionId || !item.messageId) {
    MessagePlugin.error(t('agent.artifactDrawer.deleteFailed'))
    return
  }
  const key = downloadKey(item)
  deleting[key] = true
  try {
    await deleteMessageArtifact(item.sessionId, item.messageId, item.index)
    if (previewItem.value && downloadKey(previewItem.value) === key) previewItem.value = null
    libraryItems.value = libraryItems.value.filter((row) => rowKey(row) !== key)
    emit('deleted', { messageId: item.messageId, index: item.index })
    MessagePlugin.success(t('agent.artifactDrawer.deleted'))
  } catch (err) {
    console.error('[ChatArtifactsPanel] delete failed:', err)
    MessagePlugin.error(t('agent.artifactDrawer.deleteFailed'))
  } finally {
    deleting[key] = false
  }
}

function findItem(messageId: string, previewIndex?: number | null): PanelArtifactRow | undefined {
  if (previewIndex != null && Number.isInteger(previewIndex)) {
    return mergedItems.value.find((item) => item.messageId === messageId && item.index === previewIndex)
  }
  return mergedItems.value.find((item) => item.messageId === messageId)
}

function applyFocus(focus: ArtifactPanelFocusState | null | undefined) {
  if (!focus?.messageId) return
  focusedMessageId.value = focus.messageId
  const wantsPreview = focus.previewIndex != null && Number.isInteger(focus.previewIndex)
  if (wantsPreview) {
    const target = findItem(focus.messageId, focus.previewIndex)
    if (target) previewItem.value = target
    return
  }
  previewItem.value = null
  void nextTick(() => {
    const container = listRef.value
    if (!container) return
    const rows = container.querySelectorAll<HTMLElement>('[data-message-id]')
    for (const row of rows) {
      if (row.getAttribute('data-message-id') === focus.messageId) {
        // Keep positioning inside the list: scrollIntoView can also scroll the
        // outer page horizontally while the fixed panel is sliding into view.
        const itemRect = row.getBoundingClientRect()
        const containerRect = container.getBoundingClientRect()
        let nextTop: number | null = null
        if (itemRect.top < containerRect.top) {
          nextTop = container.scrollTop + itemRect.top - containerRect.top
        } else if (itemRect.bottom > containerRect.bottom) {
          nextTop = container.scrollTop + itemRect.bottom - containerRect.bottom
        }
        if (nextTop !== null) {
          container.scrollTo({ top: Math.max(0, nextTop), behavior: 'instant' })
        }
        break
      }
    }
  })
}

watch(
  () => panel?.artifactFocus.value,
  (focus) => {
    scope.value = focus?.messageId ? 'current' : 'all'
    searchQuery.value = ''
    applyFocus(focus)
  },
  { immediate: true },
)

// Load on first mount, then again whenever the panel becomes visible. The
// panel is kept mounted and v-show'n, so a mount-only load would show a stale
// list after files were produced in other conversations.
watch(
  () => panel?.visible.value && panel?.activeTab.value === 'artifacts',
  (showing) => {
    if (showing) void loadLibrary()
  },
  { immediate: true },
)

watch(
  () => props.sessionId,
  () => {
    previewItem.value = null
    focusedMessageId.value = null
    scope.value = 'all'
    searchQuery.value = ''
    panel?.clearArtifactFocus()
    void loadLibrary()
  },
)

watch(
  () => props.items,
  () => {
    const current = previewItem.value
    if (current) {
      const next = mergedItems.value.find((item) => rowKey(item) === rowKey(current))
      if (!next) previewItem.value = null
      return
    }
    if (scope.value === 'current' && !searchQuery.value.trim()) {
      applyFocus(panel?.artifactFocus.value)
    }
  },
)

function openPreview(item: PanelArtifactRow) {
  previewItem.value = item
}

function closePreview() {
  previewItem.value = null
}

async function handleDownload(item: PanelArtifactRow) {
  if (!item.sessionId || !item.messageId) {
    MessagePlugin.error(t('agent.artifactDrawer.downloadFailed'))
    return
  }
  const key = downloadKey(item)
  downloading[key] = true
  try {
    const blob = await downloadArtifact(item.sessionId, item.messageId, item.index)
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = item.file_name || 'artifact'
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
    setTimeout(() => URL.revokeObjectURL(url), 1000)
  } catch (err) {
    console.error('[ChatArtifactsPanel] download failed:', err)
    MessagePlugin.error(t('agent.artifactDrawer.downloadFailed'))
  } finally {
    downloading[key] = false
  }
}
</script>

<style scoped lang="less">
@import '@/components/css/artifact-filter-tabs.less';

.chat-artifacts-panel {
  flex: 1;
  min-height: 0;
  width: 100%;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.artifact-panel-header {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  padding: 8px 12px;
  border-bottom: 1px solid var(--td-component-stroke);
  flex-shrink: 0;
}

.artifact-back {
  flex-shrink: 0;
  color: var(--td-text-color-secondary);

  :deep(.t-button__icon) {
    margin: 0;
  }
}

.artifact-preview-actions {
  flex-shrink: 0;
}

.artifact-panel-header-icon {
  width: 26px;
  height: 32px;
}

.artifact-panel-header-title {
  min-width: 0;
  flex: 1;
  font-size: var(--app-text-md);
  font-weight: 600;
  line-height: 1.4;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.artifact-preview-body {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  padding: 8px;
}

.artifact-panel-empty {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  padding: 32px 16px;
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-md);
  text-align: center;
}

.artifact-panel-banner {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 14px;
  font-size: var(--app-text-sm);
  color: var(--td-text-color-secondary);
  border-bottom: 1px solid var(--td-component-stroke);
}

.artifact-panel-banner-spin {
  animation: wk-spin 0.8s linear infinite;
}

.artifact-list {
  margin: 0;
  padding: 0 4px 12px;
  list-style: none;
  overflow: auto;
  flex: 1;
  min-height: 0;
}

.artifact-item {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px 8px;
  border-radius: var(--app-radius-md);

  &.is-previewable {
    cursor: pointer;
  }

  &:hover {
    background: var(--td-bg-color-container-hover);
  }

}

.artifact-filters {
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 16px 12px 12px;
}

.artifact-scope {
  .artifact-filter-tabs();
  align-self: flex-start;

  button {
    display: inline-flex;
    align-items: center;
    gap: 8px;

    span {
      color: inherit;
      opacity: 0.7;
      font-size: var(--app-text-sm);
      font-variant-numeric: tabular-nums;
    }
  }
}

.artifact-search :deep(.t-input) {
  border-radius: 7px;
}

.artifact-body {
  flex: 1;
  min-width: 0;
}

.artifact-open {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 0;
  border: 0;
  border-radius: var(--app-radius-xs);
  background: transparent;
  color: inherit;
  font: inherit;
  text-align: left;
  cursor: pointer;

  &:focus-visible {
    outline: 2px solid var(--td-text-color-secondary);
    outline-offset: 4px;
  }
}

.artifact-name {
  display: block;
  font-size: var(--app-text-md);
  font-weight: 500;
  letter-spacing: 0.01em;
  line-height: 1.35;
  color: var(--td-text-color-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.artifact-meta {
  margin-top: 2px;
  font-size: var(--app-text-sm);
  line-height: 1.3;
  color: var(--td-text-color-placeholder);
  display: flex;
  align-items: center;
  gap: 4px;
}

.artifact-meta-sep {
  opacity: 0.6;
}

/* 来源会话：跨会话列表里同名文件很常见，标一行才知道该回哪一轮找。
   单独一行并省略号截断，不挤占上面的大小/时间。 */
.artifact-origin {
  margin-top: 1px;
  font-size: var(--app-text-xs);
  line-height: 1.3;
  color: var(--td-text-color-placeholder);
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  opacity: 0.85;
}

.artifact-download.t-button {
  flex-shrink: 0;
  width: 30px;
  height: 30px;
  border-radius: 7px;
  color: var(--td-text-color-secondary);
  transition: background-color var(--app-motion-fast) ease, color var(--app-motion-fast) ease;

  &:not(.t-is-disabled):not(.t-is-loading):hover {
    background: color-mix(in srgb, var(--td-text-color-primary) 10%, var(--td-bg-color-container));
    color: var(--td-text-color-primary);
  }

  &:not(.t-is-disabled):not(.t-is-loading):active {
    background: color-mix(in srgb, var(--td-text-color-primary) 16%, var(--td-bg-color-container));
  }

  &:focus-visible {
    outline: 2px solid var(--td-text-color-secondary);
    outline-offset: 2px;
  }

  &.artifact-delete:not(.t-is-disabled):not(.t-is-loading):hover {
    background: var(--td-error-color-1);
    color: var(--td-error-color);
  }

  :deep(.t-button__icon) {
    margin: 0;
  }
}
</style>
