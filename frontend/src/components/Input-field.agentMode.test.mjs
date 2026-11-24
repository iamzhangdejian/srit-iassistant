import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

// 桌面版固定使用 builtin-main-assistant，输入框里不再有智能体/模式下拉。
// 原先这个文件测的是「选择智能体后如何切换 isAgentStreamMode」以及列表刷新
// 后如何纠正模式——那套机制随着下拉一起删除了。这里改为守住替换后的约束，
// 避免以后有人把模式开关悄悄加回去。

const source = readFileSync(new URL('./Input-field.vue', import.meta.url), 'utf8')
const store = readFileSync(new URL('../stores/settings.ts', import.meta.url), 'utf8')
const chatView = readFileSync(new URL('../views/chat/index.vue', import.meta.url), 'utf8')

test('the composer has no agent or mode picker', () => {
  assert.doesNotMatch(source, /AgentSelector/)
  assert.doesNotMatch(source, /agent-mode-btn/)
  assert.doesNotMatch(source, /syncSelectedAgentModeFromConfig/)
  assert.doesNotMatch(source, /ensureSelectedAgentNotDisabled/)
})

test('the settings store always reports the agent pipeline as active', () => {
  // 恒为 true：知识库锁定、技能区、联网搜索都靠这个判断，不能因为
  // localStorage 里残留的旧 selectedAgentId 而退回只读 RAG 模式。
  assert.match(store, /isAgentStreamMode: \(\) => true/)
  assert.match(store, /isQuickAnswerMode: \(\) => false/)
})

test('chat requests pin the builtin main assistant', () => {
  assert.match(chatView, /BUILTIN_MAIN_ASSISTANT_ID/)
  assert.match(chatView, /const agentEnabled = true/)
  // 不能再用历史选择决定走哪条管线
  assert.doesNotMatch(chatView, /useSettingsStoreInstance\.isAgentStreamMode/)
  assert.doesNotMatch(chatView, /useSettingsStoreInstance\.selectedAgentId \|\|/)
})

test('the default selected agent is the main assistant', () => {
  assert.match(store, /selectedAgentId: BUILTIN_MAIN_ASSISTANT_ID/)
})
