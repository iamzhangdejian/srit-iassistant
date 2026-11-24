// Package config loads WeKnora runtime configuration.
package config

import (
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// ResolveCustomAgentPrompts resolves inherited references at request time. Explicit
// text always wins, including legacy saved prompts; never rewrite user content.
// References are scoped to their field/mode so a context template cannot become
// system instructions just because it has a matching ID.
func (c *Config) ResolveCustomAgentPrompts(agent *types.CustomAgent) (system, context string) {
	if agent == nil {
		return "", ""
	}
	system, context = agent.Config.SystemPrompt, agent.Config.ContextTemplate
	if c == nil || c.PromptTemplates == nil {
		return
	}
	resolve := func(body, id string, templates []PromptTemplate) string {
		if body != "" || id == "" {
			return body
		}
		for _, template := range templates {
			if template.ID == id {
				return template.Content
			}
		}
		return ""
	}
	list := c.PromptTemplates.SystemPrompt
	if agent.IsAgentMode() {
		list = c.PromptTemplates.AgentSystemPrompt
	}
	system = resolve(system, agent.Config.SystemPromptID, list)
	context = resolve(context, agent.Config.ContextTemplateID, c.PromptTemplates.ContextTemplate)
	return
}

// PureChatTemplateID is the prompt_templates/system_prompt.yaml entry used when a
// turn performs no knowledge retrieval (pure conversation).
const PureChatTemplateID = "pure_chat"

// defaultPureChatPrompt is the built-in fallback used when the YAML template is
// missing or empty, so a broken config never leaves the model without a system
// prompt on the pure-chat path.
const defaultPureChatPrompt = `You are 国研智研体, an intelligent conversational assistant.
Answer general questions directly and helpfully. No knowledge base is being searched for this turn, so do not claim to be checking one and do not tell the user that materials are missing.
Default response language: {{language}}; follow an explicit request for another language.`

// ResolvePureChatPrompt returns the system prompt for a turn that performs no
// knowledge retrieval. Agents are usually configured with a knowledge-base
// prompt ("answer only from the supplied materials, say what is missing
// otherwise"); applying that to free conversation makes the model refuse
// ordinary questions such as the current date or offline general knowledge.
// Resolution order: the pure_chat template in prompt_templates, then a built-in
// fallback.
func (c *Config) ResolvePureChatPrompt() string {
	if c != nil && c.PromptTemplates != nil {
		for _, template := range c.PromptTemplates.SystemPrompt {
			if template.ID == PureChatTemplateID && strings.TrimSpace(template.Content) != "" {
				return template.Content
			}
		}
	}
	return defaultPureChatPrompt
}
