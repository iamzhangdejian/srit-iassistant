import type { CustomAgentConfig } from '@/api/agent'
import type { ModelConfig } from '@/api/model'
import { allowedToolsInclude } from './legacy-tool-names'

/**
 * Only the chat model is an actual blocker.
 *
 * 快速问答（quick-answer）走的是 RAG 检索链路，检索层缺省沿用工作区默认的
 * 摘要/改写模型与 rerank 模型，不需要在智能体上单独配置；因此「对话模型」
 * 之外的条目（原 summary_model / rerank_model）不再作为未就绪原因上报，
 * 枚举里只保留 allowed_tools（工具配置为空的硬性缺失）。
 */
export type AgentNotReadyReasonKey = 'chat_model' | 'allowed_tools'

/**
 * An agent is chat-ready only when it explicitly references a usable chat
 * model. Merely having some built-in/default model in the tenant must not hide
 * incomplete agent configuration.
 *
 * For shared agents the model belongs to the source tenant, so the caller can
 * require a non-empty ID but must leave existence validation to the backend.
 *
 * 注意：内部键名历史上叫 summary_model（沿用旧命名），语义一直是「对话模型」。
 * 这里改成 chat_model 后，i18n 仍复用 agentMissingSummaryModel 那几条文案
 * （中文即「对话模型」），只是不再有 rerank 分支。
 */
export function agentHasConfiguredChatModel(
  config: Pick<CustomAgentConfig, 'model_id'> | undefined,
  models: Pick<ModelConfig, 'id' | 'type'>[],
  sourceModelIsRemote = false,
): boolean {
  const modelID = config?.model_id?.trim()
  if (!modelID) return false
  if (sourceModelIsRemote) return true
  // 仅按 id 匹配、不再强制 KnowledgeQA 类型：模型列表按类型分别拉取，
  // 走远端 provider 的对话模型可能以别的 type 落到列表里（例如未标注
  // KnowledgeQA），旧逻辑会把「已配置好模型」误判成未就绪。
  return models.some(model => model.id === modelID)
}

/**
 * Keep this aligned with backend agentRequiresRerankModel.
 *
 * Rerank is needed only when search_knowledge can actually run. Explicitly
 * disabling the knowledge-base scope makes KB tools ineffective, even if an
 * older agent configuration still contains the tool in allowed_tools.
 *
 * Stored configs may still carry the retired `knowledge_search` name (and
 * `grep_chunks`, which was folded into search_knowledge); those count too.
 */
export function agentRequiresRerankModel(
  config: Pick<CustomAgentConfig, 'kb_selection_mode' | 'allowed_tools'> | undefined,
): boolean {
  if (!config || config.kb_selection_mode === 'none') return false

  // An unset list selects the backend's DefaultAllowedTools, which include
  // search_knowledge. An empty list registers no built-in tool.
  if (config.allowed_tools == null) return true

  return allowedToolsInclude(config.allowed_tools, 'search_knowledge')
}

export function getAgentNotReadyReasonKeys(
  config: Pick<
    CustomAgentConfig,
    'model_id' | 'rerank_model_id' | 'kb_selection_mode' | 'allowed_tools' | 'agent_mode'
  > | undefined,
  models: Pick<ModelConfig, 'id' | 'type'>[],
  options: { isAgentMode: boolean; isSharedAgent: boolean },
): AgentNotReadyReasonKey[] {
  const reasons: AgentNotReadyReasonKey[] = []

  if (!agentHasConfiguredChatModel(config, models, options.isSharedAgent)) {
    reasons.push('chat_model')
  }

  // 快速问答不需要 rerank 模型（检索缺省走工作区默认），智能推理也不再阻塞：
  // 未配置 rerank 时后端直接跳过重排，属于可选优化而非缺配置。
  // 这里保留 options.isAgentMode 形参以兼容既有调用方，不再产生 rerank 原因。

  return reasons
}

/** Shared agents are owned by another tenant; receivers cannot edit their config. */
export function canLocallyConfigureAgent(sourceTenantId?: string): boolean {
  return !sourceTenantId
}

/** Map missing-config reasons to the agent editor section that fixes them. */
export function resolveAgentNotReadySection(reasons: AgentNotReadyReasonKey[]): string {
  if (reasons.includes('chat_model')) {
    return 'model'
  }
  if (reasons.includes('allowed_tools')) {
    return 'tools'
  }
  return 'model'
}

/** First missing item — used to highlight the corresponding editor field after navigation. */
export function resolveAgentNotReadyHighlight(
  reasons: AgentNotReadyReasonKey[],
): AgentNotReadyReasonKey | undefined {
  return reasons[0]
}
