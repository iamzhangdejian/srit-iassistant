<template>
  <div class="project-files-panel">
    <!-- 未关联项目时不发请求：没有 host_dir 后端必然 403，空请求只是噪音。 -->
    <div v-if="!projectDir" class="tree-empty">
      <t-icon name="folder" size="28px" />
      <span>{{ $t('chat.sandbox.projectFilesNoProject') }}</span>
    </div>

    <template v-else>
      <div class="tree-toolbar">
        <t-input
          v-model="keyword"
          class="tree-search"
          :placeholder="$t('chat.sandbox.projectFilesSearch')"
          clearable
        >
          <template #prefix-icon><t-icon name="search" size="16px" /></template>
        </t-input>
        <t-tooltip :content="$t('chat.sandbox.projectFilesRefresh')">
          <button type="button" class="tree-tool" :disabled="loading" @click="load">
            <t-icon name="refresh" size="17px" :class="{ 'is-spinning': loading }" />
          </button>
        </t-tooltip>
        <t-tooltip :content="expanded ? $t('chat.sandbox.projectFilesCollapseAll') : $t('chat.sandbox.projectFilesExpandAll')">
          <button type="button" class="tree-tool" @click="toggleExpandAll">
            <t-icon :name="expanded ? 'chevron-up' : 'chevron-down'" size="17px" />
          </button>
        </t-tooltip>
      </div>

      <div v-if="error" class="tree-empty">
        <t-icon name="error-circle" size="28px" />
        <span>{{ $t('chat.sandbox.projectFilesFailed') }}</span>
        <t-button size="small" variant="outline" @click="load">{{ $t('common.retry') }}</t-button>
      </div>
      <div v-else-if="!loading && !rows.length" class="tree-empty">
        <t-icon name="folder-open" size="28px" />
        <span>{{ keyword.trim() ? $t('chat.sandbox.projectFilesNoMatch') : $t('chat.sandbox.projectFilesEmpty') }}</span>
      </div>

      <!-- 根目录行：Finder 里它是窗口标题下的第一级，这里做成不可折叠的定位行 -->
      <div v-if="rows.length" class="tree-root">
        <t-icon name="folder" size="17px" class="tree-icon-dir" />
        <span class="tree-root-name">{{ rootName }}</span>
      </div>

      <ul v-show="rows.length" class="tree-list" role="tree">
        <li
          v-for="row in rows"
          :key="row.key"
          class="tree-row"
          :class="{ 'is-dir': row.entry.kind === 'dir', 'is-disabled': !row.entry.downloadable && row.entry.kind === 'file' }"
          role="treeitem"
          :aria-expanded="row.entry.kind === 'dir' ? isExpanded(row.entry.path) : undefined"
          :style="{ paddingLeft: `${8 + (row.depth - 1) * 16}px` }"
          :title="row.entry.rel"
          @click="handleRowClick(row.entry)"
          @dblclick="handleRowDblClick(row.entry)"
        >
          <!-- 展开箭头：只有目录有；文件位留白以对齐 -->
          <span v-if="row.entry.kind === 'dir'" class="tree-caret" :class="{ 'is-open': isExpanded(row.entry.path) }">
            <t-icon name="chevron-right" size="14px" />
          </span>
          <span v-else class="tree-caret tree-caret--blank" />

          <!-- Finder 的文件夹是实心蓝色，文件按类型上图标 -->
          <t-icon
            v-if="row.entry.kind === 'dir'"
            :name="isExpanded(row.entry.path) ? 'folder-open' : 'folder'"
            size="17px"
            class="tree-icon-dir"
          />
          <ArtifactFileIcon v-else :file-name="row.entry.name" class="tree-icon-file" />

          <span class="tree-name">{{ row.entry.name }}</span>

          <!-- 预览：只有能识别的普通文件才给入口，软链接/超大文件不出现。 -->
          <button
            v-if="canPreview(row.entry)"
            type="button"
            class="tree-eye"
            :title="$t('chat.sandbox.projectFilesPreview')"
            @click.stop="openPreview(row.entry)"
          >
            <t-icon name="browse" size="14px" />
          </button>

          <!-- @ 引用：把这一项挂进输入框作为上下文。文件夹引用的是目录清单。 -->
          <button
            type="button"
            class="tree-at"
            :title="$t('chat.sandbox.projectFilesMention')"
            :disabled="referencing[row.entry.path] === true"
            @click.stop="handleMention(row.entry)"
          >
            <t-icon v-if="referencing[row.entry.path]" name="loading" size="14px" />
            <span v-else>@</span>
          </button>
        </li>
      </ul>

      <p v-if="list?.truncated" class="tree-note">
        {{ $t('chat.sandbox.projectFilesTruncated', { count: list.total }) }}
      </p>
    </template>

    <!-- 预览抽屉：与产物面板共用 DocumentPreview，PDF/Office/图片/文本都走同一套 -->
    <t-drawer
      v-model:visible="previewVisible"
      :size="previewSize"
      :z-index="2000"
      attach="body"
      :footer="false"
      :close-btn="true"
      :close-on-overlay-click="true"
      :close-on-esc-keydown="true"
      :destroy-on-close="false"
      @close="closePreview"
    >
      <template #header>
        <div class="preview-head">
          <ArtifactFileIcon v-if="previewEntry" :file-name="previewEntry.name" class="preview-head__icon" />
          <div class="preview-head__title" :title="previewEntry?.rel">{{ previewEntry?.name }}</div>
          <span v-if="previewRange" class="preview-head__chip" :title="$t('chat.sandbox.projectFilesPreviewStreaming')">
            {{ $t('chat.sandbox.projectFilesPreviewStreaming') }}
          </span>
          <t-button
            v-if="desktopBridge"
            variant="text"
            shape="square"
            size="small"
            :title="$t('chat.sandbox.projectFilesOpenSystem')"
            @click="handleOpenInSystemApp"
          >
            <template #icon><t-icon name="open" size="16px" /></template>
          </t-button>
          <t-button
            v-if="desktopBridge"
            variant="text"
            shape="square"
            size="small"
            :title="$t('chat.sandbox.projectFilesReveal')"
            @click="handleRevealInFileManager"
          >
            <template #icon><t-icon name="folder" size="16px" /></template>
          </t-button>
          <t-button
            variant="text"
            shape="square"
            size="small"
            :title="$t('chat.sandbox.projectFilesDownload')"
            :loading="previewDownloading"
            :disabled="!previewEntry?.downloadable"
            @click="handlePreviewDownload"
          >
            <template #icon><t-icon name="download" size="16px" /></template>
          </t-button>
        </div>
      </template>
      <div class="preview-body">
        <div v-if="previewLoading" class="preview-state">
          <t-loading size="small" />
          <span>{{ $t('common.loading') }}</span>
        </div>
        <div v-else-if="previewError" class="preview-state">
          <t-icon name="error-circle" size="28px" />
          <span>{{ $t('chat.sandbox.projectFilesPreviewFailed') }}</span>
          <t-button size="small" variant="outline" @click="reloadPreview">{{ $t('common.retry') }}</t-button>
        </div>
        <DocumentPreview
          v-else-if="previewEntry && (previewBlob || previewRange)"
          :key="previewEntry.path"
          :source-blob="previewBlob || undefined"
          :pdf-range="previewRange"
          :file-name="previewEntry.name"
          :file-type="previewExt"
          :active="previewVisible"
          fill-height
        />
      </div>
    </t-drawer>
  </div>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import {
  downloadProjectFile,
  fetchProjectFileRange,
  listProjectFiles,
  probeProjectFileSize,
  type ProjectFileEntry,
  type ProjectFileList,
} from '@/api/projectFiles'
import { isKnownPreviewableFile, resolveFilePreviewExt } from '@/utils/filePreview'
import DocumentPreview from '@/components/document-preview.vue'
import type { PdfRangeSource } from '@/components/source-preview/PdfSourceViewer.vue'
import { hasDesktopFileBridge, openInSystemApp, revealInFileManager } from '@/composables/useDesktopFiles'
import ArtifactFileIcon from './ArtifactFileIcon.vue'

const props = withDefaults(
  defineProps<{
    /** 当前会话所属项目的目录；空串表示没有关联项目。 */
    projectDir?: string
    /** 面板是否停在本 tab：只在可见时拉取。 */
    active?: boolean
    /** 当前会话 id，@ 引用要把文件挂到这个会话上。 */
    sessionId?: string
    /** 当前助手，用于沿用它的解析器规则。 */
    agentId?: string
  }>(),
  { projectDir: '', active: false, sessionId: '', agentId: '' },
)

// @ 引用成功后由父组件把附件塞进输入框——面板不直接操作 composer，
// 否则它在新建对话页（没有会话 id 之前）就没法工作。
const emit = defineEmits<{
  (e: 'mention', payload: { entry: ProjectFileEntry }): void
}>()

const { t } = useI18n()

const list = ref<ProjectFileList | null>(null)
const loading = ref(false)
const error = ref(false)
const keyword = ref('')
/** 已展开的目录绝对路径。默认只展开根，避免一进来就是几千行。 */
const expandedPaths = ref<Set<string>>(new Set())

const rootName = computed(() => {
  const root = list.value?.root || props.projectDir
  const parts = root.split('/').filter(Boolean)
  return parts[parts.length - 1] || root
})

interface TreeRow {
  key: string
  depth: number
  entry: ProjectFileEntry
}

/**
 * 把后端返回的扁平列表整理成可见行。
 *
 * 后端已经按目录在前、修改时间倒序排过，这里只做两件事：
 *  1. 过滤掉被折叠目录的后代；
 *  2. 搜索时改为平铺展示所有命中项（含其后代不再考虑层级），
 *     这样输入关键词就能直接跳到深层文件，不必先手动展开。
 */
const rows = computed<TreeRow[]>(() => {
  const entries = list.value?.entries || []
  const query = keyword.value.trim().toLocaleLowerCase()

  if (query) {
    return entries
      .filter((entry) => entry.rel.toLocaleLowerCase().includes(query))
      .map((entry) => ({ key: entry.path, depth: Math.max(1, entry.depth), entry }))
  }

  // 折叠态：一个条目可见，当且仅当它的全部祖先目录都处于展开状态。
  const hiddenPrefixes: string[] = []
  const out: TreeRow[] = []
  for (const entry of entries) {
    // entries 是深度优先顺序，父目录一定在子项之前，
    // 所以「父目录没展开」这一信息可以直接从前缀列表里查。
    if (hiddenPrefixes.some((prefix) => entry.path.startsWith(prefix + '/'))) continue
    out.push({ key: entry.path, depth: Math.max(1, entry.depth), entry })
    if (entry.kind === 'dir' && !expandedPaths.value.has(entry.path)) {
      hiddenPrefixes.push(entry.path)
    }
  }
  return out
})

const expanded = computed(() => {
  const dirs = (list.value?.entries || []).filter((entry) => entry.kind === 'dir')
  return dirs.length > 0 && dirs.every((dir) => expandedPaths.value.has(dir.path))
})

function isExpanded(path: string): boolean {
  return expandedPaths.value.has(path)
}

function toggleDir(path: string) {
  const next = new Set(expandedPaths.value)
  if (next.has(path)) next.delete(path)
  else next.add(path)
  expandedPaths.value = next
}

function toggleExpandAll() {
  if (expanded.value) {
    expandedPaths.value = new Set()
    return
  }
  const all = (list.value?.entries || []).filter((entry) => entry.kind === 'dir')
  expandedPaths.value = new Set(all.map((entry) => entry.path))
}

function handleRowClick(entry: ProjectFileEntry) {
  if (entry.kind === 'dir') toggleDir(entry.path)
}

// 双击文件直接引用到对话；目录仍然只做展开/折叠，避免误触。
function handleRowDblClick(entry: ProjectFileEntry) {
  if (entry.kind === 'file') void handleMentionEntry(entry)
}

let ticket = 0

async function load() {
  if (!props.projectDir) {
    list.value = null
    return
  }
  const mine = ++ticket
  loading.value = true
  error.value = false
  try {
    const res = await listProjectFiles(props.projectDir)
    if (mine !== ticket) return
    list.value = res ?? null
  } catch (err) {
    if (mine !== ticket) return
    console.error('[ChatProjectFilesPanel] load failed:', err)
    list.value = null
    error.value = true
  } finally {
    if (mine === ticket) loading.value = false
  }
}

// 只在 tab 可见时拉取；面板常驻挂载（v-show），不能在 mount 时拉。
// 切项目要清空展开态，否则会带着上一个项目的路径集合过去。
watch(
  () => props.projectDir,
  () => {
    expandedPaths.value = new Set()
    keyword.value = ''
  },
)

// 换会话时清掉「已引用」记录：附件列表是会话级的，新会话的输入框是空的，
// 沿用旧记录会让用户以为那个文件还在上下文里。
watch(
  () => props.sessionId,
  () => {
    Object.keys(referencing).forEach((key) => delete referencing[key])
  },
)

watch(
  () => [props.active, props.projectDir] as const,
  ([active, dir]) => {
    if (active && dir) void load()
  },
  { immediate: true },
)

/**
 * 已经引用过的条目路径（请求进行中 + 已登记完成都记在这里）。
 *
 * 后端每次 /project-refs 都会新建一个临时附件（新的 attachment_id），所以
 * 「同一个文件 @ 两次」在服务端看来是两个不同对象，前端必须先拦住：重复
 * 引用只会让输入框里出现两个一模一样的标签，对模型没有增量信息。
 */
const referencing = reactive<Record<string, boolean>>({})

function handleMention(entry: ProjectFileEntry) {
  if (referencing[entry.path]) {
    MessagePlugin.info(t('chat.sandbox.projectFilesAlreadyReferenced'))
    return
  }
  // 先占位再发事件：父组件的请求是异步的，不占位会被连点绕过。
  referencing[entry.path] = true
  emit('mention', { entry })
}

/**
 * 父组件确认这次引用失败时回调，把占位放开，用户可以重试。
 * 成功则永久保持 —— 同一路径在本会话里只引用一次。
 */
function clearReference(path: string) {
  delete referencing[path]
}

defineExpose({ clearReference })

/**
 * 预览：文件先从后端取成 Blob 再喂给 DocumentPreview。
 *
 * 面板本身只有侧栏宽度，正文放不下，所以复用右侧抽屉；PDF 走 pdf.js、
 * Office 走各自解析器，和产物面板是同一条渲染路径。
 */
const previewVisible = ref(false)
const previewEntry = ref<ProjectFileEntry | null>(null)
const previewBlob = ref<Blob | null>(null)
const previewLoading = ref(false)
const previewError = ref(false)
const previewDownloading = ref(false)
/** 每一轮预览一个票号，避免快速切换文件时旧请求覆盖新结果。 */
let previewTicket = 0

/** 超过这个大小的文件不做内存预览，只让用户下载（200MB 是后端上限）。 */
const MAX_PREVIEW_BYTES = 80 * 1024 * 1024
/** 超大 PDF 走 Range 分段：这里存的是 pdf.js 要用的取字节函数。 */
const previewRange = ref<PdfRangeSource | null>(null)
/** 抽屉宽度：留出侧栏，窄窗口退化成 92vw。 */
const previewSize = 'min(900px, 92vw)'
// 桌面壳才有 window.go 桥；浏览器形态下这两个按钮直接不渲染。
const desktopBridge = hasDesktopFileBridge()

const previewExt = computed(() => resolveFilePreviewExt(previewEntry.value?.name))

function canPreview(entry: ProjectFileEntry): boolean {
  return entry.kind === 'file' && entry.downloadable && isKnownPreviewableFile(entry.name)
}

/**
 * 超大 PDF 的取字节函数。
 *
 * 「大文件」从 80MB 起：这个尺寸整份读进内存会明显卡顿，而 pdf.js 只需要
 * 交叉引用表与当前页的字节。总长度先探测一次（Range 0-0），列表里的 size
 * 可能是旧的 —— 文件刚被重新生成时长度会变，pdf.js 拿到错的长度会算错偏移。
 */
async function createRangeSource(entry: ProjectFileEntry): Promise<PdfRangeSource> {
  const dir = props.projectDir || ''
  const total = await probeProjectFileSize(dir, entry.path).catch(() => entry.size)
  return {
    totalBytes: total,
    fetchRange: async (begin, end) => {
      const { chunk } = await fetchProjectFileRange(dir, entry.path, begin, end)
      return chunk
    },
  }
}

async function openPreview(entry: ProjectFileEntry) {
  if (!canPreview(entry)) return
  const isPdf = resolveFilePreviewExt(entry.name) === 'pdf'
  const oversized = entry.size > MAX_PREVIEW_BYTES
  // 只有 PDF 能按区间取；Office 解析器要整份字节，太大了就只给下载。
  if (oversized && !isPdf) {
    MessagePlugin.warning(t('chat.sandbox.projectFilesPreviewTooLarge'))
    return
  }
  previewEntry.value = entry
  previewVisible.value = true
  previewRange.value = null
  if (oversized) {
    // 不下载整份：探测长度后交给 pdf.js 按需拉区间。
    previewBlob.value = null
    previewLoading.value = true
    const mine = ++previewTicket
    try {
      const source = await createRangeSource(entry)
      if (mine !== previewTicket) return
      previewRange.value = source
    } catch (err) {
      if (mine !== previewTicket) return
      console.error('[ChatProjectFilesPanel] range probe failed:', err)
      previewError.value = true
    } finally {
      if (mine === previewTicket) previewLoading.value = false
    }
    return
  }
  await loadPreview()
}

async function loadPreview() {
  const entry = previewEntry.value
  if (!entry || !props.projectDir) return
  const mine = ++previewTicket
  previewLoading.value = true
  previewError.value = false
  previewBlob.value = null
  previewRange.value = null
  try {
    const blob = await downloadProjectFile(props.projectDir, entry.path)
    if (mine !== previewTicket) return
    previewBlob.value = blob
  } catch (err) {
    if (mine !== previewTicket) return
    console.error('[ChatProjectFilesPanel] preview failed:', err)
    previewError.value = true
  } finally {
    if (mine === previewTicket) previewLoading.value = false
  }
}

function reloadPreview() {
  void loadPreview()
}

function closePreview() {
  previewVisible.value = false
  previewEntry.value = null
  previewBlob.value = null
  previewRange.value = null
  previewError.value = false
  // 让还在飞的请求作废，避免旧文件覆盖新抽屉。
  previewTicket += 1
}

async function handleOpenInSystemApp() {
  const entry = previewEntry.value
  if (!entry) return
  try {
    await openInSystemApp(entry.path)
  } catch (err: any) {
    MessagePlugin.error(err?.message || String(err) || t('chat.sandbox.projectFilesOpenSystemFailed'))
  }
}

async function handleRevealInFileManager() {
  const entry = previewEntry.value
  if (!entry) return
  try {
    await revealInFileManager(entry.path)
  } catch (err: any) {
    MessagePlugin.error(err?.message || String(err) || t('chat.sandbox.projectFilesRevealFailed'))
  }
}

async function handlePreviewDownload() {
  const entry = previewEntry.value
  if (!entry || !props.projectDir) return
  previewDownloading.value = true
  try {
    // 再取一次而不是复用 Blob：预览失败时下载按钮也要能用。
    const blob = await downloadProjectFile(props.projectDir, entry.path)
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = entry.name
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
    setTimeout(() => URL.revokeObjectURL(url), 1000)
  } catch (err) {
    console.error('[ChatProjectFilesPanel] download failed:', err)
    MessagePlugin.error(t('chat.sandbox.projectFilesPreviewFailed'))
  } finally {
    previewDownloading.value = false
  }
}

async function handleMentionEntry(item: ProjectFileEntry) {
  if (!item.downloadable && item.kind === 'file') {
    // 软链接等非常规文件：读不了正文，直接说清楚而不是抛一个看不懂的错误。
    MessagePlugin.warning(t('chat.sandbox.projectFilesUnreadable'))
    return
  }
  handleMention(item)
}
</script>

<style scoped lang="less">
.project-files-panel {
  flex: 1;
  min-height: 0;
  width: 100%;
  display: flex;
  flex-direction: column;
}

/* 工具栏：搜索框 + 刷新 + 全部展开，模仿 Finder 的标题栏操作区 */
.tree-toolbar {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 10px 12px 8px;
  flex-shrink: 0;
}

.tree-search {
  flex: 1;
  min-width: 0;

  :deep(.t-input) {
    border-radius: 7px;
  }
}

.tree-tool {
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: var(--td-text-color-secondary);
  cursor: pointer;

  &:hover:not(:disabled) {
    background: var(--td-bg-color-container-hover);
  }

  &:disabled {
    opacity: 0.45;
    cursor: default;
  }
}

.is-spinning {
  animation: wk-spin 0.8s linear infinite;
}

/* 根目录行：不可折叠，用来交代「这些文件在哪」 */
.tree-root {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 2px 12px 6px;
  font-size: var(--app-text-sm);
  color: var(--td-text-color-secondary);
  flex-shrink: 0;

  .tree-root-name {
    font-weight: 500;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
}

.tree-list {
  flex: 1;
  min-height: 0;
  margin: 0;
  padding: 0 4px 12px;
  list-style: none;
  overflow: auto;
}

.tree-row {
  display: flex;
  align-items: center;
  gap: 5px;
  height: 28px;
  padding-right: 6px;
  border-radius: 5px;
  font-size: var(--app-text-md);
  color: var(--td-text-color-primary);
  cursor: pointer;
  user-select: none;

  &:hover {
    background: var(--td-bg-color-container-hover);

    /* 悬停才露出引用键，避免整列图标喧宾夺主 */
    .tree-at {
      opacity: 1;
    }
  }

  &.is-disabled {
    opacity: 0.5;
    cursor: default;
  }
}

/* Finder 里文件夹名是蓝色，文件是黑色 */
.is-dir .tree-name {
  color: var(--td-brand-color);
}

.tree-caret {
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 14px;
  color: var(--td-text-color-placeholder);
  transition: transform 0.15s ease;

  &.is-open {
    transform: rotate(90deg);
  }

  &--blank {
    /* 文件行留出与箭头等宽的空白，让图标与同层目录对齐 */
    width: 14px;
  }
}

.tree-icon-dir {
  flex-shrink: 0;
  color: var(--td-brand-color);
}

.tree-icon-file {
  flex-shrink: 0;
  width: 17px;
  height: 20px;
}

.tree-name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  line-height: 1.4;
}

/* 「@文件」：悬停才出现，与下载键并排，避免整列图标太吵 */
.tree-at {
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  padding: 0;
  border: 1px solid transparent;
  border-radius: 5px;
  background: transparent;
  color: var(--td-text-color-secondary);
  font-size: 13px;
  font-weight: 600;
  line-height: 1;
  cursor: pointer;
  opacity: 0;
  transition: opacity 0.15s ease, background-color 0.15s ease;

  &:hover:not(:disabled) {
    background: var(--td-brand-color-light);
    border-color: var(--td-brand-color-light);
    color: var(--td-brand-color);
  }

  &:disabled {
    cursor: default;
  }
}

.tree-row:hover .tree-at {
  opacity: 1;
}

/* 「预览」：与 @ 同排，悬停出现 */
.tree-eye {
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  padding: 0;
  border: 1px solid transparent;
  border-radius: 5px;
  background: transparent;
  color: var(--td-text-color-secondary);
  cursor: pointer;
  opacity: 0;
  transition: opacity 0.15s ease, background-color 0.15s ease;

  &:hover {
    background: var(--td-brand-color-light);
    border-color: var(--td-brand-color-light);
    color: var(--td-brand-color);
  }
}

.tree-row:hover .tree-eye {
  opacity: 1;
}

.preview-head {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.preview-head__icon {
  flex-shrink: 0;
  width: 17px;
  height: 20px;
}

.preview-head__chip {
  flex: 0 0 auto;
  padding: 1px 6px;
  border-radius: 999px;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  font-size: 11px;
  line-height: 16px;
  white-space: nowrap;
}
.preview-head__title {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-weight: 600;
}

.preview-body {
  height: calc(100vh - 108px);
  min-height: 240px;
  display: flex;
  flex-direction: column;
}

.preview-state {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 10px;
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-md);
}

.tree-empty {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 10px;
  padding: 32px 16px;
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-md);
  text-align: center;
}

.tree-note {
  margin: 0 12px 10px;
  font-size: var(--app-text-xs);
  line-height: 1.4;
  color: var(--td-text-color-placeholder);
}
</style>