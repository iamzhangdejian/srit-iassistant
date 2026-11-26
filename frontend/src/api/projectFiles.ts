import { get, getDown, getRangeChunk, post } from '@/utils/request'

/**
 * 项目文件列表的一行。字段与后端 handler/host_project_files.go 的
 * projectTreeEntry 一一对应。
 */
export interface ProjectFileEntry {
  /** 绝对路径，下载与预览都用它。 */
  path: string
  /** 相对项目根目录的路径，UI 显示用。 */
  rel: string
  name: string
  kind: 'file' | 'dir'
  /** 相对根目录的层级，根的直接子项为 1。用于缩进。 */
  depth: number
  size: number
  mod_time: string
  /** 目录、软链接、超过 200MB 的文件为 false，前端置灰。 */
  downloadable: boolean
  /** 该层条目被数量上限截断。 */
  truncated?: boolean
}

export interface ProjectFileList {
  root: string
  entries: ProjectFileEntry[]
  total: number
  /** 整个列表被条目上限截断，UI 应提示只显示了前 N 个。 */
  truncated: boolean
}

/**
 * 列出某个已授权项目目录下的文件。
 *
 * dir 必须精确等于用户通过系统目录选择框授权过的目录，否则后端返回 403。
 * 后端只做只读遍历，不创建、不修改、不删除任何文件。
 */
export async function listProjectFiles(dir: string) {
  // 拦截器只做透传，业务数据在 data 字段里（与 project.ts 的写法一致）。
  const res = await get<{ data: ProjectFileList }>('/api/v1/system/project-files', {
    params: { dir },
  })
  return res.data
}

/**
 * 下载项目目录里的一个文件。
 *
 * 走 getDown 而不是 <a href>：axios 会带上 Bearer token，直接用链接会 401。
 */
export async function downloadProjectFile(dir: string, path: string): Promise<Blob> {
  const query = new URLSearchParams({ dir, path })
  return getDown(`/api/v1/system/project-files/download?${query.toString()}`)
}
/**
 * 按字节区间读取项目文件（大文件预览用）。`total` 只在第一次调用时可信，
 * 之后每次都会重新解析 Content-Range，调用方缓存第一个即可。
 */
export async function fetchProjectFileRange(
  dir: string,
  path: string,
  begin: number,
  end: number,
): Promise<{ chunk: Uint8Array; total: number }> {
  const query = new URLSearchParams({ dir, path })
  return getRangeChunk(`/api/v1/system/project-files/download?${query.toString()}`, begin, end)
}

/**
 * 探测项目文件的真实长度：只取第 0 个字节，读 `Content-Range` 的总长度。
 * 列表接口已带 size，但预览前再确认一次可以避免「列表是旧的、文件刚被覆盖」。
 */
export async function probeProjectFileSize(dir: string, path: string): Promise<number> {
  const { total } = await fetchProjectFileRange(dir, path, 0, 1)
  return total
}

/** 一次 @ 引用登记的结果。 */
export interface ProjectFileRef {
  attachment_id: string
  file_name: string
  file_size: number
  kind: 'file' | 'dir'
}

/**
 * 把一个项目文件（或目录清单）登记成当前会话的临时附件。
 *
 * 服务端直接读磁盘，不经过浏览器上传：文件本来就在同一台机器上，
 * 传一遍既慢又浪费内存。拿到 attachment_id 后与普通附件走完全相同的
 * 解析与上下文注入路径。
 */
export async function createProjectFileRef(
  sessionId: string,
  params: { dir: string; path: string; kind: 'file' | 'dir'; agentId?: string },
): Promise<ProjectFileRef> {
  const res = await post<{ data: ProjectFileRef }>(`/api/v1/sessions/${sessionId}/project-refs`, {
    dir: params.dir,
    path: params.path,
    kind: params.kind,
    agent_id: params.agentId || '',
  })
  return res.data
}
