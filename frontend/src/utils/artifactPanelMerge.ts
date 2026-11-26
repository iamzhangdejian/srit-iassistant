/**
 * Row of the chat artifacts panel. The list mixes two sources: the
 * cross-session artifact library (what every conversation in this space
 * produced) and the current session's live message list (files written in this
 * very turn, which may not be in the library response yet).
 */
export interface PanelArtifactRowLike {
  index: number
  messageId: string
  sessionId: string
  file_name: string
  created_at: string
}

function rowKey(row: PanelArtifactRowLike): string {
  return `${row.sessionId}:${row.messageId}:${row.index}`
}

/**
 * Merges the library page with this session's live artifacts.
 *
 * `live` wins on a key collision: it is the fresher view of a file the current
 * turn just wrote, and the library row for it can still be the previous
 * version (or missing entirely while the collect is in flight).
 *
 * Rows are returned newest first, which is how both sources present files.
 */
export function mergeArtifactPanelRows<T extends PanelArtifactRowLike>(
  library: T[],
  live: Omit<T, 'sessionId'>[],
  sessionId: string,
): T[] {
  const byKey = new Map<string, T>()
  for (const row of library) byKey.set(rowKey(row), row)
  for (const item of live) {
    const row = { ...item, sessionId } as T
    byKey.set(rowKey(row), row)
  }
  return [...byKey.values()].sort((a, b) => b.created_at.localeCompare(a.created_at))
}
