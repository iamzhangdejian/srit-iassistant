import { defineStore } from 'pinia';
import { ref, computed } from 'vue';
import {
  listProjects,
  createProject,
  updateProject,
  deleteProject,
  setDefaultProject,
  type Project,
  type DeleteProjectSessions,
} from '@/api/project';
import { pickHostProjectDir } from '@/utils/desktopProjectDir';
import { projectDirBasename } from '@/utils/hostWorkspace';

/**
 * 项目 store：工作空间 → 项目 → 会话 三层结构的数据源。
 *
 * 设计取舍：
 * - 项目数量很小（一个工作空间通常几个到几十个），一次性全量拉取比按需分页
 *   更简单，也避免侧栏展开时抖动。
 * - 会话仍然走原有的分页接口，这里只做「按 project_id 归组」，不重复抓取。
 * - 所有写操作都先落服务端再刷新本地列表，避免乐观更新失败后界面与服务端不一致。
 */
export const useProjectStore = defineStore('project', () => {
  const projects = ref<Project[]>([]);
  const loading = ref(false);
  const loaded = ref(false);
  /** 单一飞行中的请求：侧栏在挂载与切换空间时都会触发刷新。 */
  let inflight: Promise<void> | null = null;

  const defaultProject = computed(() => projects.value.find((p) => p.is_default) ?? null);

  /**
   * 拉取项目列表。force=false 时命中缓存直接返回，用于侧栏反复展开收起。
   */
  async function fetchProjects(force = false): Promise<void> {
    if (!force && loaded.value) return;
    if (inflight) return inflight;
    loading.value = true;
    inflight = (async () => {
      try {
        projects.value = await listProjects();
        loaded.value = true;
      } catch (err) {
        // 后端不可达时保留旧列表：清空会让侧栏闪成「还没有项目」，
        // 用户会误以为自己刚才建的项目丢了。
        console.warn('[project] 加载项目列表失败', err);
      } finally {
        loading.value = false;
        inflight = null;
      }
    })();
    return inflight;
  }

  /**
   * 弹出系统目录选择框并创建项目。
   *
   * 目录必须经选择框授权后才进入后端白名单，所以这里绝不能允许手输路径。
   * 返回 null 表示用户取消了选择（不是错误，调用方不应弹提示）。
   */
  async function createFromPicker(): Promise<Project | null> {
    const dir = await pickHostProjectDir();
    if (!dir) return null;
    const project = await createProject({
      // 项目名默认取目录名，用户后续可改名。
      name: projectDirBasename(dir),
      host_dir: dir,
    });
    projects.value = [...projects.value, project];
    return project;
  }

  async function rename(id: string, name: string): Promise<void> {
    const updated = await updateProject(id, { name });
    replaceLocal(updated);
  }

  async function makeDefault(id: string, isDefault: boolean): Promise<void> {
    await setDefaultProject(id, isDefault);
    // 默认项目在一处变为 true 时其他项目必须变 false：本地同步镜像这个不变量，
    // 免得再拉一次全量。
    projects.value = projects.value.map((p) => {
      if (p.id === id) return { ...p, is_default: isDefault };
      return isDefault ? { ...p, is_default: false } : p;
    });
  }

  /**
   * 删除项目。sessions 决定项目内会话的去向，默认 refuse（还有会话就拒绝）。
   * 注意：只删数据库记录，本地目录永远不会被程序删除。
   */
  async function remove(
    id: string,
    sessions: DeleteProjectSessions = 'refuse',
    moveTo?: string,
  ): Promise<void> {
    await deleteProject(id, { sessions, move_to: moveTo });
    projects.value = projects.value.filter((p) => p.id !== id);
  }

  function replaceLocal(updated: Project) {
    projects.value = projects.value.map((p) => (p.id === updated.id ? { ...p, ...updated } : p));
  }

  /** 退出登录或切换空间时清空，避免把上一个空间的项目带过去。 */
  function reset() {
    projects.value = [];
    loaded.value = false;
    inflight = null;
  }

  return {
    projects,
    loading,
    loaded,
    defaultProject,
    fetchProjects,
    createFromPicker,
    rename,
    makeDefault,
    remove,
    reset,
  };
});