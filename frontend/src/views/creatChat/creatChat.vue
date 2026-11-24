<template>
    <div class="dialogue-wrap">
        <div class="dialogue-answers">
            <div class="dialogue-title" style="--wails-draggable: drag">
                <span style="--wails-draggable: drag">{{ $t('createChat.title') }}</span>
            </div>
            <!-- 推荐问题 -->
            <div ref="sqContainerRef" class="suggested-questions-container">
                <!-- 骨架屏占位 -->
                <div v-if="sqInitialLoad && suggestedQuestions.length === 0" class="suggested-questions-inner">
                    <div class="suggested-questions-title"><t-skeleton animation="gradient"
                            :row-col="[{ width: '120px', height: '14px' }]" /></div>
                    <div class="suggested-questions-grid">
                        <div v-for="n in 6" :key="'sq-skel-' + n" class="suggested-question-card sq-card-skeleton">
                            <t-skeleton animation="gradient"
                                :row-col="[{ width: '100%', height: '14px', type: 'rect' }]" />
                        </div>
                    </div>
                </div>
                <transition v-else appear name="sq-slide-fade" mode="out-in" @before-leave="onBeforeLeave"
                    @after-leave="onAfterLeave" @enter="onEnter" @after-enter="onQuestionsEntered">
                    <div v-if="suggestedQuestions.length > 0" :key="sqRenderKey" class="suggested-questions-inner">
                        <div class="suggested-questions-title-row">
                            <p class="suggested-questions-caption">
                                <span class="suggested-questions-title">{{ $t('chat.suggestedQuestions') }}</span>
                                <button type="button" class="suggested-questions-refresh" :disabled="sqLoading"
                                    :title="$t('chat.refreshSuggestedQuestions')"
                                    :aria-label="$t('chat.refreshSuggestedQuestions')" @click="fetchSuggestedQuestions(true)">
                                    <t-icon :name="sqLoading ? 'loading' : 'refresh'"
                                        :class="{ 'sq-refresh-spin': sqLoading }" />
                                </button>
                            </p>
                        </div>
                        <div class="suggested-questions-grid">
                            <div v-for="(item, index) in suggestedQuestions" :key="item.question"
                                class="suggested-question-card" :class="{ 'sq-card-visible': sqCardsRevealed }"
                                :style="{ transitionDelay: sqCardsRevealed ? `${index * 50}ms` : '0ms' }"
                                @click="handleSuggestedQuestionClick(item)">
                                <span class="suggested-question-text">{{ item.question }}</span>
                                <span v-if="item.source === 'faq'" class="suggested-question-badge faq">FAQ</span>
                            </div>
                        </div>
                    </div>
                </transition>
            </div>
            <div class="create-chat-composer">
                <!--
                    会话归属的项目。
                    这里刻意不做成可切换的下拉：一个项目绑定一个本地目录，
                    对话中途换项目等于换工作目录，上下文会错位。
                    已有项目时只做只读展示；没带项目时下面直接给出可选列表，
                    让用户点一下就能开始，而不是只丢一句「请从左侧新建会话」
                    却找不到入口（那个 + 图标只在悬停项目行时才出现）。
                -->
                <div v-if="hostSandboxEnabled && activeProject" class="project-dir-bar is-bound">
                    <t-icon name="folder" size="16px" class="project-dir-bar__icon" />
                    <span class="project-dir-bar__name" :title="activeProject.host_dir">{{ activeProject.name }}</span>
                </div>
                <div v-else-if="hostSandboxEnabled && projectsReady" class="project-dir-bar is-empty">
                    <div class="project-picker">
                        <p class="project-picker__hint">{{ t('project.pickFirst') }}</p>
                        <div v-if="projectStore.projects.length" class="project-picker__list">
                            <button v-for="project in projectStore.projects" :key="project.id" type="button"
                                class="project-picker__item" :title="project.host_dir"
                                @click="pickProject(project)">
                                <t-icon name="folder" size="16px" />
                                <span class="project-picker__name">{{ project.name }}</span>
                                <span v-if="project.is_default" class="project-picker__badge">{{ t('project.default') }}</span>
                            </button>
                        </div>
                        <button v-else type="button" class="project-picker__create" @click="createProjectThenStart">
                            {{ t('project.createFirst') }}
                        </button>
                    </div>
                </div>
                <InputField ref="inputFieldRef" @send-msg="sendMsg"></InputField>
            </div>
        </div>
    </div>

    <ContextualGuide tour="chat" :when="showChatContextualGuide" />

    <!-- 知识库编辑器（创建/编辑统一组件） -->
    <KnowledgeBaseEditorModal :visible="uiStore.showKBEditorModal" :mode="uiStore.kbEditorMode"
        :kb-id="uiStore.currentKBId || undefined" :initial-type="uiStore.kbEditorType"
        @update:visible="(val) => val ? null : uiStore.closeKBEditor()" @success="handleKBEditorSuccess" />
</template>
<script setup lang="ts">
import { ref, watch, onMounted, nextTick, computed } from 'vue';
import ContextualGuide from '@/components/ContextualGuide.vue';
import InputField from '@/components/Input-field.vue';
// OEM: 对话页顶部工具条（Workspace / 模型 / 召回参数）
import { createSessions } from "@/api/chat/index";
import { useProjectStore } from '@/stores/project';
import type { Project } from '@/api/project';
import { pickHostProjectDir, revokeHostProjectDir } from '@/utils/desktopProjectDir';
import { projectDirBasename, shouldRenderHostProjectSettings, withOptionalProjectDir } from '@/utils/hostWorkspace';
import { getSuggestedQuestions } from "@/api/agent/index";
import type { SuggestedQuestion } from "@/api/agent/index";
import { questionOriginFromSuggestion, type SendMessageOptions } from '@/utils/questionOrigin';
import { useMenuStore } from '@/stores/menu';
import { useSettingsStore } from '@/stores/settings';
import { useUIStore } from '@/stores/ui';
import { useDeploymentCapabilitiesStore } from '@/stores/deploymentCapabilities';
import { useRoute, useRouter, onBeforeRouteLeave } from 'vue-router';
import { MessagePlugin } from 'tdesign-vue-next';
import { useI18n } from 'vue-i18n';
import KnowledgeBaseEditorModal from '@/views/knowledge/KnowledgeBaseEditorModal.vue';
import { useKnowledgeBaseCreationNavigation } from '@/hooks/useKnowledgeBaseCreationNavigation';

const router = useRouter();
const route = useRoute();
const usemenuStore = useMenuStore();
const settingsStore = useSettingsStore();
onBeforeRouteLeave((to) => {
    // The first send carries the draft into its new session; abandoning the
    // composer must not make this a default for the next conversation.
    if (!to.path.startsWith('/platform/chat/') || !usemenuStore.isFirstSession) {
        settingsStore.reasoningEffortOverride = '';
    }
});
const uiStore = useUIStore();
const deploymentCapabilities = useDeploymentCapabilitiesStore();
const { t } = useI18n();
const { navigateToKnowledgeBaseList } = useKnowledgeBaseCreationNavigation();

const hostSandboxEnabled = computed(() =>
    // 项目归属看的是「有没有原生目录选择器」，不是本机沙箱：
    // Windows 版没有沙箱后端，但项目 / 项目文件 / 会话归属照常可用。
    shouldRenderHostProjectSettings(deploymentCapabilities.isSupported('settings.projectdir')),
);
const selectedProjectDir = ref('');
const projectStore = useProjectStore();
// 用户在本页显式选过的项目；为空时回落到默认项目，
// 所以「不选」也有归属，不会出现无项目的会话。
// 会话归属的项目由 URL 决定，而不是页内下拉：
// 从侧栏项目节点点「新建会话」会带 ?project=<id>，这里是它唯一的来源。
// 没有任何回落 —— 不传项目就不让发，避免会话在用户不知情下落进默认项目。
const pickedProjectId = ref('');
const activeProject = computed(
    () => projectStore.projects.find((p) => p.id === pickedProjectId.value) ?? null,
);
const activeProjectId = computed(() => activeProject.value?.id ?? '');
// 项目列表加载完成前不显示「请先选择项目」，否则会先闪一下错误提示
const projectsReady = ref(false);
const pickingProjectDir = ref(false);
const revokingProjectDir = ref(false);

const showChatContextualGuide = computed(() => {
    return route.name === 'globalCreatChat' || route.name === 'kbCreatChat';
});

// ===== 推荐问题 =====
const suggestedQuestions = ref<SuggestedQuestion[]>([]);
const sqLoading = ref(true);
// The skeleton reserves space only until the first answer arrives. Later
// refetches keep the current content in place, so the composer below does not
// jump each time the agent or @mention selection changes.
const sqInitialLoad = ref(true);
const sqCardsRevealed = ref(false);
const sqRenderKey = ref(0);
const sqContainerRef = ref<HTMLElement | null>(null);
let suggestedQuestionsFetchId = 0;
let lastSuggestedQuestionsKey = '';
let debounceTimer: ReturnType<typeof setTimeout> | null = null;

const sameSuggestedQuestions = (a: SuggestedQuestion[], b: SuggestedQuestion[]) =>
    a.length === b.length && a.every((item, i) => item.question === b[i].question && item.source === b[i].source);

// --- 高度平滑过渡钩子 ---
const onBeforeLeave = () => {
    const c = sqContainerRef.value;
    if (!c) return;
    c.style.height = c.offsetHeight + 'px';
    c.style.overflow = 'hidden';
};

const onAfterLeave = () => {
    const c = sqContainerRef.value;
    if (!c) return;
    if (suggestedQuestions.value.length === 0) {
        requestAnimationFrame(() => { c.style.height = '0px'; });
        c.addEventListener('transitionend', () => {
            c.style.height = '';
            c.style.overflow = '';
        }, { once: true });
    }
};

const onEnter = (el: Element) => {
    const c = sqContainerRef.value;
    if (!c) return;
    const startHeight = c.offsetHeight;
    c.style.height = 'auto';
    c.style.overflow = 'hidden';
    const targetHeight = c.offsetHeight;
    c.style.height = startHeight + 'px';
    requestAnimationFrame(() => {
        c.style.height = targetHeight + 'px';
    });
};

const onQuestionsEntered = () => {
    const c = sqContainerRef.value;
    if (c) {
        c.style.height = '';
        c.style.overflow = '';
    }
    nextTick(() => { sqCardsRevealed.value = true; });
};

const fetchSuggestedQuestions = async (force = false) => {
    const agentId = settingsStore.selectedAgentId;
    const params = settingsStore.getSuggestedQuestionsParams();
    const requestKey = JSON.stringify([agentId, params]);
    if (!force && requestKey === lastSuggestedQuestionsKey) return;
    lastSuggestedQuestionsKey = requestKey;

    const fetchId = ++suggestedQuestionsFetchId;
    sqLoading.value = true;
    try {
        if (!agentId) {
            suggestedQuestions.value = [];
            return;
        }
        const res = await getSuggestedQuestions(agentId, params);
        if (fetchId === suggestedQuestionsFetchId) {
            const next = res?.data?.questions || [];
            if (!sameSuggestedQuestions(suggestedQuestions.value, next)) {
                sqCardsRevealed.value = false;
                sqRenderKey.value++;
                suggestedQuestions.value = next;
            }
        }
    } catch (err) {
        console.warn('[SuggestedQuestions] Failed to fetch:', err);
        if (fetchId === suggestedQuestionsFetchId) {
            lastSuggestedQuestionsKey = '';
            suggestedQuestions.value = [];
        }
    } finally {
        if (fetchId === suggestedQuestionsFetchId) {
            sqLoading.value = false;
            sqInitialLoad.value = false;
        }
    }
};

// 防抖包装，切换知识库/文件时300ms内不重复请求
const debouncedFetch = () => {
    if (debounceTimer) clearTimeout(debounceTimer);
    debounceTimer = setTimeout(() => { fetchSuggestedQuestions(); }, 300);
};

// 监听 Agent / 知识库 / 文件 / 标签 @mention；MCP 与 Skill 不影响推荐范围
watch(
    () => ({
        agentId: settingsStore.selectedAgentId,
        kbs: settingsStore.settings.selectedKnowledgeBases,
        files: settingsStore.settings.selectedFiles,
        tags: settingsStore.settings.selectedTags,
    }),
    debouncedFetch,
    { deep: true },
);

onMounted(() => {
    void initProjects();
    fetchSuggestedQuestions();
});

/**
 * 拉项目列表，并识别从侧栏/项目页带过来的 `?project=<id>`。
 *
 * 项目 id 是会话归属的唯一来源。query 里的 id 可能已不存在（刚被删）或
 * 是伪造的，这两种情况都不回落默认项目，而是保持「未选项目」状态，
 * 由 UI 提示用户回侧栏重新选择。
 */
async function initProjects() {
    try {
        await projectStore.fetchProjects();
    } finally {
        projectsReady.value = true;
    }
    const wanted = typeof route.query.project === 'string' ? route.query.project : '';
    if (!wanted) return;
    const project = projectStore.projects.find((p) => p.id === wanted);
    if (!project) return;
    pickedProjectId.value = project.id;
    selectedProjectDir.value = project.host_dir;
}

const inputFieldRef = ref();

/**
 * 未选项目时，用户直接在新会话页点选一个已有项目。
 *
 * 只是把选中的项目记到本页状态，并把 ?project= 同步进 URL——这样刷新页面
 * 或从别处返回时仍然知道自己选的是哪个项目，行为与从侧栏带进来完全一致。
 */
function pickProject(project: Project) {
    pickedProjectId.value = project.id;
    selectedProjectDir.value = project.host_dir;
    void router.replace({ path: route.path, query: { ...route.query, project: project.id } });
}

/** 一个项目都没有时，走系统目录选择框建第一个项目，建好直接选中。 */
async function createProjectThenStart() {
    try {
        const created = await projectStore.createFromPicker();
        if (!created) return; // 用户取消选择，不是错误
        pickProject(created);
        expandedProjectRefresh();
    } catch (err) {
        console.error('[createChat] 创建项目失败', err);
        MessagePlugin.error(t('project.createFailed'));
    }
}

/** 新项目建好后通知侧栏刷新，否则左侧要刷新页面才看得到。 */
function expandedProjectRefresh() {
    void projectStore.fetchProjects(true);
}

// The suggestion's source rides with this send to the new session's first
// request, so the agent searches it before answering.
const handleSuggestedQuestionClick = (item: SuggestedQuestion) => {
    inputFieldRef.value?.triggerSend(item.question, { questionOrigin: questionOriginFromSuggestion(item) });
};

const sendMsg = (value: string, modelId: string, mentionedItems: any[], imageFiles: any[] = [], attachmentFiles: any[] = [], options: SendMessageOptions = {}) => {
    createNewSession(value, modelId, mentionedItems, imageFiles, attachmentFiles, options);
}

async function createNewSession(value: string, modelId: string, mentionedItems: any[] = [], imageFiles: any[] = [], attachmentFiles: any[] = [], options: SendMessageOptions = {}) {
    const selectedKbs = settingsStore.settings.selectedKnowledgeBases || [];
    const selectedFiles = settingsStore.settings.selectedFiles || [];

    // 构建 session 数据，包含 Agent 配置
    const sessionData: any = {};

    // 添加 Agent 配置（知识库信息在 agent_config 中）
    sessionData.agent_config = {
        enabled: true,
        max_iterations: settingsStore.agentConfig.maxIterations,
        temperature: settingsStore.agentConfig.temperature,
        knowledge_bases: selectedKbs,  // 所有选中的知识库
        knowledge_ids: selectedFiles,  // 所有选中的普通知识/文件
        allowed_tools: settingsStore.agentConfig.allowedTools
    };

    // 没有项目就不建会话：会话必须明确归属到某个项目的目录，
    // 否则不同工作目录的上下文会混在一起。后端同样会拒绝（400），
    // 这里前置拦截是为了不让用户白等一次请求失败。
    if (!activeProjectId.value) {
        MessagePlugin.warning(t('project.pickFirst'));
        return;
    }

    try {
        // project_id 显式传，让会话在侧栏立刻落到正确的项目节点下，
        // 而不是等下一次列表刷新才归位。目录跟随项目，避免两者不一致。
        const sessionPayload = withOptionalProjectDir(sessionData, selectedProjectDir.value);
        (sessionPayload as any).project_id = activeProjectId.value;
        const res = await createSessions(sessionPayload);
        if (res.data && res.data.id) {
            await navigateToSession(res.data.id, value, modelId, mentionedItems, imageFiles, attachmentFiles, options);
        } else {
            console.error('[createChat] Failed to create session');
            MessagePlugin.error(t('createChat.messages.createFailed'));
        }
    } catch (error) {
        console.error('[createChat] Create session error:', error);
        MessagePlugin.error(t('createChat.messages.createError'));
    }
}

const navigateToSession = async (sessionId: string, value: string, modelId: string, mentionedItems: any[], imageFiles: any[] = [], attachmentFiles: any[] = [], options: SendMessageOptions = {}) => {
    const now = new Date().toISOString();
    let obj = {
        title: t('createChat.newSessionTitle'),
        path: `chat/${sessionId}`,
        id: sessionId,
        isMore: false,
        isNoTitle: true,
        created_at: now,
        updated_at: now,
        // 必须带 project_id：侧栏按它把会话挂到对应项目节点下。
        // 漏掉这个字段时新会话会掉进「未归属项目」的历史分组，
        // 用户在项目里找不到刚建的对话。
        project_id: activeProjectId.value,
    };
    usemenuStore.updataMenuChildren(obj);
    usemenuStore.changeIsFirstSession(true);
    usemenuStore.changeFirstQuery(value, mentionedItems, modelId, imageFiles, attachmentFiles, options.questionOrigin ?? null);
    router.push(`/platform/chat/${sessionId}`);
}

const handleKBEditorSuccess = (kbId: string) => {
    navigateToKnowledgeBaseList(kbId)
}

</script>
<style lang="less" scoped>
.dialogue-wrap {
    flex: 1;
    display: flex;
    justify-content: center;
    align-items: center;
    // position: relative;
}

// 收窄到 680px，与有消息的会话页（chat/index.vue 的 .msg_list / .input-container）
// 保持同一列宽，避免新建页输入框比会话页更宽、切换时横向跳动。
.dialogue-answers {
    display: flex;
    flex-flow: column;
    align-items: center;
    width: 100%;
    max-width: 680px;
    gap: 24px;

    :deep(.answers-input) {
        position: static;
        transform: translateX(0);
    }
}

.create-chat-composer {
    display: flex;
    flex-direction: column;
    align-items: stretch;
    gap: 8px;
    width: 100%;
}

/*
 * 项目标识条：只是「这个会话属于哪个项目」的只读提示，不是切换控件。
 * 已选项目时常驻显示项目名（鼠标悬停可看完整目录）；
 * 未选项目时提示去哪选，并且此时不允许发送。
 */
.project-dir-bar {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    align-self: flex-start;
    max-width: 100%;
    padding: 0 2px;
    color: var(--td-text-color-secondary);
    font-size: var(--app-text-sm);
    box-sizing: border-box;

    &.is-empty {
        color: var(--td-text-color-placeholder);
    }
}

.project-dir-bar__icon {
    flex: 0 0 auto;
    color: var(--td-text-color-placeholder);
}

.project-dir-bar__name {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--td-text-color-primary);
    font-weight: 500;
}

.project-dir-bar__hint {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
}

/*
 * 未选项目时的可选项目列表。
 * 之前这里只有一行灰字提示，而真正的入口是侧栏项目行的悬停 + 图标，
 * 用户找不到，以为「无法新建对话」。现在把项目直接铺在输入框上方，
 * 点一下就用该项目开始对话，路径最短。
 */
.project-picker {
    display: flex;
    flex-direction: column;
    gap: 8px;
    width: 100%;
    padding: 10px 12px;
    border: 1px solid var(--td-component-stroke);
    border-radius: var(--app-radius-md, 6px);
    background: var(--td-bg-color-container);
    box-sizing: border-box;
}

.project-picker__hint {
    margin: 0;
    font-size: var(--app-text-sm);
    color: var(--td-text-color-placeholder);
}

.project-picker__list {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
}

.project-picker__item {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    max-width: 100%;
    padding: 5px 10px;
    border: 1px solid var(--td-component-stroke);
    border-radius: var(--app-radius-md, 6px);
    background: var(--td-bg-color-container);
    color: var(--td-text-color-primary);
    font-size: var(--app-text-sm);
    cursor: pointer;
    transition: background 0.12s ease, border-color 0.12s ease;

    &:hover {
        background: var(--td-bg-color-secondarycontainer-hover);
        border-color: var(--td-brand-color);
    }
}

.project-picker__name {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
}

.project-picker__badge {
    flex: 0 0 auto;
    padding: 0 4px;
    border-radius: 3px;
    background: var(--td-brand-color-light);
    color: var(--td-brand-color);
    font-size: 11px;
    line-height: 16px;
}

.project-picker__create {
    align-self: flex-start;
    padding: 5px 12px;
    border: 1px dashed var(--td-component-stroke);
    border-radius: var(--app-radius-md, 6px);
    background: transparent;
    color: var(--td-brand-color);
    font-size: var(--app-text-sm);
    cursor: pointer;

    &:hover {
        border-color: var(--td-brand-color);
        background: var(--td-bg-color-secondarycontainer-hover);
    }
}

.dialogue-title {
    display: flex;
    color: var(--td-text-color-primary);
    font-family: var(--app-font-family);
    /*
     * 字号响应式：原来写死 28px，760px 视口下这句欢迎语宽约 680px，
     * 而内容区只有 540px —— flex 容器不会自动缩字，文字直接顶出右边界
     * 被裁掉（表现为"整块内容偏右且被截断"）。
     * 改成 clamp 后随视口收缩，最小 20px，宽屏仍到 28px。
     */
    font-size: clamp(20px, 2.4vw, 28px);
    line-height: 1.35;
    font-weight: 600;
    align-items: center;
    margin-bottom: 0;
    // 允许换行：窄屏下宁可折成两行，也不要溢出被裁
    flex-wrap: wrap;
    min-width: 0;
    overflow-wrap: anywhere;

    .icon {
        display: flex;
        width: 32px;
        height: 32px;
        justify-content: center;
        align-items: center;
        border-radius: var(--app-radius-sm);
        background: var(--td-bg-color-container);
        box-shadow: var(--td-shadow-1);
        margin-right: 12px;

        .logo_img {
            height: 24px;
            width: 24px;
        }
    }
}

@import '../../components/css/suggested-questions.less';

@keyframes skeletonFadeIn {
    from {
        opacity: 0;
    }

    to {
        opacity: 1;
    }
}

.suggested-questions-container {
    /*
     * 窄屏修复：父级 .dialogue-answers 设了 align-items:center，本元素又只给了
     * max-width，于是宽度由「内容」而不是「可用空间」决定 —— 内容需要 572px 时
     * 它就长到 572px，超出 540px 的容器后居中溢出，右侧被裁、左侧留白，
     * 表现为整块对话内容"偏移且截断"（760px 视口实测）。
     * 加 width:100% + min-width:0 后，它按可用宽度收缩，再由内部网格降列。
     */
    width: 100%;
    min-width: 0;
    max-width: 680px;
    box-sizing: border-box;
    margin: 0;
    padding: 0 16px;
    transition: height 0.35s @suggested-ease;
}

.suggested-questions-inner {
    min-width: 0;
    animation: skeletonFadeIn 0.3s ease-out;
}

.sq-slide-fade-enter-active {
    transition: opacity 0.35s @suggested-ease, transform 0.35s @suggested-ease;
}

.sq-slide-fade-leave-active {
    transition: opacity var(--app-motion-fast) cubic-bezier(0.4, 0, 1, 1),
        transform 0.15s cubic-bezier(0.4, 0, 1, 1);
}

.sq-slide-fade-enter-from {
    opacity: 0;
    transform: translateY(10px);
}

.sq-slide-fade-leave-to {
    opacity: 0;
    transform: translateY(-4px);
}

.suggested-question-card {
    opacity: 0;
    transform: translateY(8px) scale(0.97);
    transition:
        opacity 0.35s @suggested-ease,
        transform 0.35s @suggested-ease,
        background 0.2s @suggested-ease,
        border-color 0.25s @suggested-ease,
        box-shadow 0.25s @suggested-ease;

    &.sq-card-skeleton {
        opacity: 1;
        transform: none;
    }

    &.sq-card-visible {
        opacity: 1;
        transform: translateY(0) scale(1);
    }

    &:not(.sq-card-skeleton):active {
        transform: scale(0.98);
    }

    &.sq-card-visible:active {
        transform: scale(0.98);
    }
}
</style>
<style lang="less">
.del-menu-popup {
    z-index: 99 !important;

    .t-popup__content {
        width: 100px;
        height: 40px;
        line-height: 30px;
        padding-left: 14px;
        cursor: pointer;
        margin-top: 4px !important;

    }
}
</style>
