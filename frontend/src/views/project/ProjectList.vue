<template>
  <!--
    项目页面：把原来挤在侧栏上方的项目树提升成独立一级菜单后的落点。

    为什么不是「侧栏原样搬过来」：项目树在侧栏里只有 200 多像素宽，
    目录路径、会话标题都被截断，重命名/设默认/删除只能靠右键小菜单。
    做成整页之后，项目可以铺开成卡片网格，目录、会话数、默认标识一眼可见。

    结构完全复用已有的两套组件，不重写业务逻辑：
      · ProjectTree —— 项目创建/重命名/设默认/删除（含目录选择与错误提示）
      · SessionSidebarRow —— 项目内会话行，与侧栏历史列表共用同一套菜单动作
  -->
  <div class="project-page">
    <header class="project-page__head">
      <div class="project-page__heading">
        <h1 class="project-page__title">{{ t('project.title') }}</h1>
        <p class="project-page__subtitle">{{ t('project.pageSubtitle') }}</p>
      </div>
      <button type="button" class="project-page__create" :disabled="creating" @click="handleCreate">
        <t-icon name="add" size="16px" />
        <span>{{ t('project.create') }}</span>
      </button>
    </header>

    <div v-if="!projectStore.loading && projects.length === 0" class="project-page__empty">
      <t-icon name="folder" size="40px" class="project-page__empty-icon" />
      <p class="project-page__empty-text">{{ t('project.empty') }}</p>
      <button type="button" class="project-page__empty-action" :disabled="creating" @click="handleCreate">
        {{ t('project.createFirst') }}
      </button>
    </div>

    <div v-else class="project-page__list">
      <section v-for="project in projects" :key="project.id" class="project-card">
        <div class="project-card__head">
          <t-icon name="folder" size="18px" class="project-card__icon" />
          <span class="project-card__name" :title="project.name">{{ project.name }}</span>
          <span v-if="project.is_default" class="project-card__badge">{{ t('project.default') }}</span>
          <t-dropdown :options="menuOptions(project)" trigger="click" placement="bottom-right"
            @click="(data: { value: string }) => handleMenu(project, data.value)">
            <button type="button" class="project-card__more" :aria-label="t('project.more')">
              <t-icon name="more" size="16px" />
            </button>
          </t-dropdown>
        </div>

        <!-- 目录是项目的身份：一个项目对应一个本地目录，改名不会改目录，所以始终显示真实路径 -->
        <p class="project-card__dir" :title="project.host_dir">
          <bdi>{{ project.host_dir }}</bdi>
        </p>

        <div class="project-card__foot">
          <span class="project-card__count">
            {{ t('project.sessionsCount', { count: project.session_count }) }}
          </span>
          <span v-if="project.last_session_at" class="project-card__time">
            {{ t('project.lastActive', { time: formatTime(project.last_session_at) }) }}
          </span>
          <button type="button" class="project-card__open" @click="openProject(project)">
            {{ t('project.openProject') }}
            <t-icon name="chevron-right" size="14px" />
          </button>
        </div>
      </section>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue';
import { useRouter } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { DialogPlugin, MessagePlugin } from 'tdesign-vue-next';
import { useProjectStore } from '@/stores/project';
import { useConfirmDelete } from '@/components/settings/useConfirmDelete';
import type { Project } from '@/api/project';

const { t, locale } = useI18n();
const router = useRouter();
const projectStore = useProjectStore();
const confirmDelete = useConfirmDelete();

const projects = computed(() => projectStore.projects);
const creating = ref(false);

onMounted(() => {
  // 强制拉一次：从侧栏切过来时缓存可能已经落后于别处（比如侧栏刚建过项目）。
  void projectStore.fetchProjects(true);
});

function formatTime(raw?: string | null) {
  if (!raw) return '';
  const d = new Date(raw);
  if (Number.isNaN(d.getTime())) return '';
  return d.toLocaleString(locale.value, { hour12: false });
}

async function handleCreate() {
  if (creating.value) return;
  creating.value = true;
  try {
    // 创建入口复用系统目录选择框：项目与本地目录一一绑定，项目名默认取目录名。
    await projectStore.createFromPicker();
  } catch (err: any) {
    // 目录已授权给别的项目、目录未授权等情况都在这里给出人话提示。
    MessagePlugin.error(err?.message || t('project.createFailed'));
  } finally {
    creating.value = false;
  }
}

function menuOptions(project: Project) {
  return [
    { content: t('project.rename'), value: 'rename' },
    { content: project.is_default ? t('project.unsetDefault') : t('project.setDefault'), value: 'default' },
    { content: t('project.openInFinder'), value: 'reveal' },
    { content: t('project.delete'), value: 'delete', theme: 'error' as const },
  ];
}

/** 重命名：只改显示名，不动磁盘目录。用 TDesign 弹窗而非 window.prompt（见 handleMenu 注释）。 */
function renameProject(project: Project) {
  const draft = ref(project.name);
  const error = ref('');
  const dialog = DialogPlugin({
    header: t('project.rename'),
    body: () =>
      h('div', { class: 'project-rename' }, [
        h('input', {
          class: 'project-rename__input',
          value: draft.value,
          placeholder: t('project.renamePrompt'),
          maxlength: 200,
          autofocus: true,
          onInput: (e: Event) => {
            draft.value = (e.target as HTMLInputElement).value;
            error.value = '';
          },
        }),
        error.value ? h('p', { class: 'project-rename__error' }, error.value) : null,
      ]),
    confirmBtn: { content: t('common.confirm'), theme: 'primary' },
    cancelBtn: t('common.cancel'),
    onConfirm: async () => {
      const name = draft.value.trim();
      if (!name) {
        error.value = t('project.nameRequired');
        return;
      }
      if (name === project.name) {
        dialog.hide();
        return;
      }
      try {
        await projectStore.rename(project.id, name);
        dialog.hide();
      } catch (err: any) {
        error.value = err?.message || t('project.updateFailed');
      }
    },
  });
}

/** 删除项目确认。 */
function deleteWithConfirm(project: Project) {
  const hasSessions = project.session_count > 0;
  confirmDelete({
    title: t('project.delete'),
    body: hasSessions
      ? t('project.deleteConfirmWithSessions', { name: project.name, count: project.session_count })
      : t('project.deleteConfirm', { name: project.name }),
    onConfirm: async () => {
      try {
        await projectStore.remove(project.id, hasSessions ? 'move' : 'refuse');
      } catch (err: any) {
        // 后端会说明具体原因（例如项目下仍有会话），原样透出比通用文案有用。
        MessagePlugin.error(err?.message || t('project.deleteFailed'));
      }
    },
  });
}

async function handleMenu(project: Project, value: string) {
  switch (value) {
    case 'rename':
      // 不用 window.prompt：WKWebView 未实现 UIDelegate 时会静默返回 null。
      renameProject(project);
      break;
    case 'default':
      try {
        await projectStore.makeDefault(project.id, !project.is_default);
      } catch (err: any) {
        MessagePlugin.error(err?.message || t('project.updateFailed'));
      }
      break;
    case 'reveal':
      // 只打开文件管理器定位目录，不读不写内容。
      window.open(`file://${encodeURI(project.host_dir)}`);
      break;
    case 'delete':
      // 与侧栏一致：删项目不等于删会话，有会话时让它们脱离项目并保留。
      deleteWithConfirm(project);
      break;
  }
}

/** 项目卡片上「打开这个项目的会话」——跳到新对话页并预选该项目。 */
function openProject(project: Project) {
  void router.push({ path: '/platform/creatChat', query: { project: project.id } });
}
</script>

<style lang="less" scoped>
.project-page {
  display: flex;
  flex-direction: column;
  gap: 20px;
  width: 100%;
  max-width: var(--content-max-width, 1280px);
  margin: 0 auto;
  padding: 28px 32px 40px;
  box-sizing: border-box;
  overflow-y: auto;
}

.project-page__head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
}

.project-page__title {
  margin: 0 0 4px;
  font-size: 20px;
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.project-page__subtitle {
  margin: 0;
  font-size: 13px;
  color: var(--td-text-color-placeholder);
}

.project-page__create {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 32px;
  padding: 0 14px;
  border: 0;
  border-radius: var(--radius-sm, 4px);
  background: var(--td-brand-color);
  color: #fff;
  font-size: 13px;
  cursor: pointer;
  white-space: nowrap;

  &:hover:not(:disabled) {
    background: var(--td-brand-color-hover);
  }

  &:disabled {
    opacity: 0.6;
    cursor: default;
  }
}

.project-page__empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 12px;
  padding: 80px 20px;
  border: 1px dashed var(--td-component-border);
  border-radius: var(--radius-default, 6px);
}

.project-page__empty-icon {
  color: var(--td-text-color-placeholder);
}

.project-page__empty-text {
  margin: 0;
  font-size: 13px;
  color: var(--td-text-color-placeholder);
}

.project-page__empty-action {
  height: 32px;
  padding: 0 16px;
  border: 1px solid var(--td-brand-color);
  border-radius: var(--radius-sm, 4px);
  background: transparent;
  color: var(--td-brand-color);
  font-size: 13px;
  cursor: pointer;

  &:hover:not(:disabled) {
    background: var(--td-brand-color-light);
  }
}

.project-page__list {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 320px), 1fr));
  gap: 16px;
}

.project-card {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 16px;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--radius-default, 6px);
  background: var(--td-bg-color-container);
  transition: border-color 0.15s ease;

  &:hover {
    border-color: var(--td-brand-color-light-active, var(--td-brand-color));
  }
}

.project-card__head {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.project-card__icon {
  flex: 0 0 auto;
  color: var(--td-brand-color);
}

.project-card__name {
  flex: 0 1 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 14px;
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.project-card__badge {
  flex: 0 0 auto;
  padding: 0 6px;
  border-radius: 4px;
  background: var(--td-brand-color-light);
  color: var(--td-brand-color);
  font-size: 11px;
  line-height: 18px;
}

.project-card__more {
  flex: 0 0 auto;
  margin-left: auto;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  border: 0;
  border-radius: var(--radius-sm, 4px);
  background: transparent;
  color: var(--td-text-color-secondary);
  cursor: pointer;

  &:hover {
    background: var(--td-bg-color-container-hover);
    color: var(--td-text-color-primary);
  }
}

.project-card__dir {
  margin: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 12px;
  font-family: var(--td-font-family-mono, ui-monospace, SFMono-Regular, Menlo, monospace);
  color: var(--td-text-color-placeholder);
  // 超长路径让省略号出现在中间：只截掉中间部分，头（卷/挂载点）和尾（文件夹名）
  // 都保留。不用 direction: rtl —— 它会把开头的 "/" 甩到末尾显示成 "Test/"。
  direction: ltr;
  text-align: left;
}

.project-card__foot {
  display: flex;
  align-items: center;
  gap: 12px;
  font-size: 12px;
  color: var(--td-text-color-placeholder);
}

.project-card__count {
  color: var(--td-text-color-secondary);
}

.project-card__open {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  margin-left: auto;
  padding: 0;
  border: 0;
  background: transparent;
  color: var(--td-brand-color);
  font-size: 12px;
  cursor: pointer;

  &:hover {
    color: var(--td-brand-color-hover);
  }
}

/* 重命名弹窗内容由 DialogPlugin 挂到 body，scoped 样式命中不到，用 :global 声明。 */
:global(.project-rename__input) {
  width: 100%;
  height: 32px;
  padding: 0 8px;
  border: 1px solid var(--td-component-border);
  border-radius: var(--radius-sm, 4px);
  background: var(--td-bg-color-container);
  color: var(--td-text-color-primary);
  font-size: 14px;
  outline: none;
  box-sizing: border-box;
}

:global(.project-rename__input:focus) {
  border-color: var(--td-brand-color);
  box-shadow: 0 0 0 2px var(--td-brand-color-light);
}

:global(.project-rename__error) {
  margin: 6px 0 0;
  font-size: 12px;
  color: var(--td-error-color);
}
</style>