<template>
    <div class="aside_box" :class="{ 'aside_box--collapsed': uiStore.sidebarCollapsed, 'aside_box--resizing': uiStore.sidebarResizing }" :style="sidebarStyleVars" :data-density="sidebarDensity">
        <!--
          OEM：窗口控制条（仅桌面端展开态渲染）。
          内嵌标题栏（mac.TitleBarHiddenInset）下，macOS 的红黄绿按钮由系统绘制在
          窗口左上角、浮在 webview 之上，占据约 76x28 的区域。
          这一行把「折叠侧栏 / 全局搜索」放到红绿灯同一水平线的右侧空白处，
          既利用了原本整条 38px 都是空白的顶部区域，又把这两个按钮从品牌行挪走，
          品牌名和版本号因此不再被挤成「知识工...」。

          为什么用 margin-left 让位而不是 padding：红绿灯是系统绘制的，
          我们只能用空白让开它；76px ≈ 3 个按钮 + 间距 + 左边距的实测总宽。
        -->
        <div class="sidebar_winctl" v-if="!uiStore.sidebarCollapsed">
            <div class="sidebar_winctl__spacer" aria-hidden="true"></div>
            <div class="logo_actions">
                <t-tooltip v-if="assistantStore.entry === 'sidebar'" :content="assistantName" placement="bottom">
                    <AssistantButton data-guide="assistant-entry" icon-only :icon-size="18" :label="assistantName"
                        :active="assistantStore.visible" @click="assistantStore.toggle()" />
                </t-tooltip>
                <t-tooltip placement="bottom">
                    <template #content>
                        <span class="cmdk-tip">
                            <span class="cmdk-tip-label">{{ t('menu.search') }}</span>
                            <span class="cmdk-tip-keys">{{ cmdModKeyLabel }}K</span>
                        </span>
                    </template>
                    <div class="header-icon-btn" @click="commandPaletteStore.openPalette('')"
                        :aria-label="t('menu.search')">
                        <img class="header-icon-img" :src="getImgSrc('search.svg')" alt="">
                    </div>
                </t-tooltip>
                <div class="sidebar-toggle" @click="uiStore.toggleSidebar" :title="t('menu.collapseSidebar')">
                    <svg viewBox="0 0 20 20" width="16" height="16" fill="none" xmlns="http://www.w3.org/2000/svg">
                        <rect x="1.5" y="1.5" width="17" height="17" rx="3" stroke="currentColor" stroke-width="1.5" />
                        <line x1="7.5" y1="1.5" x2="7.5" y2="18.5" stroke="currentColor" stroke-width="1.5" />
                        <line x1="4" y1="7.5" x2="4" y2="12.5" stroke="currentColor" stroke-width="1.5"
                            stroke-linecap="round" />
                    </svg>
                </div>
            </div>
        </div>

        <!-- 展开时：Logo + 产品名 + 版本号 -->
        <div class="logo_row" v-if="!uiStore.sidebarCollapsed">
            <!--
              OEM 白标点位：Logo 与产品名。
              替换方式：把 frontend/public/oem/logo.svg（或 logo.png）换成客户素材即可，
              无需改代码。产品名在 src/config/oem.ts 的 BRAND.name 中统一配置。
            -->
            <div class="logo_box" @click="router.push(OEM_HOME_ROUTE)" style="cursor: pointer;">
                <!--
                  圆形 badge：品牌图标统一收进正圆容器里。
                  为什么不用裸 <img>：素材是 228x202 的竖版透明 PNG，直接放进
                  侧栏会按比例缩成一个又窄又高的矩形，和右侧的品牌名基线对不齐。
                  圆形容器 + object-fit: contain 让任意比例的素材都落在同一个视觉
                  尺寸内，换素材不需要再调宽度。
                  白底 + 1px 描边是为了托住透明边角（图案不是满幅的），
                  深色模式下描边与底色跟随主题变量自动切换。
                -->
                <span v-if="!logoFailed" class="logo-badge">
                    <img class="logo-badge__img" :src="BRAND.logo" :alt="BRAND.name" @error="logoFailed = true">
                </span>
                <span v-else class="logo-badge logo-badge--fallback">{{ BRAND.shortName.slice(0, 1) }}</span>
                <!--
                  产品名与图标并排。图标只负责"图形识别"，名称由文字承担：
                  这样换成任何比例的素材都不会出现"字被压扁看不清"的问题，
                  也避免用户要求改品牌名时还得重做一张图。
                  窄档位下自动隐藏，只留图标（图标已有 title/alt 提示）。
                -->
                <span class="logo-name" v-if="sidebarDensity !== 'compact'">
                    <!--
                      OEM：改回内嵌标题栏后系统标题栏被隐藏，版本号信息不能丢。
                      但侧栏顶部这一行只有约 250px，图标 + 品牌名 + 3 个操作按钮
                      已经把空间占满（实测品牌名只剩 62px 会被截成「知识工...」），
                      所以版本号放在品牌名下方第二行，用弱化的小字号，不抢主视觉。
                      版本号由 Go 侧 OnDomReady 注入 window.__WK_APP_VERSION__，
                      浏览器环境没有该变量时整块不渲染。
                    -->
                    <span class="logo-name__text">{{ BRAND.name }}</span>
                    <span class="logo-name__version" v-if="appVersion">{{ appVersion }}</span>
                </span>
            </div>
        </div>
        <!-- 折叠时：展开按钮 -->
        <t-tooltip v-else :content="t('menu.expandSidebar')" placement="right">
            <div class="menu_item sidebar-toggle-item" @click="uiStore.toggleSidebar">
                <div class="menu_item-box">
                    <div class="menu_icon">
                        <svg class="icon icon--toggle" viewBox="0 0 20 20" width="16" height="16" fill="none"
                            xmlns="http://www.w3.org/2000/svg">
                            <rect x="1.5" y="1.5" width="17" height="17" rx="3" stroke="currentColor"
                                stroke-width="1.5" />
                            <line x1="7.5" y1="1.5" x2="7.5" y2="18.5" stroke="currentColor" stroke-width="1.5" />
                            <line x1="5" y1="10" x2="3" y2="8" stroke="currentColor" stroke-width="1.5"
                                stroke-linecap="round" />
                            <line x1="5" y1="10" x2="3" y2="12" stroke="currentColor" stroke-width="1.5"
                                stroke-linecap="round" />
                        </svg>
                    </div>
                </div>
            </div>
        </t-tooltip>

        <!--
            项目树已从侧栏移除，提升为一级菜单项「项目」（位于「系统设置」下方）。
            侧栏保留历史会话列表；项目内的会话在项目页查看与管理。
        -->

        <!-- 侧栏边缘拖拽调宽，拖窄时自动收缩。
             命中区收窄到 16px：默认的 52px 会盖住会话行尾的「更多」按钮，
             导致重命名/删除点击落在手柄上而不是按钮上。 -->
        <PanelResizeHandle edge="right" :label="t('knowledgeStages.resizeDrawer')" :hit-width="16"
            :value="uiStore.sidebarDisplayWidth" :min="SIDEBAR_COLLAPSED_WIDTH" :max="SIDEBAR_MAX_WIDTH"
            @start="startSidebarResize" @resize="resizeSidebar" @end="uiStore.sidebarResizing = false" />

        <!-- 上半部分：新对话吸顶 + 知识库/智能体/共享空间/历史会话随滚动一起滚走 -->
        <div class="menu_top" ref="scrollContainer" @scroll="handleScroll">
            <!-- 全局搜索入口：点击打开命令面板（⌘K）。展开态移至顶部 logo_row 的图标按钮；
                 折叠态在此处保留为图标项 + 深色 tooltip。 -->
            <div class="menu_box menu_box--cmdk" v-if="uiStore.sidebarCollapsed">
                <t-tooltip placement="right">
                    <template #content>
                        <span class="cmdk-tip">
                            <span class="cmdk-tip-label">{{ t('menu.search') }}</span>
                            <span class="cmdk-tip-keys">{{ cmdModKeyLabel }}K</span>
                        </span>
                    </template>
                    <div class="menu_item menu_item--cmdk" @click="commandPaletteStore.openPalette('')">
                        <div class="menu_item-box">
                            <div class="menu_icon">
                                <img class="icon" :src="getImgSrc('search.svg')" alt="">
                            </div>
                        </div>
                    </div>
                </t-tooltip>
                <t-tooltip v-if="assistantStore.entry === 'sidebar'" :content="assistantName" placement="right">
                    <AssistantButton data-guide="assistant-entry" class="assistant-sidebar-entry" icon-only :icon-size="18"
                        :label="assistantName" :active="assistantStore.visible" @click="assistantStore.toggle()" />
                </t-tooltip>
            </div>
            <!--
                项目树：紧跟在「系统设置」菜单项之后渲染，但它是独立的块（不在
                topMenuItems 循环里），这样才能带着自己的标题、会话插槽和展开态。
                用 v-if 判断「上一个菜单项是 settings」，保证顺序由菜单配置驱动，
                将来调整菜单顺序时项目树会自动跟着走。
            -->
            <template v-for="(item, index) in topMenuItems" :key="index">
                <div class="menu_box" :class="{ 'menu_box--sticky': item.children && !uiStore.sidebarCollapsed }">
                <t-tooltip :content="item.title" placement="right" :disabled="!uiStore.sidebarCollapsed">
                    <div @click="handleMenuClick(item.path)" @mouseenter="mouseenteMenu(item.path)"
                        @mouseleave="mouseleaveMenu(item.path)" :data-guide="`nav-${item.path}`"
                        :class="['menu_item', item.childrenPath && item.childrenPath == currentpath ? 'menu_item_c_active' : isMenuItemActive(item.path) ? 'menu_item_active' : '']">
                        <div class="menu_item-box">
                            <div class="menu_icon" :class="`menu_icon--${item.icon}`">
                                <img class="icon"
                                    :src="getImgSrc(item.icon == 'zhishiku' ? knowledgeIcon : item.icon == 'agent' ? agentIcon : item.icon == 'artifact' ? artifactIcon : item.icon == 'automation' ? automationIcon : item.icon == 'toolbox' ? toolboxIcon : item.icon == 'organization' ? organizationIcon : item.icon == 'setting' ? settingIcon : prefixIcon)"
                                    alt="">
                            </div>
                            <template v-if="!uiStore.sidebarCollapsed">
                                <span class="menu_title" :title="item.title">{{ item.title }}</span>
                                <span v-if="item.path === 'organizations' && orgStore.totalPendingJoinRequestCount > 0"
                                    class="menu-pending-badge"
                                    :title="t('organization.settings.pendingJoinRequestsBadge')">{{
                                        orgStore.totalPendingJoinRequestCount }}</span>
                                <span v-if="item.path === 'toolbox' && toolboxPreview.length" class="menu-toolbox-stack"
                                    :title="toolboxPreview.map((tool) => tool.key === 'browserconnection' && browserStackStatus
                                        ? `${t(tool.title)} (${t(`localBrowser.${browserStackStatus}`)})` : t(tool.title)).join(' · ')">
                                    <span v-for="tool in toolboxPreview" :key="tool.key" class="menu-toolbox-stack__item">
                                        <template v-if="tool.key === 'browserconnection'">
                                            <BrowserIcon width="12" height="12" />
                                            <i v-if="browserStackStatus" class="menu-toolbox-stack__status"
                                                :class="`is-${browserStackStatus}`" aria-hidden="true" />
                                        </template>
                                        <t-icon v-else :name="tool.icon" size="12px" />
                                    </span>
                                </span>
                            </template>
                        </div>
                    </div>
                </t-tooltip>
            </div>

                <!--
                    项目树插在「系统设置」下方：保持「项目 → 项目内会话」的树状展开。
                    上方的功能项与下方项目区是两套不同性质的东西（功能入口 vs 资源树），
                    所以在两者之间放一条分隔线 + 上下留白，视觉上分成两块。
                -->
                <div v-if="item.path === 'settings' && !uiStore.sidebarCollapsed" class="menu_box menu_box--projects">
                    <div class="menu_box__divider" role="separator" aria-hidden="true"></div>
                    <ProjectTree :expanded-id="expandedProjectId" @expand="onProjectExpand" @created="onProjectCreated"
                        @new-session="onProjectNewSession">
                        <template #sessions="{ project }">
                            <!--
                                项目下的会话行。复用历史列表同一套 SessionSidebarRow 与菜单动作，
                                所以置顶/改名/删除/运行中/未读这些能力在两处行为完全一致。
                                这里不再按日期分组：项目本身已经是分组，多套分组会让缩进层级过深。
                            -->
                            <div class="project-sessions-slot" :data-project-id="project.id">
                                <template v-if="sessionsByProject[project.id]?.length">
                                    <div v-for="subitem in sessionsByProject[project.id]" :key="subitem.id"
                                        class="submenu_item_p session-chat-row project-session-row"
                                        :data-session-id="subitem.id" :class="{
                                            'session-chat-row--active': !batchMode && subitem.path === currentSecondpath,
                                            'session-chat-row--selected': batchMode && batchSelectedIds.includes(subitem.id),
                                            'session-chat-row--revealed': revealedSessionId === subitem.id,
                                        }">
                                        <div class="session-list-row session-list-row--flat">
                                            <div class="session-list-row__body">
                                                <SessionSidebarRow :item="subitem" :batch-mode="batchMode"
                                                    :running="Boolean(sessionActivityEntries[subitem.id])"
                                                    :unread="Boolean(sessionUnread[subitem.id])"
                                                    :active-path="currentSecondpath" :selected-ids="batchSelectedIds"
                                                    :menu-options="buildSessionMenuOptions(subitem)"
                                                    @navigate="gotopage(subitem.path)"
                                                    @toggle-select="toggleBatchSelect(subitem.id)"
                                                    @menu-click="handleSessionMenuClick($event, subitem)"
                                                    @rename-submit="renameSessionTitle(subitem, $event.title)"
                                                    @hover-in="mouseenteBotDownr(subitem.id)"
                                                    @hover-out="mouseleaveBotDown" />
                                            </div>
                                        </div>
                                    </div>
                                </template>
                                <div v-else class="project-sessions-empty">{{ t('menu.noSessions') }}</div>
                            </div>
                        </template>
                    </ProjectTree>
                </div>
            </template>

            <!-- 历史会话：按来源筛选后统一按日期分组展示 -->
            <div class="submenu" v-if="!uiStore.sidebarCollapsed">
                <!-- Stable, always-mounted source filter: reserving its row here
                     (instead of embedding it in the first date group, which
                     appears/disappears while a bucket loads) prevents the
                     top-right control from jumping when switching session type. -->
                <div v-if="showSessionSourceFilter && !batchMode" class="session-list-scope-header">
                    <SessionSourceFilter inline :emphasized="sessionScopeFilterPinned" :sources="sessionSourceOptions"
                        :current="activeSessionBucketKey" @select="switchSessionBucket" />
                </div>
                <template v-if="sessionListBooting && !hasAnySession">
                    <div v-for="n in 4" :key="'skel-' + n" class="submenu_item_p session-chat-row">
                        <div class="session-list-row session-list-row--flat">
                            <t-skeleton animation="gradient" class="session-list-row__body"
                                :row-col="[{ width: '100%', height: '14px' }]" />
                        </div>
                    </div>
                </template>

                <div v-else class="session-filtered-list">
                    <template
                        v-if="activeBucket?.loading && !activeBucket.loaded && filteredGroupedSessions.length === 0">
                        <div v-for="n in 4" :key="'bucket-skel-' + n" class="submenu_item_p session-chat-row">
                            <div class="session-list-row session-list-row--flat">
                                <t-skeleton animation="gradient" class="session-list-row__body"
                                    :row-col="[{ width: '100%', height: '14px' }]" />
                            </div>
                        </div>
                    </template>
                    <template v-else-if="activeBucket?.loaded && filteredGroupedSessions.length === 0">
                        <!-- 历史区只列未归属项目的会话，所以这里可能「加载完成但为空」，
                             此时会话其实都在上面的项目树里，用 noSessions 会让人误以为丢了。 -->
                        <div class="submenu_empty">{{ t('project.historyEmpty') }}</div>
                    </template>
                    <template v-else>
                        <template v-for="group in filteredGroupedSessions" :key="group.key">
                            <div v-if="group.label" class="timeline_header session-list-row session-list-row--flat">
                                <span class="session-list-row__body">
                                    <span class="timeline_header-label">{{ group.label }}</span>
                                </span>
                            </div>
                            <div v-for="subitem in group.items" :key="subitem.id"
                                class="submenu_item_p session-chat-row" :data-session-id="subitem.id" :class="{
                                    'session-chat-row--active': !batchMode && subitem.path === currentSecondpath,
                                    'session-chat-row--selected': batchMode && batchSelectedIds.includes(subitem.id),
                                    'session-chat-row--revealed': revealedSessionId === subitem.id,
                                }">
                                <div class="session-list-row session-list-row--flat">
                                    <div class="session-list-row__body">
                                        <SessionSidebarRow :item="subitem" :batch-mode="batchMode"
                                            :running="Boolean(sessionActivityEntries[subitem.id])"
                                            :unread="Boolean(sessionUnread[subitem.id])"
                                            :active-path="currentSecondpath" :selected-ids="batchSelectedIds"
                                            :menu-options="buildSessionMenuOptions(subitem)"
                                            @navigate="gotopage(subitem.path)"
                                            @toggle-select="toggleBatchSelect(subitem.id)"
                                            @menu-click="handleSessionMenuClick($event, subitem)"
                                            @rename-submit="renameSessionTitle(subitem, $event.title)"
                                            @hover-in="mouseenteBotDownr(subitem.id)" @hover-out="mouseleaveBotDown" />
                                    </div>
                                </div>
                            </div>
                        </template>
                        <div v-if="activeBucket?.loading && filteredGroupedSessions.length > 0"
                            class="session-list-loading session-list-row session-list-row--flat">
                            <span class="session-list-row__body">
                                <t-loading size="small" />
                            </span>
                        </div>
                    </template>
                </div>
            </div>
        </div>

        <!-- 批量管理底部操作条：固定在侧栏底部、授权条上方 -->
        <div v-if="batchMode && !uiStore.sidebarCollapsed" class="batch-inline-footer">
            <div class="batch-footer-left">
                <t-checkbox :checked="isAllBatchSelected" :indeterminate="isBatchIndeterminate"
                    :disabled="batchDeleting" @change="toggleBatchSelectAll">
                    {{ t('batchManage.selectAll') }}
                </t-checkbox>
            </div>
            <div class="batch-footer-right">
                <t-button size="small" variant="text" :disabled="batchDeleting" @click="exitBatchMode">
                    {{ t('batchManage.cancel') }}
                </t-button>
                <t-button size="small" theme="danger" variant="base" :disabled="batchDeleting || batchSelectedIds.length === 0"
                    :loading="batchDeleting" @click="handleInlineBatchDelete">
                    {{ t('batchManage.delete') }}{{ batchSelectedIds.length > 0 ? `(${batchDisplayCount})` : '' }}
                </t-button>
            </div>
        </div>

        <!-- 侧栏底部：只有授权状态入口，没有头像/用户名/退出登录 -->
        <div class="menu_bottom">
            <SidebarLicenseBar />
        </div>

    </div>
</template>

<script setup lang="ts">
import { storeToRefs } from 'pinia';
import { onMounted, onUnmounted, watch, computed, ref, h, nextTick } from 'vue';
import { useRoute, useRouter } from 'vue-router';
// OEM: 白标配置（Logo / 产品名 / 首页路由）
import { BRAND, OEM_HOME_ROUTE, SIDEBAR_DENSITY, pickSidebarDensity, resolveSidebarWidth, type SidebarDensityKey } from '@/config/oem';
import { getSessionsList, batchDelSessions, deleteAllSessions, getSession } from "@/api/chat/index";
import { useChatResourcesStore } from '@/stores/chatResources';
import { listAllIMChannels } from '@/api/agent/index';
import SessionSidebarRow from './SessionSidebarRow.vue';
// OEM：底部只保留授权入口（没有账号体系）
import SidebarLicenseBar from './SidebarLicenseBar.vue';
import PanelResizeHandle from './PanelResizeHandle.vue';
import { SIDEBAR_COLLAPSED_WIDTH, SIDEBAR_MIN_WIDTH, SIDEBAR_MAX_WIDTH } from '@/utils/sidebarWidth';
import {
    clearSession,
    removeSession,
    renameSession,
    SESSION_MUTATION_EVENT,
    setSessionPinned,
    type SessionMutationDetail,
} from './sessionMutations';
import SessionSourceFilter from './SessionSourceFilter.vue';
import {
    SIDEBAR_BUCKET_PAGE_SIZE,
    applyBucketCountProbe,
    buildBucketDefinitions,
    bucketHasMore,
    bucketVisible,
    createEmptyBucket,
    flattenBucketItems,
    isChannelBucket,
    isChannelBucketKey,
    mergeBucketPage,
    prependSessionToWebBucket,
    removeSessionFromBuckets,
    type SidebarSessionBucket,
} from './sessionSidebarBuckets';
import type { SessionForGrouping } from './sessionGrouping';
import { listAllEmbedChannels } from '@/api/embed/index';
import {
    classifyDateBucket,
    configuredPlatforms,
    groupSessionsByDate,
    originGroupKey,
    resolveSessionOrigin,
    type DateBucketKey,
} from './sessionGrouping';
import {
    DEFAULT_SESSION_BUCKET_KEY,
    buildSessionSourceOptions,
    findSessionBucketKey,
    shouldShowSessionSourceFilter,
} from './sessionSidebarSourceFilter';
import { useMenuStore } from '@/stores/menu';
import { useSettingsStore } from '@/stores/settings';
import { useSessionActivityStore } from '@/stores/sessionActivity';
import { useAuthStore } from '@/stores/auth';
import { useProjectStore } from '@/stores/project';
import ProjectTree from '@/components/ProjectTree.vue';
import { useDeploymentCapabilitiesStore } from '@/stores/deploymentCapabilities';
import { TOOLBOX_ITEMS, canAccessToolboxSection } from '@/config/toolbox';
import BrowserIcon from '@/components/icons/BrowserIcon.vue';
import { useBrowserConnectionStore } from '@/stores/browserConnection';
import { useOrganizationStore } from '@/stores/organization';
import { useUIStore } from '@/stores/ui';
import { useCommandPaletteStore } from '@/stores/commandPalette';
import { useAssistantStore } from '@/stores/assistant';
import AssistantButton from '@/components/assistant/AssistantButton.vue';
import { MessagePlugin, DialogPlugin, Icon as TIcon } from "tdesign-vue-next";
import { useI18n } from 'vue-i18n';
import { useEditorResourcesStore } from '@/stores/editorResources';

const chatResources = useChatResourcesStore();
const editorResources = useEditorResourcesStore();
// Platform logos reused from IMChannelsOverviewPanel — keeps the session list
// visually consistent with the channels admin view.
import wecomLogo from '@/assets/img/im/wecom.svg';
import feishuLogo from '@/assets/img/im/feishu.svg';
import larkLogo from '@/assets/img/im/lark.svg';
import slackLogo from '@/assets/img/im/slack.svg';
import telegramLogo from '@/assets/img/im/telegram.svg';
import dingtalkLogo from '@/assets/img/im/dingtalk.svg';
import mattermostLogo from '@/assets/img/im/mattermost.svg';
import wechatLogo from '@/assets/img/im/wechat.svg';
import qqbotLogo from '@/assets/img/im/qqbot.png';

const PLATFORM_LOGO: Record<string, string> = {
    wecom: wecomLogo,
    feishu: feishuLogo,
    lark: larkLogo,
    slack: slackLogo,
    telegram: telegramLogo,
    dingtalk: dingtalkLogo,
    mattermost: mattermostLogo,
    wechat: wechatLogo,
    qqbot: qqbotLogo,
};

const platformLogo = (p: string): string => (p ? PLATFORM_LOGO[p] || '' : '');

const { t } = useI18n();
const usemenuStore = useMenuStore();
const sessionActivity = useSessionActivityStore();
const { entries: sessionActivityEntries, unread: sessionUnread } = storeToRefs(sessionActivity);
let sessionActivityTimer: ReturnType<typeof setInterval> | undefined;
const authStore = useAuthStore();
const deploymentCapabilities = useDeploymentCapabilitiesStore();
const toolboxPreview = computed(() => TOOLBOX_ITEMS.filter((item) => canAccessToolboxSection(item.key, {
    currentTenantRole: authStore.currentTenantRole,
    canAccessAllTenants: authStore.canAccessAllTenants,
    hasRole: (role) => authStore.hasRole(role),
    isSupported: (capability) => deploymentCapabilities.isSupported(capability),
})));
const orgStore = useOrganizationStore();
const uiStore = useUIStore();
const browserConnection = useBrowserConnectionStore();
const browserStackStatus = computed(() => {
    if (!uiStore.sidebarBrowserStatus) return '';
    if (!browserConnection.loaded || !browserConnection.enabled || !browserConnection.device) return '';
    return browserConnection.connected ? 'connected' : 'offline';
});
watch(() => uiStore.sidebarBrowserStatus && toolboxPreview.value.some((tool) => tool.key === 'browserconnection'), (visible) => {
    if (visible && !browserConnection.loaded) browserConnection.refresh().catch(() => {});
}, { immediate: true });
const commandPaletteStore = useCommandPaletteStore();
const assistantStore = useAssistantStore();
const assistantName = computed(() => assistantStore.displayName || t('assistant.title'));

// Platform-aware label for the ⌘K hint. navigator.platform is deprecated but
// the alternatives (userAgentData.platform) aren't universally available yet;
// this check is good enough for Mac vs. non-Mac.
const isMacLike = typeof navigator !== 'undefined' && /Mac|iPod|iPhone|iPad/.test(navigator.platform || '');
const cmdModKeyLabel = isMacLike ? '⌘' : 'Ctrl';
const route = useRoute();
const router = useRouter();
const currentpath = ref('');
const total = ref(0);
const sessionBuckets = ref<Record<string, SidebarSessionBucket>>({});
const bucketOrder = ref<string[]>([]);
let bucketRequestToken = 0;
const sessionListBooting = ref(false);
const currentSecondpath = ref('');
const scrollContainer = ref<HTMLElement | null>(null);
const imPlatforms = ref<string[]>([]);
const embedChannelNames = ref<Record<string, string>>({});
const activeSessionBucketKey = ref(DEFAULT_SESSION_BUCKET_KEY);
const sessionListCanScroll = ref(false);
const visibleChannelBuckets = computed(() =>
    bucketOrder.value
        .map((key) => sessionBuckets.value[key])
        .filter((bucket): bucket is SidebarSessionBucket => !!bucket && isChannelBucket(bucket) && bucketVisible(bucket)),
);
const showSessionSourceFilter = computed(() =>
    shouldShowSessionSourceFilter(visibleChannelBuckets.value.length),
);
const sessionScopeFilterPinned = computed(() =>
    activeSessionBucketKey.value !== DEFAULT_SESSION_BUCKET_KEY,
);
const sessionSourceOptions = computed(() =>
    buildSessionSourceOptions(
        t('menu.myChats'),
        visibleChannelBuckets.value.map((bucket) => ({
            key: bucket.key,
            label: bucket.label,
            platform: bucket.platform,
        })),
        (platform) => platformLogo(platform),
    ),
);
const activeBucket = computed(() => sessionBuckets.value[activeSessionBucketKey.value]);

// 侧栏项目树用：把所有已加载桶里的会话按 project_id 归一。
// 这里刻意复用 flattened buckets 而不是另发请求——项目树和历史列表看的是同一份数据，
// 归档/改名/置顶在任何一处发生都能立刻同步，不会出现两个列表不一致。
// flattenBucketItems 返回的裸行不带 path（历史列表是在分组时才补 `chat/${id}`），
// 而 SessionSidebarRow 的 navigate 事件与激活态判断都依赖 path，所以这里统一补上。
// SessionSidebarRow 要求 title/path 必填，而裸行两者都可能缺，这里一次性补齐。
type SidebarSessionRow = SessionForGrouping & { path: string; title: string };
const withRowPath = (item: SessionForGrouping): SidebarSessionRow => ({
    ...item,
    path: `chat/${item.id}`,
    title: item.title || t('menu.newSession'),
});

// 按项目分组的会话（供侧栏项目树的 sessions 插槽使用）。
const sessionsByProject = computed<Record<string, SidebarSessionRow[]>>(() => {
    const out: Record<string, SidebarSessionRow[]> = {};
    for (const item of flattenBucketItems(sessionBuckets.value, bucketOrder.value)) {
        const pid = item.project_id || '';
        if (!pid) continue;
        (out[pid] ||= []).push(withRowPath(item));
    }
    return out;
});
const hasAnySession = computed(() =>
    Object.values(sessionBuckets.value).some((bucket) => bucket.items.length > 0),
);
type MenuItem = { title: string; icon: string; path: string; childrenPath?: string; children?: any[] };
const { menuArr, visibleMenuArr } = storeToRefs(usemenuStore);
let activeSubmenu = ref<string>('');
const isLiteEdition = ref(false);

// 批量管理状态
const batchMode = ref(false)
const batchSelectedIds = ref<string[]>([])
const batchDeleting = ref(false)

const allSessionIds = computed(() => {
    const chatMenu = (menuArr.value as unknown as MenuItem[]).find((item: MenuItem) => item.path === 'creatChat');
    if (!chatMenu?.children) return [];
    return (chatMenu.children as any[]).map((s: any) => s.id);
})

const isAllBatchSelected = computed(() =>
    allSessionIds.value.length > 0 && batchSelectedIds.value.length === allSessionIds.value.length
)

const isBatchIndeterminate = computed(() =>
    batchSelectedIds.value.length > 0 && batchSelectedIds.value.length < allSessionIds.value.length
)

const batchDisplayCount = computed(() =>
    isAllBatchSelected.value ? total.value : batchSelectedIds.value.length
)

// ── 项目层（工作空间 → 项目 → 会话）───────────────────────────────
// 侧栏项目树整体放在「系统设置」菜单项下方，保持「项目 → 项目内会话」的树状展开。
// 只保留一个展开的项目：侧栏宽度有限，同时展开多个会把会话列表挤到折叠线以下。
const projectStore = useProjectStore();
const expandedProjectId = ref<string>('');

function onProjectExpand(id: string) {
    expandedProjectId.value = id;
}

// 新项目建好后刷新会话列表：默认项目变化会影响后续新建会话的归属。
function onProjectCreated() {
    void projectStore.fetchProjects(true);
}

/**
 * 在某项目下开新会话。
 *
 * 输入框里的项目下拉已移除，因此「会话归属哪个项目」只能由用户在这里
 * 从项目节点发起。带上 ?project=<id> 进新会话页，由那一页在创建会话时
 * 提交 project_id；不带项目直接进新会话页会被后端拒绝（请先选择项目）。
 */
function onProjectNewSession(project: { id: string }) {
    expandedProjectId.value = project.id;
    void router.push({ path: '/platform/creatChat', query: { project: project.id } });
}

// 是否处于知识库详情页（不包括全局聊天）
const isInKnowledgeBase = computed<boolean>(() => {
    return route.name === 'knowledgeBaseDetail' ||
        route.name === 'kbCreatChat' ||
        route.name === 'knowledgeBaseSettings';
});

// 是否在知识库列表页面
const isInKnowledgeBaseList = computed<boolean>(() => {
    return route.name === 'knowledgeBaseList';
});

// 是否在创建聊天页面
const isInCreatChat = computed<boolean>(() => {
    return route.name === 'globalCreatChat' || route.name === 'kbCreatChat';
});

// 是否在对话详情页
const isInChatDetail = computed<boolean>(() => route.name === 'chat');

// 是否在智能体列表页面
const isInAgentList = computed<boolean>(() => route.name === 'agentList');

// 是否在组织列表页面
const isInOrganizationList = computed<boolean>(() => route.name === 'organizationList');

// 统一的菜单项激活状态判断
const isMenuItemActive = (itemPath: string): boolean => {
    const currentRoute = route.name;

    switch (itemPath) {
        case 'knowledge-bases':
            return currentRoute === 'knowledgeBaseList' ||
                currentRoute === 'knowledgeBaseDetail' ||
                currentRoute === 'knowledgeBaseSettings';
        case 'agents':
            return currentRoute === 'agentList';
        case 'toolbox':
            return currentRoute === 'toolbox';
        case 'artifacts':
            return currentRoute === 'artifactLibrary';
        case 'automations':
            return currentRoute === 'automationList';
        case 'organizations':
            return currentRoute === 'organizationList';
        case 'creatChat':
            return currentRoute === 'kbCreatChat' || currentRoute === 'globalCreatChat';
        case 'settings':
            return currentRoute === 'settings';
        default:
            return itemPath === currentpath.value;
    }
};

// 统一的图标激活状态判断
const getIconActiveState = (itemPath: string) => {
    const currentRoute = route.name;

    return {
        isKbActive: itemPath === 'knowledge-bases' && (
            currentRoute === 'knowledgeBaseList' ||
            currentRoute === 'knowledgeBaseDetail' ||
            currentRoute === 'knowledgeBaseSettings'
        ),
        isCreatChatActive: itemPath === 'creatChat' && (currentRoute === 'kbCreatChat' || currentRoute === 'globalCreatChat'),
        isSettingsActive: itemPath === 'settings' && currentRoute === 'settings',
        isChatActive: itemPath === 'chat' && currentRoute === 'chat'
    };
};

// 分离上下两部分菜单（使用 visibleMenuArr 以便 lite 模式过滤 logout）
// OEM 改造：把 settings 也纳入主菜单区，让「模型管理」成为左侧导航的第四项
// （原版 settings 只藏在右下角用户菜单里，与需求「左侧导航 4 项」不符）。
const TOP_MENU_PATHS = new Set(['creatChat', 'knowledge-bases', 'artifacts', 'agents', 'automations', 'toolbox', 'organizations', 'settings']);

const topMenuItems = computed<MenuItem[]>(() => {
    return (visibleMenuArr.value as unknown as MenuItem[]).filter((item: MenuItem) => TOP_MENU_PATHS.has(item.path));
});

const bottomMenuItems = computed<MenuItem[]>(() => {
    return (visibleMenuArr.value as unknown as MenuItem[]).filter((item: MenuItem) => !TOP_MENU_PATHS.has(item.path));
});

// 当前知识库信息
const currentKbName = ref<string>('')
const currentKbInfo = ref<any>(null)

// 进行中的置顶/取消置顶请求，避免重复点击
const pinningIds = ref<Set<string>>(new Set())

// 「聊天」区内按日期分组（当前筛选来源）
const dateBucketLabels = computed<Record<DateBucketKey, string>>(() => ({
    pinned: t('time.pinned'),
    today: t('time.today'),
    yesterday: t('time.yesterday'),
    last7Days: t('time.last7Days'),
    last30Days: t('time.last30Days'),
    lastYear: t('time.lastYear'),
    earlier: t('time.earlier'),
}));

// 底部历史列表 = 当前会话来源下的全部会话，按日期分组。
//
// 归属项目的会话会同时出现在这里的日期分组中；侧栏项目树用于按项目维度
// 浏览和新建，两者不冲突（项目树可收起，历史列表始终可用）。
// 历史区固定只列未归属项目的会话（见下方 computed），所以空态也统一用
// 「没有未归属项目的会话」，避免出现「暂无对话」但其实会话都在项目树里。
const filteredGroupedSessions = computed(() => {
    const bucket = activeBucket.value;
    if (!bucket?.items.length) return [];
    // 已归属项目的会话在项目树里展示，历史区只列「未归属项目」的会话，
    // 否则同一个会话会在侧栏出现两次，用户无从判断该点哪一个。
    // 项目树被收起时也不回填：项目节点仍然可见，点开就能找到会话。
    const ungrouped = bucket.items.filter((item) => !item.project_id);
    if (!ungrouped.length) return [];
    return groupSessionsByDate(
        ungrouped.map((item) => ({
            ...item,
            path: `chat/${item.id}`,
            title: item.title || '',
        })),
        dateBucketLabels.value,
        (session) => classifyDateBucket(session.updated_at || session.created_at),
    );
});

// Only a locally created fork requests attention; loading history and switching
// between existing sessions must not replay the entrance animation.
const pendingForkRevealId = ref('');
const revealedSessionId = ref('');
let forkRevealTimer: ReturnType<typeof setTimeout> | undefined;
usemenuStore.$onAction(({ name, args, after }) => {
    if (name !== 'updataMenuChildren' || !args[0]?.parent_session_id) return;
    const sessionId = String(args[0].id);
    after(() => { pendingForkRevealId.value = sessionId; });
});

watch(
    () => {
        const id = pendingForkRevealId.value;
        return id && !uiStore.sidebarCollapsed && currentSecondpath.value === `chat/${id}`
            && filteredGroupedSessions.value.some((group) => group.items.some((item) => item.id === id))
            ? id : '';
    },
    (id) => {
        if (!id) return;
        const container = scrollContainer.value;
        const row = Array.from(container?.querySelectorAll<HTMLElement>('[data-session-id]') ?? [])
            .find((element) => element.dataset.sessionId === id);
        if (!container || !row) return;

        // Reveal within the sidebar only, without moving the conversation pane.
        const bounds = container.getBoundingClientRect();
        const rowBounds = row.getBoundingClientRect();
        if (rowBounds.top < bounds.top) container.scrollTop += rowBounds.top - bounds.top;
        else if (rowBounds.bottom > bounds.bottom) container.scrollTop += rowBounds.bottom - bounds.bottom;

        clearTimeout(forkRevealTimer);
        revealedSessionId.value = id;
        pendingForkRevealId.value = '';
        forkRevealTimer = setTimeout(() => { revealedSessionId.value = ''; }, 350);
    },
    { flush: 'post' },
);

const refreshSessionListScrollability = async () => {
    await nextTick();
    const container = scrollContainer.value;
    sessionListCanScroll.value = !!container && container.scrollHeight > container.clientHeight + 1;
};

/** 列表未撑满滚动区时自动续页（按当前可见 DOM 测量，避免折叠导致误判） */
const ensureBucketFillsViewport = async (key: string) => {
    const MAX_ITERATIONS = 20;
    for (let i = 0; i < MAX_ITERATIONS; i++) {
        await nextTick();
        await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));
        const container = scrollContainer.value;
        const bucket = sessionBuckets.value[key];
        if (!container || !bucket || !bucketHasMore(bucket) || bucket.loading) break;

        const hasOverflow = container.scrollHeight > container.clientHeight + 1;
        if (hasOverflow) break;

        const prevCount = bucket.items.length;
        await loadBucketPage(key);
        if ((sessionBuckets.value[key]?.items.length ?? 0) <= prevCount) break;
    }
};

const mouseenteBotDownr = (val: string) => {
    activeSubmenu.value = val;
}
const mouseleaveBotDown = () => {
    activeSubmenu.value = '';
}

const enterBatchMode = () => {
    if (batchDeleting.value) return
    batchMode.value = true
    batchSelectedIds.value = []
}

const exitBatchMode = () => {
    if (batchDeleting.value) return
    batchMode.value = false
    batchSelectedIds.value = []
}

const toggleBatchSelect = (id: string) => {
    if (batchDeleting.value) return
    const idx = batchSelectedIds.value.indexOf(id)
    if (idx > -1) {
        batchSelectedIds.value.splice(idx, 1)
    } else {
        batchSelectedIds.value.push(id)
    }
}

const toggleBatchSelectAll = (checked: boolean) => {
    if (batchDeleting.value) return
    batchSelectedIds.value = checked ? [...allSessionIds.value] : []
}

const handleInlineBatchDelete = () => {
    if (batchDeleting.value || batchSelectedIds.value.length === 0) return;
    const selectedIds = [...batchSelectedIds.value];
    const isDeleteAll = isAllBatchSelected.value;
    const displayCount = batchDisplayCount.value;
    let submitted = false;
    const confirmDialog = DialogPlugin.confirm({
        header: t('batchManage.deleteConfirmTitle'),
        body: isDeleteAll
            ? t('batchManage.deleteAllConfirmBody') || t('batchManage.deleteConfirmBody', { count: displayCount })
            : t('batchManage.deleteConfirmBody', { count: displayCount }),
        confirmBtn: { content: t('batchManage.delete'), theme: 'danger' as const },
        cancelBtn: t('batchManage.cancel'),
        theme: 'warning',
        confirmOnEnter: false,
        onClose: () => {
            if (!batchDeleting.value) confirmDialog.destroy();
        },
        onConfirm: async () => {
            if (submitted || batchDeleting.value) return;
            submitted = true;
            batchDeleting.value = true;
            try {
                confirmDialog.update({
                    confirmLoading: true,
                    closeOnEscKeydown: false,
                    closeOnOverlayClick: false,
                    closeBtn: false,
                    cancelBtn: { content: t('batchManage.cancel'), disabled: true },
                });
                const res = isDeleteAll
                    ? await deleteAllSessions()
                    : await batchDelSessions(selectedIds);
                if (res && res.success === true) {
                    if (isDeleteAll) {
                        usemenuStore.clearMenuArr();
                        total.value = 0;
                        await getMessageList();
                    } else {
                        let next = sessionBuckets.value;
                        for (const id of selectedIds) next = removeSessionFromBuckets(next, id);
                        sessionBuckets.value = next;
                        syncMenuStoreFromBuckets();
                    }
                    const currentChatId = route.params.chatid as string;
                    if (currentChatId && (isDeleteAll || selectedIds.includes(currentChatId)))
                        router.push('/platform/creatChat');
                    batchSelectedIds.value = [];
                    batchMode.value = false;
                    MessagePlugin.success(t('batchManage.deleteSuccess'));
                } else {
                    MessagePlugin.error(t('batchManage.deleteFailed'));
                }
            } catch {
                MessagePlugin.error(t('batchManage.deleteFailed'));
            } finally {
                batchDeleting.value = false;
                confirmDialog.destroy();
            }
        },
    });
};

const handleSessionMenuClick = (data: { value: string }, item: any) => {
    if (data?.value === 'delete') {
        delCard(item);
    } else if (data?.value === 'clearMessages') {
        clearMessages(item);
    } else if (data?.value === 'batchManage') {
        enterBatchMode()
    } else if (data?.value === 'pin' || data?.value === 'unpin') {
        togglePin(item, data.value === 'pin');
    }
};

// 基于会话来源推导展示用的短标签已经被 platformLogo(<img>) 取代，Web 会话没有图标。

const buildSessionMenuOptions = (item: any) => {
    const options: any[] = [];
    if (item.is_pinned) {
        options.push({
            content: t('menu.unpin'),
            value: 'unpin',
            prefixIcon: () => h(TIcon, { name: 'pin-filled' }),
        });
    } else {
        options.push({
            content: t('menu.pin'),
            value: 'pin',
            prefixIcon: () => h(TIcon, { name: 'pin' }),
        });
    }
    options.push(
        { content: t('menu.renameSession'), value: 'rename', prefixIcon: () => h(TIcon, { name: 'edit-1' }) },
        { content: t('menu.clearMessages'), value: 'clearMessages', prefixIcon: () => h(TIcon, { name: 'clear' }) },
        { content: t('menu.batchManage'), value: 'batchManage', prefixIcon: () => h(TIcon, { name: 'queue' }) },
        { content: t('upload.deleteRecord'), value: 'delete', theme: 'error', prefixIcon: () => h(TIcon, { name: 'delete' }) },
    );
    return options;
};

const updateSessionInBuckets = (
    sessionId: string,
    patch: Partial<{ is_pinned: boolean; pinned_at: string | null; title: string; isNoTitle?: boolean }>,
) => {
    const next: Record<string, SidebarSessionBucket> = {};
    for (const [key, bucket] of Object.entries(sessionBuckets.value)) {
        next[key] = {
            ...bucket,
            items: bucket.items.map((row) => (row.id === sessionId ? { ...row, ...patch } : row)),
        };
    }
    sessionBuckets.value = next;
    syncMenuStoreFromBuckets();
};

const renameSessionTitle = async (item: any, title: string) => {
    try {
        await renameSession(item.id, title, item.description || '');
        MessagePlugin.success(t('menu.renameSessionSuccess'));
    } catch {
        MessagePlugin.error(t('menu.renameSessionFailed'));
    }
};

const togglePin = (item: any, pin: boolean) => {
    if (pinningIds.value.has(item.id)) return;
    pinningIds.value.add(item.id);

    setSessionPinned(item.id, pin).catch(() => {
        MessagePlugin.error(pin ? t('menu.pinFailed') : t('menu.unpinFailed'));
    }).finally(() => {
        pinningIds.value.delete(item.id);
    });
};

const clearMessages = (item: any) => {
    clearSession(item.id).then(() => {
        MessagePlugin.success(t('menu.clearMessagesSuccess'));
    }).catch(() => {
        MessagePlugin.error(t('menu.clearMessagesFailed'));
    });
};

const delCard = (item: any) => {
    removeSession(item.id).catch(() => MessagePlugin.error(t('chat.deleteSessionFailed')))
}


const debounce = (fn: (...args: any[]) => void, delay: number) => {
    let timer: ReturnType<typeof setTimeout>
    return (...args: any[]) => {
        clearTimeout(timer)
        timer = setTimeout(() => fn(...args), delay)
    }
}
const mapSessionRow = (item: any) => ({
    title: item.title ? item.title : t('menu.newSession'),
    path: `chat/${item.id}`,
    id: item.id,
    isMore: false,
    isNoTitle: item.title ? false : true,
    created_at: item.created_at,
    updated_at: item.updated_at,
    is_pinned: !!item.is_pinned,
    pinned_at: item.pinned_at || null,
    im_platform: item.im_platform || '',
    description: item.description || '',
    user_id: item.user_id || '',
    parent_session_id: item.parent_session_id || '',
    project_id: item.project_id || '',
});

const syncMenuStoreFromBuckets = () => {
    usemenuStore.clearMenuArr();
    const flat = flattenBucketItems(sessionBuckets.value, bucketOrder.value);
    flat.forEach((item) => usemenuStore.updatemenuArr(item));
    total.value = flat.length;
};

const menuChildToSessionRow = (item: Record<string, unknown>): SessionForGrouping & { path: string } => {
    const id = String(item.id);
    return {
        id,
        path: typeof item.path === 'string' ? item.path : `chat/${id}`,
        title: typeof item.title === 'string' ? item.title : undefined,
        is_pinned: !!item.is_pinned,
        created_at: typeof item.created_at === 'string' ? item.created_at : undefined,
        updated_at: typeof item.updated_at === 'string' ? item.updated_at : undefined,
        im_platform: typeof item.im_platform === 'string' ? item.im_platform : '',
        description: typeof item.description === 'string' ? item.description : '',
        user_id: typeof item.user_id === 'string' ? item.user_id : '',
        parent_session_id: typeof item.parent_session_id === 'string' ? item.parent_session_id : '',
        // 必须带 project_id：侧栏按它把会话挂到项目节点下。漏了会让新会话
        // 掉进「未归属项目」，项目节点里反而看不到刚建的会话。
        project_id: typeof item.project_id === 'string' ? item.project_id : '',
    };
};

const sessionExistsInBuckets = (sessionId: string) =>
    Object.values(sessionBuckets.value).some((bucket) => bucket.items.some((row) => row.id === sessionId));

/** 创建会话后 menuStore 已乐观写入，但列表实际渲染自 sessionBuckets，需补齐。 */
const ensureSessionInSidebar = (sessionId: string) => {
    if (!sessionId || sessionExistsInBuckets(sessionId)) return;

    const web = sessionBuckets.value.web;
    if (!web) return;

    const chatMenu = (menuArr.value as unknown as MenuItem[]).find((item) => item.path === 'creatChat');
    const fromStore = (chatMenu?.children as Record<string, unknown>[] | undefined)
        ?.find((item) => item.id === sessionId);
    if (!fromStore) return;

    sessionBuckets.value = {
        ...sessionBuckets.value,
        web: prependSessionToWebBucket(web, menuChildToSessionRow(fromStore)),
    };
    total.value = flattenBucketItems(sessionBuckets.value, bucketOrder.value).length;
};

const rebuildBucketDefinitions = () => buildBucketDefinitions(
    imPlatforms.value,
    embedChannelNames.value,
    {
        web: t('menu.myChats'),
        imPlatform: (platform) => t(`agentEditor.im.${platform}`),
        embedChannel: (name) => name,
        api: t('menu.apiChats'),
    },
    { includeAdminChannelBuckets: authStore.hasRole('admin') },
);

/** 首屏轻量探测各渠道是否有会话（page_size=1 只取 total），避免展示空文件夹 */
const probeChannelBucketCounts = async (keys: string[], token: number) => {
    const targets = keys.filter((key) => isChannelBucketKey(key));
    await Promise.all(
        targets.map(async (key) => {
            const bucket = sessionBuckets.value[key];
            if (!bucket) return;
            try {
                const res: any = await getSessionsList(1, 1, bucket.apiSource);
                if (token !== bucketRequestToken) return;
                sessionBuckets.value = {
                    ...sessionBuckets.value,
                    [key]: applyBucketCountProbe(bucket, res?.total ?? 0),
                };
            } catch {
                if (token !== bucketRequestToken) return;
                sessionBuckets.value = {
                    ...sessionBuckets.value,
                    [key]: applyBucketCountProbe(bucket, 0),
                };
            }
        }),
    );
};

const loadBucketPage = async (key: string, page?: number, token?: number) => {
    const activeToken = token ?? bucketRequestToken;
    const bucket = sessionBuckets.value[key];
    if (!bucket || bucket.loading) return;

    const nextPage = page ?? bucket.page + 1;
    sessionBuckets.value = {
        ...sessionBuckets.value,
        [key]: { ...bucket, loading: true },
    };

    try {
        const res: any = await getSessionsList(nextPage, SIDEBAR_BUCKET_PAGE_SIZE, bucket.apiSource);
        if (activeToken !== bucketRequestToken) return;
        const rows = (res?.data || []).map((item: any) => mapSessionRow(item));
        const current = sessionBuckets.value[key];
        sessionBuckets.value = {
            ...sessionBuckets.value,
            [key]: mergeBucketPage(current, rows, res?.total ?? rows.length, nextPage),
        };
        syncMenuStoreFromBuckets();
        await refreshSessionListScrollability();
    } catch {
        if (activeToken !== bucketRequestToken) return;
        const current = sessionBuckets.value[key];
        sessionBuckets.value = {
            ...sessionBuckets.value,
            [key]: { ...current, loading: false, loaded: true },
        };
    }
};

const switchSessionBucket = async (key: string) => {
    if (key === activeSessionBucketKey.value) return;
    activeSessionBucketKey.value = key;
    const bucket = sessionBuckets.value[key];
    if (bucket && !bucket.loaded && !bucket.loading) {
        await loadBucketPage(key, 1);
    }
    await ensureBucketFillsViewport(key);
    await refreshSessionListScrollability();
};

const syncActiveBucketFromChat = async (sessionId: string | undefined) => {
    if (!sessionId) return;

    let bucketKey = findSessionBucketKey(sessionBuckets.value, sessionId);
    if (!bucketKey) {
        const chatMenu = (menuArr.value as unknown as MenuItem[]).find((item) => item.path === 'creatChat');
        const fromStore = (chatMenu?.children as Record<string, unknown>[] | undefined)
            ?.find((item) => item.id === sessionId);
        if (fromStore) {
            bucketKey = originGroupKey(resolveSessionOrigin(menuChildToSessionRow(fromStore)));
        }
    }
    // On a hard refresh only the web bucket is loaded, so a session opened from
    // any other folder (IM, embed, or the admin-only API folder) isn't in any
    // bucket or the menu store. Fetch its detail and classify its origin folder
    // so the sidebar stays in sync with the chat pane instead of snapping back
    // to "my chats". Only switch when that folder is actually present.
    if (!bucketKey) {
        try {
            const res: any = await getSession(sessionId);
            const candidate = originGroupKey(resolveSessionOrigin({
                id: sessionId,
                im_platform: res?.data?.im_platform || '',
                description: res?.data?.description || '',
                user_id: res?.data?.user_id || '',
            }));
            if (sessionBuckets.value[candidate]) {
                bucketKey = candidate;
            }
        } catch {
            // Fall through: leave the default bucket active on lookup failure.
        }
    }
    if (!bucketKey || bucketKey === activeSessionBucketKey.value) return;

    activeSessionBucketKey.value = bucketKey;
    const bucket = sessionBuckets.value[bucketKey];
    if (bucket && !bucket.loaded && !bucket.loading) {
        await loadBucketPage(bucketKey, 1);
    }
};

const initSessionBuckets = async () => {
    const token = ++bucketRequestToken;
    sessionListBooting.value = true;

    const defs = rebuildBucketDefinitions();
    bucketOrder.value = defs.map((def) => def.key);
    const buckets: Record<string, SidebarSessionBucket> = {};
    for (const def of defs) {
        buckets[def.key] = createEmptyBucket(def);
    }
    sessionBuckets.value = buckets;

    // 首屏：拉 web 会话 + 轻量探测各渠道 count（不拉完整列表）；有会话的渠道才展示文件夹
    const channelKeys = defs.map((def) => def.key).filter((key) => isChannelBucketKey(key));
    await Promise.all([
        loadBucketPage('web', 1, token),
        probeChannelBucketCounts(channelKeys, token),
    ]);

    if (token === bucketRequestToken) {
        sessionListBooting.value = false;
        syncMenuStoreFromBuckets();
        await ensureBucketFillsViewport('web');
        await refreshSessionListScrollability();
    }
};

const getMessageList = async () => {
    await initSessionBuckets();
};

// 滚动到底时为当前筛选来源加载下一页
const checkScrollBottom = async () => {
    const container = scrollContainer.value;
    const key = activeSessionBucketKey.value;
    const bucket = sessionBuckets.value[key];
    if (!container || !bucket || !bucketHasMore(bucket) || bucket.loading) return;

    const { scrollTop, scrollHeight, clientHeight } = container;
    const hasOverflow = scrollHeight > clientHeight + 1;
    if (!hasOverflow) {
        await ensureBucketFillsViewport(key);
        return;
    }

    const isNearBottom = scrollHeight - (scrollTop + clientHeight) < 100;
    if (!isNearBottom) return;

    await loadBucketPage(key);
};

const handleScroll = debounce(checkScrollBottom, 200);

async function loadCurrentKbInfo(kbId: string) {
    if (!kbId || !isInKnowledgeBase.value) {
        currentKbName.value = ''
        currentKbInfo.value = null
        return
    }
    const data = await chatResources.fetchKnowledgeBaseById(kbId)
    if (data) {
        currentKbName.value = data.name || ''
        currentKbInfo.value = data
    } else {
        currentKbInfo.value = null
    }
}

const loadSessionOriginMeta = async () => {
    try {
        const res: any = await listAllIMChannels();
        imPlatforms.value = configuredPlatforms(res?.data || []);
    } catch {
        imPlatforms.value = [];
    }
    try {
        const res: any = await listAllEmbedChannels();
        const names: Record<string, string> = {};
        for (const ch of res?.data || []) {
            if (ch?.id && ch?.name) names[ch.id] = ch.name;
        }
        embedChannelNames.value = names;
    } catch {
        embedChannelNames.value = {};
    }
};

const handleSessionMutation = (event: Event) => {
    const detail = (event as CustomEvent<SessionMutationDetail>).detail;
    if (!detail?.sessionId) return;
    if (detail.removed || detail.messagesCleared) sessionActivity.dismiss(detail.sessionId);
    if (detail.patch) {
        updateSessionInBuckets(detail.sessionId, {
            ...detail.patch,
            ...(detail.patch.title ? { isNoTitle: false } : {}),
        });
    }
    if (detail.removed) {
        sessionBuckets.value = removeSessionFromBuckets(sessionBuckets.value, detail.sessionId);
        syncMenuStoreFromBuckets();
        // 项目节点上的「N 个会话」徽标来自项目列表接口，删会话后必须重拉，
        // 否则数字停在旧值（删完会话徽标还显示 2）。
        if (projectStore.loaded) void projectStore.fetchProjects(true);
        if (detail.sessionId === route.params.chatid) {
            router.push('/platform/creatChat');
        }
    }
};

// ---------------------------------------------------------------------------
// 侧栏密度档位（窄屏自适应）
//
// 之前侧栏所有尺寸都写死在 LESS 里，没有任何 @media 断点：1440px 和 760px 视口下
// 侧栏恒为 260px、图标恒为 18px、行高恒为 34px，窄屏时把对话区挤得很窄。
// 这里改为三档密度（compact / regular / relaxed），由视口宽度和用户拖拽的
// 侧栏宽度共同决定，最终写成一串 CSS 变量挂在 .aside_box 上，
// 侧栏内部（含 TenantSelector、会话列表）统一读变量，避免「图标缩了行高没缩」。
// ---------------------------------------------------------------------------
// Logo 加载失败兜底：某些内网部署会拦掉 /oem/* 静态资源，
// 这时不该留一块空白，直接退化成产品名文字。
const logoFailed = ref(false);
// OEM：版本号角标。桌面版由 Go 侧 OnDomReady 注入 window.__WK_APP_VERSION__。
//
// 时序注意：Wails 的 OnDomReady 可能早于 Vue 组件 setup 完成，也可能晚到；
// 因此这里既在 setup 时读一次，也在 onMounted 后再读一次兜底。
// 浏览器里访问时该全局变量不存在，角标不渲染（不硬编码版本，避免与制品不一致）。
const readAppVersion = (): string => {
    if (typeof window === 'undefined') return '';
    return String((window as any).__WK_APP_VERSION__ || '').trim();
};
const appVersion = ref<string>(readAppVersion());
const viewportWidth = ref(typeof window === 'undefined' ? 1440 : window.innerWidth);
const sidebarDensity = computed<SidebarDensityKey>(() =>
    pickSidebarDensity(viewportWidth.value, uiStore.sidebarDisplayWidth),
);
// 窄屏下侧栏宽度随视口收缩（宽屏仍是用户拖拽的值，不打扰）
const responsiveSidebarWidth = computed(() =>
    resolveSidebarWidth(viewportWidth.value, uiStore.sidebarDisplayWidth, SIDEBAR_MIN_WIDTH),
);
const sidebarStyleVars = computed(() => {
    const d = SIDEBAR_DENSITY[sidebarDensity.value];
    return {
        // 宽度也写进行内变量：.aside_box 的 width 读 var(--sidebar-width)，
        // 这里覆盖它即可让收缩生效，且不破坏展开/折叠的过渡动画。
        '--sidebar-width': `${responsiveSidebarWidth.value}px`,
        '--sidebar-icon-size': `${d.icon}px`,
        '--sidebar-icon-gap': `${d.gap}px`,
        '--sidebar-inset-x': `${d.inset}px`,
        '--sidebar-row-height': `${d.rowHeight}px`,
        '--sidebar-logo-height': `${d.logoHeight}px`,
        '--sidebar-logo-max-width': `${d.logoMaxWidth}px`,
        '--sidebar-section-gap': `${d.sectionGap}px`,
        // 文案列起点 = 内边距 + 图标 + 间距；三处对齐都靠它
        '--sidebar-text-inset': `${d.inset + d.icon + d.gap}px`,
        // 次级间距（Logo 行下方、空间选择器下方）：跟随档位，约为钮间距的一半
        '--sidebar-subgap': `${Math.round(d.gap * 1.25)}px`,
    };
});

// 用 requestAnimationFrame 节流，拖拽窗口边缘时不至于每像素触发一次重算
let viewportRaf = 0;
const handleViewportResize = () => {
    if (viewportRaf) return;
    viewportRaf = requestAnimationFrame(() => {
        viewportRaf = 0;
        viewportWidth.value = window.innerWidth;
    });
};

onMounted(async () => {
    // OEM：Wails 的版本注入（OnDomReady → WindowExecJS）与 Vue 挂载没有先后保证，
    // 这里补读一次并在 3 秒内轻量轮询，读到就停——避免角标因为几百毫秒的时序差永远不出现。
    if (!appVersion.value) {
        appVersion.value = readAppVersion();
        if (!appVersion.value) {
            const timer = window.setInterval(() => {
                const v = readAppVersion();
                if (v) {
                    appVersion.value = v;
                    window.clearInterval(timer);
                }
            }, 300);
            window.setTimeout(() => window.clearInterval(timer), 3000);
        }
    }
    sessionActivity.restore();
    void sessionActivity.refresh();
    sessionActivityTimer = setInterval(() => { void sessionActivity.refresh(); }, 5000);
    const routeName = typeof route.name === 'string' ? route.name : (route.name ? String(route.name) : '')
    currentpath.value = routeName;
    if (route.params.chatid) {
        currentSecondpath.value = `chat/${route.params.chatid}`;
    }

    window.addEventListener(SESSION_MUTATION_EVENT, handleSessionMutation);
    window.addEventListener('resize', handleViewportResize, { passive: true });

    isLiteEdition.value = authStore.isLiteMode
    editorResources.ensureSystemInfo().then(() => {
        if (editorResources.systemInfo?.edition === 'lite') {
            isLiteEdition.value = true
            authStore.setLiteMode(true)
        }
    }).catch(() => { })

    await loadCurrentKbInfo((route.params as any)?.kbId as string)

    await loadSessionOriginMeta();
    await getMessageList();
    const initialChatId = route.params.chatid as string | undefined;
    if (initialChatId) {
        ensureSessionInSidebar(initialChatId);
        await syncActiveBucketFromChat(initialChatId);
    }
    // 若组织列表未加载则拉取一次，用于侧栏「待审批」角标
    if (deploymentCapabilities.isSupported('organizations') && orgStore.organizations.length === 0) {
        orgStore.fetchOrganizations();
    }
});

onUnmounted(() => {
    clearInterval(sessionActivityTimer);
    clearTimeout(forkRevealTimer);
    sessionActivity.clear();
    window.removeEventListener(SESSION_MUTATION_EVENT, handleSessionMutation);
    window.removeEventListener('resize', handleViewportResize);
    if (viewportRaf) {
        cancelAnimationFrame(viewportRaf);
        viewportRaf = 0;
    }
});

watch([() => route.name, () => route.params], (newvalue, oldvalue) => {
    const nameStr = typeof newvalue[0] === 'string' ? (newvalue[0] as string) : (newvalue[0] ? String(newvalue[0]) : '')
    currentpath.value = nameStr;
    if (newvalue[1].chatid) {
        currentSecondpath.value = `chat/${newvalue[1].chatid}`;
    } else {
        currentSecondpath.value = "";
    }

    // 创建新会话时 creatChat 会先 updataMenuChildren，再跳转 chat/:id。
    // 侧栏实际渲染 sessionBuckets，需按 buckets 判断是否缺失，不能把 menuStore 当真相来源。
    const newChatId = (newvalue[1] as any)?.chatid as string | undefined;
    if (nameStr === 'chat' && newChatId) {
        ensureSessionInSidebar(newChatId);
        void syncActiveBucketFromChat(newChatId);
    }

    // 路由变化时更新图标状态和知识库信息（不涉及对话列表）
    getIcon(nameStr);

    // 如果切换了知识库，更新知识库名称但不重新加载对话列表
    if (newvalue[1].kbId !== oldvalue?.[1]?.kbId) {
        loadCurrentKbInfo((newvalue[1] as any)?.kbId as string);
    }
});
let knowledgeIcon = ref('zhishiku-green.svg');
let prefixIcon = ref('prefixIcon.svg');
let settingIcon = ref('setting.svg');
let agentIcon = ref('agent.svg');
let artifactIcon = ref('artifact.svg');
let toolboxIcon = ref('toolbox.svg');
let automationIcon = ref('automation.svg');
let organizationIcon = ref('organization.svg');
let pathPrefix = ref(route.name)
const getIcon = (path: string) => {
    // 根据当前路由状态更新所有图标
    const kbActiveState = getIconActiveState('knowledge-bases');
    const creatChatActiveState = getIconActiveState('creatChat');
    const settingsActiveState = getIconActiveState('settings');
    const agentsActiveState = route.name === 'agentList';
    const artifactsActiveState = route.name === 'artifactLibrary';
    const organizationsActiveState = route.name === 'organizationList';

    // 知识库图标：只在知识库页面显示绿色
    knowledgeIcon.value = kbActiveState.isKbActive ? 'zhishiku-green.svg' : 'zhishiku.svg';

    // 智能体图标：只在智能体页面显示绿色
    agentIcon.value = agentsActiveState ? 'agent-green.svg' : 'agent.svg';

    // 产物图标：只在产物页面显示绿色
    artifactIcon.value = artifactsActiveState ? 'artifact-green.svg' : 'artifact.svg';

    toolboxIcon.value = route.name === 'toolbox' ? 'toolbox-green.svg' : 'toolbox.svg';
    automationIcon.value = route.name === 'automationList' ? 'automation-green.svg' : 'automation.svg';

    // 组织图标：只在组织页面显示绿色
    organizationIcon.value = organizationsActiveState ? 'organization-green.svg' : 'organization.svg';

    // 对话图标：只在对话创建页面显示绿色，其他情况显示默认
    prefixIcon.value = creatChatActiveState.isCreatChatActive ? 'prefixIcon-green.svg' : 'prefixIcon.svg';

    // 设置图标：只在设置页面显示绿色
    settingIcon.value = settingsActiveState.isSettingsActive ? 'setting-green.svg' : 'setting.svg';

}
getIcon(typeof route.name === 'string' ? route.name as string : (route.name ? String(route.name) : ''))
const handleMenuClick = async (path: string) => {
    // 离开设置页时必须先关掉设置，否则 showSettingsModal 会一直为 true，
    // 设置页继续占满内容区，用户会以为"菜单点了没反应"。
    // 目标就是 settings 时不清（下面分支会重新 openSettings）。
    if (path !== 'settings' && uiStore.showSettingsModal) {
        uiStore.closeSettings()
    }
    if (path === 'knowledge-bases') {
        // 知识库菜单项：如果在知识库内部，跳转到当前知识库文件页；否则跳转到知识库列表
        const kbId = await getCurrentKbId()
        if (kbId) {
            router.push(`/platform/knowledge-bases/${kbId}`)
        } else {
            router.push('/platform/knowledge-bases')
        }
    } else if (path === 'agents') {
        router.push('/platform/agents')
    } else if (path === 'organizations') {
        // 组织菜单项：跳转到组织列表
        router.push('/platform/organizations')
    } else if (path === 'settings') {
        // OEM: 这里的设置页已收敛为「模型管理」，
        // 默认直接落在模型配置分区，而不是通用的 general 分区。
        uiStore.openSettings('models')
        router.push('/platform/settings?section=models')
    } else {
        gotopage(path)
    }
}

const getCurrentKbId = async (): Promise<string | null> => {
    const kbId = (route.params as any)?.kbId as string
    if (isInKnowledgeBase.value && kbId) {
        return kbId
    }
    return null
}

const gotopage = async (path: string) => {
    pathPrefix.value = path;
    {
        if (path === 'creatChat') {
            // 再次点击当前「新对话」时路由不会变化，仍需清空上一条草稿的引用。
            if (isInCreatChat.value) useSettingsStore().clearChatReferences()
            // 如果在知识库详情页，跳转到全局对话创建页
            if (isInKnowledgeBase.value) {
                router.push('/platform/creatChat')
            } else {
                // 如果不在知识库内，进入对话创建页
                router.push(`/platform/creatChat`)
            }
        } else {
            router.push(`/platform/${path}`);
        }
    }
    getIcon(path)
}

const getImgSrc = (url: string) => {
    return new URL(`/src/assets/img/${url}`, import.meta.url).href;
}

const mouseenteMenu = (path: string) => {
}
const mouseleaveMenu = (path: string) => {
}

let sidebarResizeStartWidth = 0
const startSidebarResize = () => {
    sidebarResizeStartWidth = uiStore.sidebarDisplayWidth
    uiStore.sidebarResizing = true
}
const resizeSidebar = (delta: number, keyboard: boolean) => {
    if (keyboard && uiStore.sidebarCollapsed && delta > 0) {
        uiStore.expandSidebar()
    } else if (keyboard && uiStore.sidebarWidth === SIDEBAR_MIN_WIDTH && delta < 0) {
        uiStore.collapseSidebar()
    } else {
        uiStore.resizeSidebar(sidebarResizeStartWidth + delta)
    }
}


</script>
<style lang="less" scoped>
.aside_box {
    // 侧栏水平栅格：图标列与文案列统一对齐（Logo / 菜单 / 会话分组 / 会话行）
    //
    // 下面这组变量只是「兜底值」，真实取值由 menu.vue 的 :style="sidebarStyleVars"
    // 按密度档位（compact / regular / relaxed）写到行内样式上，行内样式优先级更高。
    // 兜底值存在的意义：JS 还没跑起来的那一帧、或被样式隔离的环境里也不至于错位。
    --sidebar-inset-x: 12px;
    --sidebar-icon-size: 18px;
    --sidebar-channel-icon: 14px;
    --sidebar-icon-gap: 8px;
    --sidebar-row-height: 34px;
    --sidebar-logo-height: 30px;
    --sidebar-logo-max-width: 148px;
    --sidebar-section-gap: 16px;
    // OEM：给 macOS 红黄绿窗口按钮留出的顶部高度。仅桌面端（html.wails-desktop）
    // 生效，见下方 .aside_box 的 html.wails-desktop 覆盖规则。
    // 38px ≈ macOS 交通灯按钮中心线 + 上下各约 12px 呼吸，默认窗口下不会压到 Logo。
    --sidebar-titlebar-h: 38px;
    // OEM：交通灯中心线距窗口顶部的距离（pt）。
    // 决定 .sidebar_winctl 里折叠/搜索按钮的垂直位置——调到它就能让按钮
    // 与系统红黄绿按钮落在同一条水平线上（大 ⇒ 按钮下移）。
    //
    // 26px 是对真机窗口截图做像素测量得到的值（不是按 macOS 文档推算的）：
    //   截图上红绿灯落在像素 y=40..63（@2x），换算 CSS = 20..31.5，中心 25.75。
    // Windows 的交通灯并非贴顶，而是从 y≈20 才开始，所以理论值 16 会偏高、
    // 48 会偏低，实测 26 才是真正对齐的那条线。
    --sidebar-winctl-center: 26px;
    --sidebar-text-inset: calc(var(--sidebar-inset-x) + var(--sidebar-icon-size) + var(--sidebar-icon-gap));

    min-width: 0;
    width: var(--sidebar-width, 260px);
    flex-shrink: 0;
    padding: 8px 4px 6px;
    background: var(--td-bg-color-sidebar);
    box-sizing: border-box;
    /* Avoid 100vh because <html> carries a `zoom` multiplier for font-size
       control; 100vh is evaluated against the unscaled viewport and then
       scaled, so at "large" the sidebar would extend past the window. The
       ancestor chain (html/body/#app/.main) is already height: 100%. */
    height: 100%;
    overflow: visible;
    display: flex;
    flex-direction: column;
    border-right: 1px solid var(--td-component-stroke);
    box-shadow: 1px 0 0 rgba(0, 0, 0, 0.02);
    transition: width 0.25s ease, min-width 0.25s ease;
    position: relative;

    // OEM：内嵌标题栏（mac.TitleBarHiddenInset）下，红黄绿窗口按钮由系统绘制在
    // 窗口左上角、浮在 webview 之上。这里不再靠整条 padding-top 让位，
    // 而是让 .sidebar_winctl 这个专门的行去避开红绿灯（见下方），
    // 从而把「红绿灯同一行」的右侧空白利用起来放折叠/搜索按钮。
    // 折叠态没有这一行（红绿灯位置本来就是空的），所以仍然整条下移。
    // 若改回原生标题栏（TitleBarDefault），把 --sidebar-titlebar-h 设为 0 即可。
    html.wails-desktop & {
        padding-top: 0;
    }

    &--resizing {
        transition: none;
    }

    &.aside_box--collapsed {
        min-width: 48px;
        width: 48px;
        padding: 8px 0;
        overflow: visible;

        // OEM：折叠态没有 .sidebar_winctl（窄到放不下按钮，红绿灯位置本就是空的），
    // 所以这里仍然整条下移，避免首个图标被红黄绿按钮压住。
    //
    // 桌面端折叠宽度按「红黄绿按钮带」取 76px（不是浏览器的 48px）：
    //   · 系统按钮占窗口左上 ≈76x28，竖排图标居中于这 76px，和红黄绿在同一竖条里；
    //   · 右边缘正好落在按钮带右侧，右侧内容（标题、页面 header）不会再被压住，
    //     也不再需要给每个页面单独加左侧让位。
    // JS 里的 SIDEBAR_COLLAPSED_WIDTH 仍是 48（浏览器 / Web 形态），
    // 只有桌面端通过 html.wails-desktop 覆盖；--sidebar-width 变量只被 .aside_box 自己读，
    // 不会和这里打架。
    html.wails-desktop & {
        // 桌面端折叠宽度取 92px：左右对称留白，内容中心正好落在 46px —— 就是 macOS
        // 红黄绿按钮带的中心（展开态 .sidebar_winctl 的 spacer 是 76px：左 20 + 三颗 12
        // + 两处 8，按钮带 20..72，中心 46）。所以竖排图标和红黄绿在同一条竖线上，
        // 同时右侧多出 20px 余量，内容区（从 92px 起）不再紧贴按钮带。
        min-width: 92px;
        width: 92px;
        padding: var(--sidebar-titlebar-h, 38px) 0 6px;

        // 折叠态图标放大一档：16px 在 92px 栏里显得空，20px 与展开态视觉重量相当
        --sidebar-icon-size: 20px;

        .menu_item {
            width: 36px;
            height: 36px;
            margin: 0 auto 8px;
        }

        .menu_bottom {
            padding: 4px 0 8px;
        }
    }

        .menu_box {
            align-items: center;
        }

        .menu_item {
            justify-content: center;
            width: 32px;
            height: 32px;
            padding: 0;
            margin: 0 auto 6px;
            border-radius: var(--app-radius-sm);

            .menu_item-box {
                justify-content: center;
                width: auto;
            }

            .menu_icon {
                margin-right: 0;
                justify-content: center;
            }
        }

        .menu_item_active {
            border-radius: var(--app-radius-sm);
        }

        .menu_top {
            margin-right: 0;
            padding-right: 0;
        }
    }

    /* OEM：窗口控制条 —— 只在内嵌标题栏 + 展开态渲染。
       自身高度 = 红绿灯带高度（--sidebar-titlebar-h），左侧 spacer 让开系统按钮，
       右侧放折叠/搜索图标。鼠标可拖动窗口（系统按钮区域由 noDragSel 排除）。 */
    .sidebar_winctl {
        display: flex;
        // 本行的垂直几何按 macOS 交通灯位置来定（真机实测值，见 .aside_box 的
        // --sidebar-winctl-center）。
        //
        // 不用 align-items:center 让按钮随行居中：交通灯中心线并不在行高中点，
        // 居中会让按钮与红绿灯错位（真机实测偏高）。改用 align-items:flex-start
        // + 显式 padding-top 把按钮中心压到交通灯中心线上：
        //   按钮中心 = padding-top + 按钮高/2 = (center - 14) + 14 = center
        align-items: flex-start;
        justify-content: flex-end;
        // 行高必须容得下「按钮中心 = winctl-center」：
        //   center - 14 (按钮高一半) + 28 (按钮高) + 4 (下呼吸) = center + 18
        // 只写死 38px 的话，center=48 时按钮会溢出到下方 logo 行上。
        // 这里用 max() 取「标题栏带高度」和「按钮实际所需高度」的较大值：
        //   · center=16 时 → max(38, 16+18=34) = 38（保持原布局）
        //   · center=48 时 → max(38, 48+18=66) = 66（自动加高，不压 logo）
        min-height: var(--sidebar-titlebar-h, 38px);
        height: max(var(--sidebar-titlebar-h, 38px), calc(var(--sidebar-winctl-center, 48px) + 18px));
        padding-top: calc(var(--sidebar-winctl-center, 48px) - 14px);
        // 必须是 0 0 auto：flex 纵向容器里 flex-grow 会拉伸高度，
        // 写 flex:1 1 auto 时 height 会被忽略、这一行被撑到 300px+（实测踩过）。
        flex: 0 0 auto;
        // 桌面端 .aside_box 的 padding-top 被设为 0（见上方 html.wails-desktop 覆盖），
        // 所以这里不需要负 margin 抵消；加了反而会把整行顶出窗口上边缘。
        margin: 0 0 var(--sidebar-subgap, 8px) 0;
        padding-right: 2px;
        box-sizing: border-box;
        // 内嵌标题栏下 webview 铺满窗口，这一条的空白处要能拖动窗口。
        // 按钮容器单独排除，否则点不动。
        -webkit-app-region: drag;

        // spacer：让开 macOS 红黄绿三颗按钮（系统绘制在 webview 之上）
        // 76px ≈ 左内边距 20 + 三颗 12px 按钮 + 两处 8px 间距 + 右侧留白
        &__spacer {
            flex: 0 0 auto;
            width: 76px;
        }

        .logo_actions {
            -webkit-app-region: no-drag;
        }
    }

    .logo_row {
        display: flex;
        align-items: center;
        justify-content: space-between;
        gap: var(--sidebar-icon-gap);
        // 高度由 Logo 实际高度 + 上下呼吸空间决定，不再写死 39px。
        // 旧写法的问题：Logo 是竖版图，按 width:108px 反推出 96px 高，
        // 塞进固定 39px 的容器里会被 overflow:hidden 裁掉，只剩图标顶部一截。
        min-height: calc(var(--sidebar-logo-height) + 16px);
        margin: 0 0 var(--sidebar-subgap, 8px);
        flex-shrink: 0;
        padding: 0 2px 0 var(--sidebar-inset-x);
    }

    .sidebar-toggle {
        display: flex;
        align-items: center;
        justify-content: center;
        width: 28px;
        height: 28px;
        flex-shrink: 0;
        cursor: pointer;
        color: var(--td-text-color-secondary);
        border-radius: var(--app-radius-xs);
        transition: background-color var(--app-motion-base) ease;
        box-sizing: border-box;

        &:hover {
            background: var(--td-bg-color-container-hover);
            color: var(--td-text-color-primary);
        }
    }

    .logo_box {
        display: flex;
        align-items: center;
        flex: 1;
        min-width: 0;
        // 不再裁切：Logo 高度已经受控，溢出属于配置错误，应暴露而不是静默吃掉
        overflow: visible;

        /*
         * 圆形 badge：品牌图标的正圆容器。
         *
         * 尺寸只由 --sidebar-logo-height 决定（各密度档位 30/34/38px），
         * 宽高强制相等才能是正圆——不能靠素材比例，否则换图就变椭圆。
         * 白底 + 1px 描边托住透明边角；描边用 --td-component-stroke，
         * 深浅色模式自动跟随。图案用 contain 居中缩放，任何比例都不变形。
         */
        .logo-badge {
            display: inline-flex;
            align-items: center;
            justify-content: center;
            flex-shrink: 0;
            width: var(--sidebar-logo-height);
            height: var(--sidebar-logo-height);
            border-radius: 50%;
            overflow: hidden;
            background: var(--td-bg-color-container);
            border: 1px solid var(--td-component-stroke);
            box-sizing: border-box;

            &__img {
                width: 100%;
                height: 100%;
                object-fit: contain;
                display: block;
                /* 图案本身留白不多，稍放大以贴合圆形边缘，避免看起来"缩成一团" */
                transform: scale(1.08);
            }

            // 图片加载失败：用品牌首字兜底，保持圆形轮廓不变
            &--fallback {
                font-size: calc(var(--sidebar-logo-height) * 0.44);
                font-weight: 700;
                color: var(--td-brand-color);
                letter-spacing: 0;
            }
        }

        // 产品名：与图标垂直居中对齐，字号跟随侧栏密度
        .logo-name {
            margin-left: 8px;
            font-size: var(--app-text-md);
            font-weight: 700;
            letter-spacing: 0.2px;
            color: var(--td-text-color-primary);
            white-space: nowrap;
            overflow: hidden;
            text-overflow: ellipsis;
            min-width: 0;
            flex-shrink: 1;

            // 品牌名 + 版本号纵向两行：横向并排会把品牌名挤成「知识工...」
            // （实测可用宽度只有 62px，而文字本身需要 66px）
            display: flex;
            flex-direction: column;
            align-items: flex-start;
            justify-content: center;
            line-height: 1.25;

            &__text {
                max-width: 100%;
                overflow: hidden;
                text-overflow: ellipsis;
            }

            // 版本号放在第二行，弱化成辅助信息，不抢品牌名的视觉权重
            &__version {
                font-size: 11px;
                font-weight: 400;
                letter-spacing: 0;
                color: var(--td-text-color-placeholder);
            }
        }

        .lite-badge {
            margin-left: 2px;
            align-self: flex-start;
            margin-top: 2px;
            font-size: 9px;
            font-weight: 600;
            color: var(--td-text-color-placeholder);
            user-select: none;
            white-space: nowrap;
        }
    }

    .menu_top {
        flex: 1;
        display: flex;
        flex-direction: column;
        overflow-y: auto;
        overflow-x: hidden;
        min-height: 0;
        // 菜单区与上方（Logo / 空间选择器）之间留出档位化的间距，
        // 原来 4-6px 在信息密集时显得挤，宽屏又过于紧凑。
        // 注意：这里只管「项与项」的微间距——分组间距不要加在 .menu_box 上，
        // 因为每个菜单项本身也是一个 .menu_box，那样会让三个菜单项之间各多出
        // 一个 section-gap（实测间距从 1px 变成 19px）。
        gap: 2px;
        // 抵消 .aside_box 的右内边距，让滚动条贴近面板右缘；
        // 等量 padding 补回，保证列表文字位置不变。
        margin-right: -4px;
        padding-right: 4px;

        // Claude 风格细滚动条：默认透明，悬浮时显示一条圆角细灰条
        scrollbar-width: thin;
        scrollbar-color: transparent transparent;
        transition: scrollbar-color var(--app-motion-base) ease;

        &::-webkit-scrollbar {
            width: 6px;
        }

        &::-webkit-scrollbar-track {
            background: transparent;
        }

        &::-webkit-scrollbar-thumb {
            background-color: transparent;
            border-radius: var(--app-radius-sm);
            transition: background-color var(--app-motion-base) ease;
        }

        &:hover {
            scrollbar-color: var(--td-scrollbar-color) transparent;

            &::-webkit-scrollbar-thumb {
                background-color: var(--td-scrollbar-color);
            }
        }

        &::-webkit-scrollbar-thumb:hover {
            background-color: var(--td-scrollbar-hover-color);
        }
    }

    .menu_bottom {
        flex-shrink: 0;
        display: flex;
        flex-direction: column;
        padding: 4px 8px 8px;
    }

    .menu_box {
        display: flex;
        flex-direction: column;

        // 「新对话」吸顶：作为滚动容器(.menu_top)的直接子级，滚动时钉在顶部，
        // 知识库/智能体/共享空间及历史列表一起从其下方滚走。背景遮挡滚动内容。
        &--sticky {
            position: sticky;
            top: 0;
            z-index: 2;
            background: var(--td-bg-color-sidebar);
        }
    }


    .active-upload {
        color: var(--td-brand-color);
    }

    .menu_item_active {
        border-radius: var(--app-radius-xs);
        background: var(--td-bg-color-secondarycontainer) !important;

        .menu_icon,
        .menu_title {
            color: var(--td-brand-color) !important;
        }
    }

    .menu_item_c_active {

        .menu_icon,
        .menu_title {
            color: var(--td-text-color-primary);
        }
    }

    .menu_item {
        cursor: pointer;
        display: flex;
        align-items: center;
        justify-content: space-between;
        // 行高走密度变量：窄屏自动收紧，避免菜单区把会话列表挤没
        height: var(--sidebar-row-height);
        padding: 6px 8px 6px var(--sidebar-inset-x);
        box-sizing: border-box;
        margin-bottom: 1px;
        border-radius: var(--app-radius-xs);
        transition: background-color var(--app-motion-base) ease;

        .menu_item-box {
            display: flex;
            align-items: center;
        }

        &:hover {
            border-radius: var(--app-radius-xs);
            background: var(--td-bg-color-container-hover);

            .menu_icon,
            .menu_title {
                color: var(--td-text-color-primary);
            }
        }
    }

    .menu_icon {
        display: flex;
        align-items: center;
        justify-content: center;
        flex: 0 0 var(--sidebar-icon-size);
        width: var(--sidebar-icon-size);
        height: var(--sidebar-icon-size);
        margin-right: var(--sidebar-icon-gap);
        color: var(--td-text-color-secondary);

        .icon {
            // 尺寸兜底修正：原来这里写死 18px，导致改 --sidebar-icon-size 时
            // 容器缩了而图标没缩，出现「图标顶到边框」的观感。
            // 现在两者都读同一个变量，并彼此封顶，永不溢出容器。
            width: var(--sidebar-icon-size);
            height: var(--sidebar-icon-size);
            max-width: 100%;
            max-height: 100%;
            overflow: hidden;
            flex-shrink: 0;
        }

        // 图形几乎占满画布的图标缩小显示，否则会比其他图标大一圈
        &.menu_icon--zhishiku .icon,
        &.menu_icon--organization .icon,
        .icon.icon--toggle {
            // 收缩量按比例算（-2px 对应 18px 档的 16px），换档位时依然协调
            width: calc(var(--sidebar-icon-size) - 2px);
            height: calc(var(--sidebar-icon-size) - 2px);
        }
    }

    .menu_title {
        color: var(--td-text-color-primary);
        text-overflow: ellipsis;
        font-family: var(--app-font-family);
        font-size: var(--app-text-base);
        font-style: normal;
        font-weight: 500;
        line-height: 20px;
        overflow: hidden;
        white-space: nowrap;
        // 上限跟着侧栏可用宽度走，窄屏时不至于把「知识库」也截成「知…」
        max-width: 100%;
        min-width: 0;
        flex: 1;
    }

    .submenu {
        position: relative;
        font-family: var(--app-font-family);
        font-size: var(--app-text-base);
        font-style: normal;
        min-width: 0;
        padding-top: 3px;
    }

    :deep(.submenu_pin_icon) {
        color: inherit;
        font-size: var(--app-text-sm);
        margin-right: 4px;
        vertical-align: middle;
        flex-shrink: 0;
    }

    .submenu_source_icon {
        width: 14px;
        height: 14px;
        margin-right: 0px;
        vertical-align: middle;
        object-fit: contain;
        flex-shrink: 0;
        // 默认淡化处理，避免未选中状态下彩色图标与灰色标题不协调；
        // 悬浮或选中时恢复彩色，交互时才引人注意。
        filter: grayscale(1);
        opacity: 0.55;
        transition: filter var(--app-motion-fast) ease, opacity var(--app-motion-fast) ease;
    }

    :deep(.submenu_item:hover .submenu_source_icon),
    :deep(.submenu_item_active .submenu_source_icon) {
        filter: none;
        opacity: 1;
    }

    // 列表行统一栅格：左缘 inset-x + 图标槽 18px + 间距 8px → 文案列与主菜单文字对齐
    .session-list-row {
        display: flex;
        align-items: center;
        gap: var(--sidebar-icon-gap);
        padding: 0 8px 0 var(--sidebar-inset-x);
        min-width: 0;
        box-sizing: border-box;
    }

    .session-list-row__icon {
        flex: 0 0 var(--sidebar-icon-size);
        width: var(--sidebar-icon-size);
        height: var(--sidebar-icon-size);
        display: inline-flex;
        align-items: center;
        justify-content: center;
        flex-shrink: 0;
    }

    .session-list-row__body {
        flex: 1 1 auto;
        min-width: 0;
        overflow: hidden;
    }

    // 聊天区分组标题 / 会话行：与「聊天」节标题同列左对齐，不再预留图标槽
    .session-list-row--flat {
        padding-left: var(--sidebar-inset-x);
        gap: 0;
    }

    .session-list-loading {
        display: flex;
        align-items: center;
        min-height: 26px;
        color: var(--td-text-color-placeholder);
    }

    .timeline_header {
        font-family: var(--app-font-family);
        font-size: var(--app-text-xs);
        font-weight: 600;
        color: var(--td-text-color-disabled);
        padding-top: 4px;
        padding-bottom: 1px;
        margin-top: 0;
        line-height: 16px;
        user-select: none;
    }

    .timeline_header-label {
        white-space: nowrap;
    }

    // 项目区：紧跟在「系统设置」菜单项之后，但与上面几个功能项是不同性质的
    // 内容（功能入口 vs 资源树），所以用「分隔线 + 上下大留白」明确分成两块，
    // 避免项目标题被误认成又一个菜单项。
    .menu_box--projects {
        padding: 0 var(--menu-item-inline-pad, 10px) 6px;
    }

    // 功能项与项目区之间的分隔线。左右各内缩 6px，让线不比菜单项宽，
    // 看起来是「分组线」而不是表格边框。
    .menu_box__divider {
        height: 1px;
        margin: 12px 6px 14px;
        background: var(--td-component-stroke);
    }

    // 项目内的会话行：作为二级层级，字号与颜色都明显弱于项目名（13px/primary），
    // 靠「更小字号 + 更浅颜色 + 缩进」三重信号表达从属关系，而不是只靠缩进。
    .project-sessions-slot {
        display: flex;
        flex-direction: column;
        gap: 1px;
        // 上方不留空：紧接目录行，让「项目名 → 目录 → 会话」读作一个整体
        padding: 2px 0 4px;
    }

    // 缩进跟随项目名的文字列（行内 2px + 图标槽 + 间距），再多 8px 作为
    // 「我是子项」的从属暗示。项目行图标槽与主菜单同档（--sidebar-icon-size），
    // 所以这里的缩进会随侧栏密度自动变化，不需要改魔数。
    .project-session-row .session-list-row {
        padding-left: calc(10px + var(--sidebar-icon-size, 18px) + var(--sidebar-icon-gap, 8px));
        padding-right: 6px;
    }

    // 会话行高度与字号：比历史列表（34px/14px）更矮更小，读起来是「子项」。
    // 注意特异性：基础规则写的是 ".submenu_item_p.session-chat-row .session-list-row"
    // （高度 34px）与 ":deep(.submenu_item)"（字号 14px），两处都是 (0,3,0) 级别，
    // 所以这里必须同带 .submenu_item_p 才压得住，光写 .project-session-row 会被盖回去。
    .submenu_item_p.project-session-row.session-chat-row {
        margin-bottom: 0;
        margin-top: 0;

        > .session-list-row {
            height: 28px;
            border-radius: var(--app-radius-sm);
        }

        :deep(.submenu_item),
        :deep(.submenu_title-text) {
            // 13px：比一级项（14px）小一档，仍是能舒服阅读的正文字号，
            // 与项目名的 14px 形成清晰的一级/二级关系而不至于碎得看不清。
            font-size: var(--app-text-md);
            line-height: 20px;
            color: var(--td-text-color-secondary);
        }

        // 运行中/未读小圆点也缩小一档，跟着字号一起降级，否则圆点会把行撑高
        :deep(.session-row-badge) {
            transform: scale(0.9);
            transform-origin: center right;
        }

        // 当前打开的会话在项目内也要能一眼认出来
        &.session-chat-row--active > .session-list-row {
            background: var(--td-bg-color-container-hover);

            :deep(.submenu_item),
            :deep(.submenu_title-text) {
                color: var(--td-brand-color);
                font-weight: 500;
            }
        }
    }

    .project-sessions-empty {
        // 与上面会话文字同列，让「暂无对话」落在子项那一列而不是另起一块
        padding: 2px 6px 2px calc(12px + var(--sidebar-icon-size, 18px) + var(--sidebar-icon-gap, 8px));
        font-size: var(--app-text-md);
        line-height: 20px;
        color: var(--td-text-color-placeholder);
    }

    // Stable filter control: always mounted and absolutely pinned to the list's
    // top-right so it visually sits on the first row (e.g. beside "近30天") and
    // never jumps when switching session type reloads a bucket. It overlays the
    // empty right side of the first header row, so it needs no reserved height.
    .session-list-scope-header {
        position: absolute;
        top: 4px;
        right: 8px;
        z-index: 2;
        display: flex;
        justify-content: flex-end;
        max-width: calc(100% - var(--sidebar-inset-x) - 8px);

        :deep(.session-source-filter--inline) {
            flex: 0 1 auto;
            min-width: 0;
            max-width: 100%;
            opacity: 0;
            transition: opacity var(--app-motion-fast) ease;
        }
    }

    .submenu:hover .session-list-scope-header :deep(.session-source-filter--inline),
    .session-list-scope-header:hover :deep(.session-source-filter--inline),
    .session-list-scope-header:focus-within :deep(.session-source-filter--inline),
    .session-list-scope-header :deep(.session-source-filter--inline.session-source-filter--emphasized) {
        opacity: 1;
    }

    .submenu_item_p {
        padding: 0;
        box-sizing: border-box;
        min-width: 0;
        overflow: hidden;

        &.session-chat-row {
            margin-bottom: 1px;
        }

        &.session-chat-row .session-list-row {
            height: 34px;
            padding-right: 4px;
            border-radius: var(--app-radius-sm);
            transition: background var(--app-motion-fast) ease, color var(--app-motion-fast) ease;
        }

        &.session-chat-row--revealed {
            animation: session-fork-enter 280ms ease-out both;
        }

        &.session-chat-row:hover .session-list-row {
            background: var(--td-bg-color-container-hover);

            :deep(.menu-more) {
                color: var(--td-text-color-primary);
            }

        }

        &.session-chat-row--active .session-list-row {
            background: var(--td-bg-color-container-hover);

            :deep(.submenu_item) {
                color: var(--td-brand-color);
            }

            :deep(.menu-more) {
                color: var(--td-text-color-primary);
            }
        }

        &.session-chat-row--selected .session-list-row {
            background: color-mix(in srgb, var(--td-brand-color) 5%, transparent);
        }
    }

    // SessionSidebarRow 为子组件，需 :deep 才能让标题省略号生效
    :deep(.submenu_item) {
        cursor: pointer;
        display: flex;
        align-items: center;
        color: var(--td-text-color-primary);
        font-weight: 400;
        font-size: var(--app-text-base);
        line-height: 20px;
        height: 100%;
        width: 100%;
        padding: 0;
        position: relative;
        min-width: 0;
        background: transparent;

        .submenu_title {
            display: flex;
            align-items: center;
            flex: 1 1 auto;
            min-width: 0;
            overflow: hidden;
        }

        .submenu_title-text {
            flex: 1 1 auto;
            min-width: 0;
            overflow: hidden;
            white-space: nowrap;
            text-overflow: ellipsis;
        }

        .menu-more-wrap {
            transition: opacity var(--app-motion-base) ease;
            flex-shrink: 0;
        }

        .menu-more {
            display: inline-block;
            font-weight: bold;
            color: var(--td-brand-color);
        }

        .submenu_title--batch {
            margin-left: 4px;
        }

        &.submenu_item_batch {
            padding-left: 0;
        }
    }

    :deep(.submenu_item_batch) {
        cursor: pointer;
        user-select: none;
    }

    .batch-checkbox {
        flex-shrink: 0;
    }

}

.batch-inline-footer {
    flex-shrink: 0;
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 6px 12px;
    border-top: 1px solid var(--td-component-stroke);
    background: var(--td-bg-color-container);

    .batch-footer-left {
        display: flex;
        align-items: center;
        font-size: var(--app-text-md);
        color: var(--td-text-color-placeholder);
    }

    .batch-footer-right {
        display: flex;
        align-items: center;
        gap: 6px;
    }
}

.menu_item-box {
    display: flex;
    align-items: center;
    width: 100%;
    position: relative;
}

/* Empty state when there are no sessions. */
.submenu_empty {
    padding: 24px 14px;
    text-align: center;
    font-size: var(--app-text-sm);
    color: var(--td-text-color-placeholder);
    user-select: none;
}

// 顶部 logo_row 右侧的图标按钮组（搜索 + 折叠），与折叠按钮风格一致
.logo_actions {
    display: flex;
    align-items: center;
    gap: 2px;
    flex-shrink: 0;
}

.aside_box--collapsed .assistant-sidebar-entry {
    display: flex;
    width: 32px;
    height: 32px;
    margin: 0 auto 6px;
}

.header-icon-btn {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 28px;
    height: 28px;
    flex-shrink: 0;
    cursor: pointer;
    border-radius: var(--app-radius-sm);
    color: var(--td-text-color-secondary);
    transition: background-color var(--app-motion-base) ease;
    box-sizing: border-box;

    &:hover {
        background: var(--td-bg-color-container-hover);
    }

    &.is-active {
        background: var(--td-bg-color-container-hover);
        color: var(--td-brand-color);
    }

    .header-icon-img {
        width: 18px;
        height: 18px;
        display: block;
    }
}

// 深色 tooltip 内容：标签 + 浅灰快捷键内联
.cmdk-tip {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    white-space: nowrap;

    .cmdk-tip-label {
        font-size: var(--app-text-md);
    }

    .cmdk-tip-keys {
        font-size: var(--app-text-md);
        opacity: 0.6;
        letter-spacing: 0.5px;
    }
}

.menu-toolbox-stack {
    display: inline-flex;
    align-items: center;
    flex-shrink: 0;
    margin-left: auto;
}

.menu-toolbox-stack__item {
    position: relative;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 20px;
    height: 20px;
    box-sizing: border-box;
    border: 1px solid var(--td-component-stroke);
    border-radius: 50%;
    background: var(--td-bg-color-container);
    color: var(--td-text-color-secondary);
    rotate: var(--stack-rotate, 0deg);
    --stack-spring: cubic-bezier(0.34, 1.56, 0.64, 1);
    animation: menu-toolbox-stack-in 420ms var(--stack-spring) both;
    animation-delay: var(--stack-delay, 0ms);
    transition:
        margin var(--app-motion-slow) var(--stack-spring),
        rotate var(--app-motion-slow) var(--stack-spring),
        translate var(--app-motion-slow) var(--stack-spring),
        color var(--app-motion-base) ease,
        box-shadow var(--app-motion-base) ease;
    transition-delay: var(--stack-delay, 0ms);

    & + & {
        margin-left: -6px;
    }

    &:nth-child(1) { z-index: 3; --stack-rotate: -10deg; }
    &:nth-child(2) { z-index: 2; --stack-delay: 50ms; }
    &:nth-child(3) { z-index: 1; --stack-rotate: 10deg; --stack-delay: 100ms; }
}

.menu-toolbox-stack__status {
    position: absolute;
    right: -1px;
    bottom: -1px;
    width: 6px;
    height: 6px;
    border-radius: var(--app-radius-pill);
    box-shadow: 0 0 0 1.5px var(--td-bg-color-container);

    &.is-connected {
        background: var(--td-success-color);
    }

    &.is-offline {
        background: var(--td-warning-color);
    }
}

@keyframes menu-toolbox-stack-in {
    from {
        opacity: 0;
        scale: 0.4;
    }
}

.menu_item:hover .menu-toolbox-stack__item {
    color: var(--td-text-color-primary);
    rotate: 0deg;
    translate: 0 -1px;
    box-shadow: 0 2px 6px rgba(0, 0, 0, 0.08);
}

.menu_item:hover .menu-toolbox-stack__item + .menu-toolbox-stack__item {
    margin-left: 3px;
}

@media (prefers-reduced-motion: reduce) {
    .menu-toolbox-stack__item {
        animation: none;
        transition: color var(--app-motion-base) ease;
    }
}

.menu-pending-badge {
    min-width: 18px;
    height: 18px;
    padding: 0 5px;
    margin-left: 6px;
    border-radius: 9px;
    background: rgba(250, 173, 20, 0.2);
    color: var(--td-warning-color);
    font-size: var(--app-text-sm);
    font-weight: 600;
    line-height: 18px;
    text-align: center;
    flex-shrink: 0;
}

.menu_box {
    position: relative;
}

@keyframes session-fork-enter {
    from { opacity: 0; transform: translateX(-10px); }
    to { opacity: 1; transform: translateX(0); }
}

@media (prefers-reduced-motion: reduce) {
    .aside_box .submenu_item_p.session-chat-row--revealed {
        animation: none;
    }
}
</style>
<style lang="less">
// Dark mode: invert dark logo to light
html[theme-mode="dark"] .aside_box .logo_box .logo {
    filter: invert(1) hue-rotate(180deg);
}

// Dark mode: 滚动条在深色背景下需要更亮的颜色才看得见
html[theme-mode="dark"] .aside_box .menu_top:hover {
    scrollbar-color: rgba(255, 255, 255, 0.22) transparent;
}

html[theme-mode="dark"] .aside_box .menu_top:hover::-webkit-scrollbar-thumb {
    background-color: rgba(255, 255, 255, 0.22);
}

html[theme-mode="dark"] .aside_box .menu_top::-webkit-scrollbar-thumb:hover {
    background-color: rgba(255, 255, 255, 0.38);
}

// Dark mode: invert the top search icon button image to match text color
html[theme-mode="dark"] .aside_box .header-icon-img {
    filter: invert(1);
    opacity: 0.55;
}

html[theme-mode="dark"] .aside_box .header-icon-btn:hover .header-icon-img {
    opacity: 0.9;
}

// Dark mode: make SVG icons match text color (loaded via <img>, currentColor won't work)
html[theme-mode="dark"] .aside_box .menu_icon img.icon {
    filter: invert(1);
    opacity: 0.55;
}

// Hover state: brighter icon like text
html[theme-mode="dark"] .aside_box .menu_item:hover .menu_icon img.icon {
    opacity: 0.9;
}

// menu_item_c_active: text is primary, so icon should match
html[theme-mode="dark"] .aside_box .menu_item_c_active .menu_icon img.icon {
    opacity: 0.9;
}

// Active (green) icons should not be inverted
html[theme-mode="dark"] .aside_box .menu_item_active .menu_icon img.icon {
    filter: none;
    opacity: 1;
}

// 下拉菜单样式已统一至 @/assets/dropdown-menu.less

// 退出登录确认框样式
:deep(.t-popconfirm) {
    .t-popconfirm__content {
        background: var(--td-bg-color-container);
        border: 1px solid var(--td-component-stroke);
        border-radius: var(--app-radius-sm);
        box-shadow: var(--td-shadow-3);
        padding: 12px 16px;
        font-size: var(--app-text-base);
        color: var(--td-text-color-primary);
        max-width: 200px;
    }

    .t-popconfirm__arrow {
        border-bottom-color: var(--td-component-stroke);
    }

    .t-popconfirm__arrow::after {
        border-bottom-color: var(--td-bg-color-container);
    }

    .t-popconfirm__buttons {
        margin-top: 8px;
        display: flex;
        justify-content: flex-end;
        gap: 8px;
    }

    .t-button--variant-outline {
        border-color: var(--td-component-border);
        color: var(--td-text-color-secondary);
    }

    .t-button--theme-danger {
        background-color: var(--td-error-color);
        border-color: var(--td-error-color);
    }

    .t-button--theme-danger:hover {
        background-color: var(--td-error-color);
        border-color: var(--td-error-color);
    }
}
</style>
