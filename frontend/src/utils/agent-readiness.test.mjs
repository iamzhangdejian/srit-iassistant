import assert from 'node:assert/strict'
import test from 'node:test'
import {
  agentHasConfiguredChatModel,
  agentRequiresRerankModel,
  canLocallyConfigureAgent,
  getAgentNotReadyReasonKeys,
  resolveAgentNotReadySection,
  resolveAgentNotReadyHighlight,
} from './agent-readiness.ts'

test('does not treat an unrelated built-in chat model as agent configuration', () => {
  assert.equal(agentHasConfiguredChatModel(
    {},
    [{ id: 'builtin-chat', type: 'KnowledgeQA' }],
  ), false)
})

test('accepts the chat model explicitly configured on the agent', () => {
  assert.equal(agentHasConfiguredChatModel(
    { model_id: 'builtin-chat' },
    [{ id: 'builtin-chat', type: 'KnowledgeQA' }],
  ), true)
})

test('rejects a configured chat model that no longer exists', () => {
  assert.equal(agentHasConfiguredChatModel(
    { model_id: 'deleted-chat' },
    [{ id: 'builtin-chat', type: 'KnowledgeQA' }],
  ), false)
})

test('requires rerank when search_knowledge has a knowledge-base scope', () => {
  assert.equal(agentRequiresRerankModel({
    kb_selection_mode: 'all',
    allowed_tools: ['search_knowledge'],
  }), true)
})

test('still requires rerank for configs saved with the retired knowledge_search name', () => {
  assert.equal(agentRequiresRerankModel({
    kb_selection_mode: 'all',
    allowed_tools: ['knowledge_search'],
  }), true)
  assert.equal(agentRequiresRerankModel({
    kb_selection_mode: 'selected',
    allowed_tools: ['thinking', 'grep_chunks'],
  }), true)
})

test('does not require rerank for read-only document tools', () => {
  assert.equal(agentRequiresRerankModel({
    kb_selection_mode: 'all',
    allowed_tools: ['read_document', 'list_documents'],
  }), false)
})

test('does not require rerank when knowledge bases are disabled', () => {
  assert.equal(agentRequiresRerankModel({
    kb_selection_mode: 'none',
    allowed_tools: ['search_knowledge'],
  }), false)
  assert.equal(agentRequiresRerankModel({
    kb_selection_mode: 'none',
    allowed_tools: ['knowledge_search'],
  }), false)
})

test('does not require rerank for wiki-only tools', () => {
  assert.equal(agentRequiresRerankModel({
    kb_selection_mode: 'all',
    allowed_tools: ['wiki_search', 'wiki_read_page'],
  }), false)
})

test('matches backend default-tool behavior', () => {
  // Never set: the backend's default tools, which include search_knowledge.
  assert.equal(agentRequiresRerankModel({
    kb_selection_mode: 'all',
    allowed_tools: undefined,
  }), true)
  assert.equal(agentRequiresRerankModel({
    kb_selection_mode: 'all',
    allowed_tools: null,
  }), true)
  // Empty: no built-in tools, so nothing calls the reranker.
  assert.equal(agentRequiresRerankModel({
    kb_selection_mode: 'all',
    allowed_tools: [],
  }), false)
  assert.equal(agentRequiresRerankModel({
    kb_selection_mode: 'none',
    allowed_tools: [],
  }), false)
})

test('resolveAgentNotReadySection opens model config for model issues', () => {
  assert.equal(resolveAgentNotReadySection(['chat_model']), 'model')
  assert.equal(resolveAgentNotReadySection(['chat_model', 'allowed_tools']), 'model')
})

test('resolveAgentNotReadySection opens tools config when only tools are missing', () => {
  assert.equal(resolveAgentNotReadySection(['allowed_tools']), 'tools')
})

test('resolveAgentNotReadyHighlight returns the first missing item', () => {
  assert.equal(resolveAgentNotReadyHighlight(['chat_model', 'allowed_tools']), 'chat_model')
  assert.equal(resolveAgentNotReadyHighlight(['allowed_tools']), 'allowed_tools')
  assert.equal(resolveAgentNotReadyHighlight([]), undefined)
})

test('getAgentNotReadyReasonKeys flags missing chat model', () => {
  assert.deepEqual(getAgentNotReadyReasonKeys(
    {},
    [{ id: 'chat-1', type: 'KnowledgeQA' }],
    { isAgentMode: false, isSharedAgent: false },
  ), ['chat_model'])
})

// 快速问答/智能推理都不再因为缺少 rerank 模型被判未就绪：
// 检索缺省沿用工作区默认的 rerank，未配置时后端直接跳过重排。
test('getAgentNotReadyReasonKeys never requires a rerank model', () => {
  const models = [{ id: 'chat-1', type: 'KnowledgeQA' }, { id: 'rerank-1', type: 'Rerank' }]
  for (const tools of [['search_knowledge'], ['knowledge_search'], undefined]) {
    assert.deepEqual(getAgentNotReadyReasonKeys(
      { model_id: 'chat-1', kb_selection_mode: 'all', allowed_tools: tools },
      models,
      { isAgentMode: true, isSharedAgent: false },
    ), [])
  }
})

test('getAgentNotReadyReasonKeys does not treat current shared context as shared without sourceTenantId', () => {
  assert.deepEqual(getAgentNotReadyReasonKeys(
    { model_id: 'deleted-chat' },
    [{ id: 'chat-1', type: 'KnowledgeQA' }],
    { isAgentMode: false, isSharedAgent: false },
  ), ['chat_model'])
})

test('getAgentNotReadyReasonKeys treats an empty allowed_tools with a rerank model as ready', () => {
  assert.deepEqual(getAgentNotReadyReasonKeys(
    {
      model_id: 'chat-1',
      rerank_model_id: 'rerank-1',
      kb_selection_mode: 'all',
      allowed_tools: [],
    },
    [{ id: 'chat-1', type: 'KnowledgeQA' }, { id: 'rerank-1', type: 'Rerank' }],
    { isAgentMode: true, isSharedAgent: false },
  ), [])
})

test('canLocallyConfigureAgent is false for shared agents', () => {
  assert.equal(canLocallyConfigureAgent('42'), false)
  assert.equal(canLocallyConfigureAgent(undefined), true)
  assert.equal(canLocallyConfigureAgent(''), true)
})
