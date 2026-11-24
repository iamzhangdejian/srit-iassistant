import { get, post, put, del } from "@/utils/request";

/**
 * 项目（工作空间 → 项目 → 会话 的中间层）。
 *
 * 一个项目绑定一个本地目录；项目下的会话创建时继承该目录。
 * 目录必须经由系统目录选择框授权，后端 `hostdir.MatchApproved` 做精确匹配，
 * 因此前端不能凭空拼一个路径提交上去——必须先走 pickHostProjectDir()。
 */
export interface Project {
  id: string;
  name: string;
  description: string;
  /** 关联的本地目录绝对路径；创建后不可更改。 */
  host_dir: string;
  /** 是否为新建会话的默认归属项目，每个工作空间最多一个。 */
  is_default: boolean;
  sort_order: number;
  session_count: number;
  /** 最近一次会话活动时间；没有会话时为 null。 */
  last_session_at: string | null;
  created_at: string;
  updated_at: string;
}

export interface CreateProjectRequest {
  /** 留空则由目录名自动生成。 */
  name?: string;
  description?: string;
  host_dir: string;
}

export interface UpdateProjectRequest {
  name?: string;
  description?: string;
  sort_order?: number;
}

/** 删除项目时会话的去向：默认拒绝（refuse），可移动或一并删除。 */
export type DeleteProjectSessions = "refuse" | "move" | "delete";

export interface DeleteProjectRequest {
  sessions?: DeleteProjectSessions;
  /** sessions 为 move 时的目标项目；留空表示会话脱离项目但保留。 */
  move_to?: string;
}

export async function listProjects() {
  // 响应拦截器返回的是后端原文 { success, data }，不是裸数组。
  const res = await get<{ success: boolean; data: Project[] }>("/api/v1/projects");
  // 后端保证 data 是数组；这里再兜一层，避免空库时把 null 传进 v-for。
  return Array.isArray(res?.data) ? res.data : [];
}

export async function createProject(data: CreateProjectRequest): Promise<Project> {
  const res = await post<{ success: boolean; data: Project }>("/api/v1/projects", data);
  return res.data;
}

export async function getProject(id: string): Promise<Project> {
  const res = await get<{ success: boolean; data: Project }>(`/api/v1/projects/${encodeURIComponent(id)}`);
  return res.data;
}

export async function updateProject(id: string, data: UpdateProjectRequest): Promise<Project> {
  const res = await put<{ success: boolean; data: Project }>(`/api/v1/projects/${encodeURIComponent(id)}`, data);
  return res.data;
}

export async function deleteProject(id: string, data: DeleteProjectRequest = {}) {
  return del(`/api/v1/projects/${encodeURIComponent(id)}`, data);
}

export async function setDefaultProject(id: string, isDefault: boolean) {
  return put(`/api/v1/projects/${encodeURIComponent(id)}/default`, { default: isDefault });
}