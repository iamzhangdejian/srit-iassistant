import { post } from '@/utils/request'

/**
 * 旧版 Office（Word 97-2003 / PowerPoint 97-2003）转换预览。
 *
 * 这两种格式是 OLE2 二进制，浏览器端的 docx-preview / pptx 渲染器只认
 * OOXML（ZIP+XML），所以由后端 anydoc 转成 Markdown 再渲染。接口只在这里
 * 被调用：`DocumentPreview` 的 `legacy` 分支。
 */
export interface LegacyConvertResult {
  markdown: string
  /** anydoc 识别出的格式名（doc / ppt / xls …）。 */
  format: string
  engine: string
}

/**
 * 把旧版 Office 文档转成 Markdown。
 *
 * 后端限制 64MB、只接受 .doc/.dot/.ppt/.pot/.xls；失败时抛出带后端 message
 * 的错误（415 不支持 / 413 太大 / 501 未内置转换器 / 422 转换失败），
 * 预览面板据此显示原因并引导下载。
 */
export async function convertLegacyDocument(file: Blob, fileName: string): Promise<LegacyConvertResult> {
  const form = new FormData()
  form.append('file', file, fileName || 'document')
  const res = await post<{ data: LegacyConvertResult }>('/api/v1/documents/convert', form)
  const data = (res as { data?: LegacyConvertResult })?.data
  if (!data || typeof data.markdown !== 'string') {
    throw new Error('转换结果为空')
  }
  return data
}