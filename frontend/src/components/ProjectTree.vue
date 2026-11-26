<template>
  <!--
    项目树：工作空间 → 项目 → 会话 的中间层。

    只负责「项目这一层的展示与操作」，会话行仍由 menu.vue 渲染，
    这样历史会话的批量选择、正在运行状态、未读标记等既有能力全部复用，
    不需要在这里重写一套会话列表。
  -->
  <div class="project-tree">
    <div class="project-tree__head">
      <span class="project-tree__title">{{ t('project.title') }}</span>
      <t-tooltip :content="t('project.create')" placement="right">
        <button type="button" class="project-tree__add" :disabled="creating" @click="handleCreate">
          <t-icon name="add" size="16px" />
        </button>
      </t-tooltip>
    </div>

    <!-- 首次使用：还没有项目时给一条明确的启动路径，而不是空白 -->
    <div v-if="!projectStore.loading && projects.length === 0" class="project-tree__empty">
      <p class="project-tree__empty-text">{{ t('project.empty') }}</p>
      <button type="button" class="project-tree__empty-action" :disabled="creating" @click="handleCreate">
        {{ t('project.createFirst') }}
      </button>
    </div>

    <div v-else class="project-tree__list">
      <div v-for="project in projects" :key="project.id" class="project-node">
        <div class="project-node__row" :class="{ 'project-node__row--open': isOpen(project.id) }">
          <button type="button" class="project-node__toggle" :aria-expanded="isOpen(project.id)"
            @click="toggle(project.id)">
            <!--
              单个图标槽，两个字形叠在一起做交叉淡入淡出：
                默认 / 悬停 → 文件夹（表达「这是一个项目/本地目录」）
                展开 / 悬停 → chevron-right（表达「点我展开」）
              「悬停」只在 :hover 的兜底规则里生效，鼠标有 hover 能力的设备才看得到；
              触屏设备点一下就是展开，走的是 is-open 分支。两者不冲突。
              两个字形都是 position: absolute 叠放，所以不会像之前那样
              「caret + folder」并排占掉 14+5+16 = 35px 宽。
            -->
            <span class="project-node__icon-slot">
                <!-- 尺寸由 CSS 变量 --sidebar-icon-size 驱动，与一级菜单图标同档（随密度档位 16/18/20 自动变） -->
                <t-icon class="project-node__glyph project-node__glyph--folder" name="folder" />
                <t-icon class="project-node__glyph project-node__glyph--arrow" name="chevron-right" />
              </span>
            <span class="project-node__name" :title="project.host_dir">{{ project.name }}</span>
            <span v-if="project.is_default" class="project-node__badge">{{ t('project.default') }}</span>
            <!--
              这里刻意不显示 session_count 徽标：数字来自 projects 接口的统计字段，
              与下方会话列表（来自 sessions 接口）是两次独立请求，新建会话后两者
              往往错位，出现「显示 0 条但下面列了 2 条」的矛盾。列表本身就在下方，
              数字没有额外信息量，去掉更不容易误导。
            -->
          </button>

          <!--
            在此项目下开新会话。这是「指定会话归属项目」的唯一入口：
            输入框里的项目下拉已经移除，切项目只能从这里的项目节点发起，
            会话一旦创建就绑死该项目的目录，中途不再提供切换。
          -->
          <!-- 两个操作按钮都默认隐藏，悬停/键盘聚焦项目行时才淡入。
               顺序固定为「更多 → 新建会话」：更多是低频的破坏性入口，
               放在左侧远离项目名，新建会话是高频正向操作，放最右侧、
               紧贴行尾，鼠标划到行右侧就能点到。 -->
          <t-dropdown :options="menuOptions(project)" trigger="click" placement="bottom-right"
            @click="(data: { value: string }) => handleMenu(project, data.value)">
            <button type="button" class="project-node__more" :aria-label="t('project.more')">
              <t-icon name="more" size="16px" />
            </button>
          </t-dropdown>

          <button type="button" class="project-node__new" :aria-label="t('project.newSession')"
            :title="t('project.newSession')" @click.stop="startSession(project)">
            <t-icon name="add" size="16px" />
          </button>
        </div>

        <div v-if="isOpen(project.id)" class="project-node__body">
          <slot name="sessions" :project="project" />
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { DialogPlugin, MessagePlugin } from 'tdesign-vue-next';
import { useProjectStore } from '@/stores/project';
import { useConfirmDelete } from '@/components/settings/useConfirmDelete';
import { revealHostProjectDir } from '@/utils/desktopProjectDir';
import type { Project } from '@/api/project';

const { t } = useI18n();
const projectStore = useProjectStore();
const confirmDelete = useConfirmDelete();

const props = defineProps<{
  /** 当前展开的项目 id，由父组件持有，切换会话时保持不变。 */
  expandedId?: string;
}>();

const emit = defineEmits<{
  (e: 'expand', id: string): void;
  (e: 'created', project: Project): void;
  (e: 'new-session', project: Project): void;
}>();

const projects = computed(() => projectStore.projects);
const creating = ref(false);

onMounted(() => {
  void projectStore.fetchProjects();
});

function isOpen(id: string) {
  return props.expandedId === id;
}

function toggle(id: string) {
  emit('expand', isOpen(id) ? '' : id);
}

function startSession(project: Project) {
  emit('new-session', project);
}

async function handleCreate() {
  if (creating.value) return;
  creating.value = true;
  try {
    const project = await projectStore.createFromPicker();
    if (project) {
      emit('created', project);
      // 新建后直接展开，用户接着就能在里面开新会话。
      emit('expand', project.id);
    }
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

async function handleMenu(project: Project, value: string) {
  switch (value) {
    case 'rename':
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
      //
      // 不能写成 window.open(`file://…`)：Wails 注入的脚本把这个方法改写成
      // 只放行 http/https，file:// 会落到浏览器原生实现，而 WKWebView 又
      // 不允许 webview 导航到 file://，结果就是点了没反应、也没有报错。
      // 改由后端进程以原生方式执行（macOS 上 open -R 会在 Finder 里选中它）。
      await revealProjectInFinder(project);
      break;
    case 'delete':
      deleteWithConfirm(project);
      break;
  }
}

/**
 * 在系统文件管理器中定位项目目录。
 *
 * 失败时把后端的原始原因透出来（目录已被撤销授权、系统没有文件管理器等），
 * 而不是让按钮点了没反应——之前的问题正是「静默失败」。
 */
async function revealProjectInFinder(project: Project) {
  try {
    const revealed = await revealHostProjectDir(project.host_dir);
    if (!revealed) {
      MessagePlugin.warning(t('project.revealFailed'));
    }
  } catch (err: any) {
    MessagePlugin.error(err?.message || t('project.revealFailed'));
  }
}

/**
 * 重命名：只改显示名，不动磁盘上的目录（一个项目绑定一个目录，改名不跟着改目录）。
 *
 * 这里刻意不用 window.prompt：macOS 的 WKWebView 在没有实现 UIDelegate 的
 * confirm/textInput 回调时会直接返回 null，弹窗根本不出现，用户看到的就是
 * 「点重命名没反应」。改用 TDesign 对话框，行为与平台无关。
 */
function renameProject(project: Project) {
  const draft = ref(project.name);
  const error = ref('');
  const dialog = DialogPlugin({
    header: t('project.rename'),
    // 默认按钮会把表单内容渲染成正文，这里只放输入框，提示语用 placeholder。
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
        // 空名不关闭弹窗，让用户就地补全。
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

/**
 * 删除项目：项目下还有会话时，先把「会话会怎样」说清楚再确认。
 * 删项目不等于删会话——会话会脱离项目并保留，避免误删用户的历史对话。
 */
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
        MessagePlugin.error(err?.message || t('project.deleteFailed'));
      }
    },
  });
}
</script>

<style lang="less" scoped>
/*
 * 视觉层级约定（侧栏里一共三层）：
 *   一级 · 主菜单（对话问答/知识库/…）      14px / 常规字重
 *   一级 · 项目名                          13px / 500 字重 / 主文字色 —— 与菜单项同级分量
 *   二级 · 项目内的会话                    12px / 常规字重 / 次要文字色 —— 明显弱于项目名
 * 所以这里刻意不把项目名做成 12px 的小灰字：项目是这一块真正的主体，
 * 真正需要「弱化」的是它下面的会话列表（见 menu.vue 的 .project-session-row）。
 */
.project-tree {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

/* 区块标题：小号、加字距、弱对比。它是「项目」这一整块的标签，
   不是可点击项，所以要比项目名再弱一档。 */
.project-tree__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  height: 26px;
  margin-bottom: 2px;
  padding: 0 2px 0 4px;
}

.project-tree__title {
  font-size: var(--app-text-xs);
  font-weight: 600;
  color: var(--td-text-color-placeholder);
  letter-spacing: 0.06em;
}

.project-tree__add {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  border: 0;
  border-radius: var(--radius-sm, 4px);
  background: transparent;
  color: var(--td-text-color-placeholder);
  cursor: pointer;
  transition: color 0.12s ease, background 0.12s ease;

  &:hover:not(:disabled) {
    background: var(--td-bg-color-container-hover);
    color: var(--td-brand-color);
  }

  &:disabled {
    opacity: 0.45;
    cursor: default;
  }
}

.project-tree__list {
  display: flex;
  flex-direction: column;
  gap: 1px;
}

.project-tree__empty {
  padding: 2px 4px 4px;
}

.project-tree__empty-text {
  margin: 0 0 8px;
  font-size: var(--app-text-sm);
  line-height: 1.6;
  color: var(--td-text-color-placeholder);
}

.project-tree__empty-action {
  width: 100%;
  height: 30px;
  border: 1px dashed var(--td-component-border);
  border-radius: var(--radius-sm, 4px);
  background: transparent;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-sm);
  cursor: pointer;
  transition: color 0.12s ease, border-color 0.12s ease;

  &:hover:not(:disabled) {
    border-color: var(--td-brand-color);
    color: var(--td-brand-color);
  }
}

/* 项目行：这一块的一级项，所以比别人高一档（32px）、字重 500、主文字色。 */
.project-node__row {
  display: flex;
  align-items: center;
  border-radius: var(--app-radius-sm);
  transition: background 0.12s ease;

  &:hover {
    background: var(--td-bg-color-container-hover);
  }
}

/* 展开态给一行极淡底色，把「项目 + 它的会话」圈成一组，
   否则子项目缩进后没有容器边界，会和下面的历史列表连成一片。 */
.project-node__row--open {
  background: var(--td-bg-color-container-hover);
}

.project-node__toggle {
  display: flex;
  flex: 1 1 auto;
  align-items: center;
  // 与一级主菜单同栅格：图标槽 --sidebar-icon-size + 间距 --sidebar-icon-gap
  gap: var(--sidebar-icon-gap, 8px);
  min-width: 0;
  // 行高跟主菜单的 --sidebar-row-height 走（标准档 34px），
  // 项目是这一块的一级项，就该和「对话问答」「系统设置」同样高。
  height: var(--sidebar-row-height, 34px);
  // 2px：项目区容器 .menu_box--projects 的内边距是 --menu-item-inline-pad(10px)，
  // 而一级菜单项自身左右各带 2px 内边距（图标落在 x=16）。补上这 2px 后，
  // 项目图标与上方菜单图标落在同一条竖线上。
  padding: 0 2px;
  border: 0;
  border-radius: var(--app-radius-sm);
  background: transparent;
  color: var(--td-text-color-primary);
  // 字号与一级主菜单 .menu_title 完全一致：14px / 500 / line-height 20px
  font-family: var(--app-font-family);
  font-size: var(--app-text-base);
  font-weight: 500;
  line-height: 20px;
  text-align: left;
  cursor: pointer;
}

/* 单图标槽：两个字形叠放，宽高与主菜单图标槽完全相同，
   这样项目名文字的起始 x 和主菜单文字是同一条竖线。 */
.project-node__icon-slot {
  position: relative;
  flex: 0 0 var(--sidebar-icon-size, 18px);
  width: var(--sidebar-icon-size, 18px);
  height: var(--sidebar-icon-size, 18px);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  color: var(--td-text-color-secondary);
}

/* 两个字形都以槽中心为原点旋转，交叉淡入淡出时不会横向跳动。 */
.project-node__glyph {
  position: absolute;
  inset: 0;
  margin: auto;
  font-size: var(--sidebar-icon-size, 18px);
  transition: opacity 0.16s ease, transform 0.16s ease;
}

/* 默认态：露文件夹 */
.project-node__glyph--folder {
  opacity: 1;
  transform: rotate(0deg) scale(1);
}

.project-node__glyph--arrow {
  opacity: 0;
  transform: rotate(-90deg) scale(0.75);
}

/*
 * 展开态：文件夹淡出、chevron 转正 90°指向下。
 * 箭头朝下是展开/折叠的通用语义（chevron-right 收起 → chevron-down 展开），
 * 比「箭头原地不动」能让人一眼看出当前处于展开状态。
 */
.project-node__row--open {
  .project-node__glyph--folder {
    opacity: 0;
    transform: rotate(-90deg) scale(0.75);
  }

  .project-node__glyph--arrow {
    opacity: 1;
    transform: rotate(90deg) scale(1);
  }

  .project-node__icon-slot {
    // 展开后图标转为品牌色，作为状态提示（与 active 会话行呼应）
    color: var(--td-brand-color);
  }
}

/*
 * 悬停态：未展开时文件夹让位给箭头，提示「点我会展开」。
 * 已展开时箭头保持朝下（由上面的 --open 规则给出），
 * 所以这里只处理「未展开」的悬停，用 :not 排除展开态，
 * 否则悬停会把已展开的朝下箭头扳回朝右，反而误导。
 * 用 @media (hover: hover) 包住，触屏设备没有 hover，
 * 点一下就是展开，走的是上面的 --open 分支。
 */
@media (hover: hover) {
  .project-node__row:not(.project-node__row--open) .project-node__toggle:hover {
    .project-node__glyph--folder {
      opacity: 0;
      transform: rotate(-90deg) scale(0.75);
    }

    .project-node__glyph--arrow {
      opacity: 1;
      transform: rotate(0deg) scale(1);
    }
  }
}

.project-node__name {
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.project-node__badge {
  flex: 0 0 auto;
  padding: 0 5px;
  border-radius: 3px;
  background: var(--td-brand-color-light);
  color: var(--td-brand-color);
  font-size: var(--app-text-2xs);
  font-weight: 500;
  line-height: 15px;
}

/* 注：会话数徽标（.project-node__count）已随模板一并移除，样式不再需要。 */

/*
 * 与 more 按钮同规格。
 * 默认隐藏，悬停 / 键盘聚焦项目行时才淡入，与 more 一致——
 * 两个按钮同时出现、同时消失，避免行内元素数量随悬停状态跳动，
 * 项目名也不会被常驻按钮挤窄。
 */
.project-node__new {
  flex: 0 0 auto;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  margin-right: 2px;
  border: 0;
  border-radius: var(--radius-sm, 4px);
  background: transparent;
  color: var(--td-text-color-secondary);
  cursor: pointer;
  opacity: 0;
  transition: opacity 0.12s ease, color 0.12s ease;

  .project-node__row:hover &,
  .project-node__row:focus-within & {
    opacity: 1;
  }

  &:hover {
    background: var(--td-bg-color-secondarycontainer-hover);
    color: var(--td-text-color-primary);
  }
}

.project-node__more {
  flex: 0 0 auto;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  border: 0;
  border-radius: var(--radius-sm, 4px);
  background: transparent;
  color: var(--td-text-color-secondary);
  cursor: pointer;
  opacity: 0;
  transition: opacity 0.12s ease;

  .project-node__row:hover &,
  .project-node__row:focus-within & {
    opacity: 1;
  }

  &:hover {
    background: var(--td-bg-color-container-hover);
    color: var(--td-text-color-primary);
  }
}

/* 展开后的容器：与上面项目行之间留 2px，与下方历史列表由外层分隔线隔开。 */
.project-node__body {
  padding: 2px 0 6px;
}

/* 项目绑定的本地目录：等宽小字，缩进到与项目名文字同列（4 + 14 + 5 + 16 + 5 = 44px），
   让「目录」承接在「项目名」正下方，而不是另起一列。
   长路径用中间省略（…/Desktop/knowledge_base）：尾部的目录名才是用户真正要认的部分，
   单纯 text-overflow: ellipsis 会把最有辨识度的结尾切掉，只留下 /Users/iamzhang...。
   上边距收紧到 0：目录是项目名的补充说明，应该贴着项目行，而不是浮在行与行之间。 */
// 重命名弹窗内容由 DialogPlugin 挂到 body 上，scoped 样式无法命中，故用 :deep 包裹的全局类名。
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

  &:focus {
    border-color: var(--td-brand-color);
    box-shadow: 0 0 0 2px var(--td-brand-color-light);
  }
}

:global(.project-rename__error) {
  margin: 6px 0 0;
  font-size: 12px;
  color: var(--td-error-color);
}
</style>