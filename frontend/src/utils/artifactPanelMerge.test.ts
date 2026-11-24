import assert from 'node:assert/strict'
import test from 'node:test'
import { mergeArtifactPanelRows } from './artifactPanelMerge.ts'

const base = {
  file_type: '',
  file_size: 1,
  source_path: '',
  mod_time: '',
  created_at: '2026-11-19T10:00:00Z',
  index: 0,
  messageId: 'm1',
}

test('mergeArtifactPanelRows keeps library rows from other sessions', () => {
  const merged = mergeArtifactPanelRows(
    [
      { ...base, file_name: 'out.docx', sessionId: 'other', created_at: '2026-11-19T09:00:00Z' },
    ],
    [],
    'current',
  )
  assert.equal(merged.length, 1)
  assert.equal(merged[0].sessionId, 'other')
})

test('mergeArtifactPanelRows lets this turn win over a stale library row', () => {
  const merged = mergeArtifactPanelRows(
    [
      {
        ...base,
        file_name: 'out.docx',
        sessionId: 'current',
        created_at: '2026-11-19T09:00:00Z',
      },
    ],
    [{ ...base, file_name: 'out.docx' }],
    'current',
  )
  assert.equal(merged.length, 1, 'same session+message+index must collapse to one row')
  assert.equal(merged[0].sessionId, 'current')
})

test('mergeArtifactPanelRows orders newest first', () => {
  const merged = mergeArtifactPanelRows(
    [
      { ...base, index: 1, file_name: 'old.docx', sessionId: 's', created_at: '2026-11-18T09:00:00Z' },
      { ...base, index: 0, file_name: 'new.docx', sessionId: 's', created_at: '2026-11-19T09:00:00Z' },
    ],
    [],
    's',
  )
  assert.deepEqual(
    merged.map((row) => row.file_name),
    ['new.docx', 'old.docx'],
  )
})
