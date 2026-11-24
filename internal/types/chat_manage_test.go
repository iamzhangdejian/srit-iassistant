package types

import "testing"

func TestQueryIntentNeedsKBRetrieval(t *testing.T) {
	tests := []struct {
		name   string
		intent QueryIntent
		want   bool
	}{
		{name: "knowledge base search", intent: IntentKBSearch, want: true},
		{name: "clarification", intent: IntentClarification, want: true},
		{name: "summarize", intent: IntentSummarize, want: false},
		{name: "web search is handled by ChatManage", intent: IntentWebSearch, want: false},
		{name: "greeting", intent: IntentGreeting, want: false},
		{name: "chitchat", intent: IntentChitchat, want: false},
		{name: "image only", intent: IntentImageOnly, want: false},
		{name: "document only", intent: IntentDocOnly, want: false},
		{name: "empty intent defaults to retrieval", intent: "", want: true},
		{name: "unknown intent", intent: QueryIntent("unknown"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.intent.NeedsKBRetrieval(); got != tt.want {
				t.Errorf("NeedsKBRetrieval() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestChatManageNeedsRetrieval(t *testing.T) {
	tests := []struct {
		name             string
		intent           QueryIntent
		webSearchEnabled bool
		kbIDs            []string
		want             bool
	}{
		{name: "knowledge base search", intent: IntentKBSearch, want: true},
		{name: "summarize", intent: IntentSummarize, want: false},
		{name: "web search enabled", intent: IntentWebSearch, webSearchEnabled: true, want: true},
		{name: "web search disabled", intent: IntentWebSearch, webSearchEnabled: false, want: false},
		// 追问：没挂知识库时保持旧的「不检索」行为；挂了知识库则必须检索，
		// 否则模型会拿着没有出处的历史自说自话（答案退化成通用知识）。
		{name: "follow-up without kb", intent: IntentFollowUp, want: false},
		{name: "follow-up with kb", intent: IntentFollowUp, kbIDs: []string{"kb-1"}, want: true},
		// 挂库也不该让闲聊去查库：省一次检索，也避免无关内容污染上下文。
		{name: "greeting with kb", intent: IntentGreeting, kbIDs: []string{"kb-1"}, want: false},
		{name: "chitchat with kb", intent: IntentChitchat, kbIDs: []string{"kb-1"}, want: false},
		{name: "summarize with kb", intent: IntentSummarize, kbIDs: []string{"kb-1"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chat := &ChatManage{
				PipelineRequest: PipelineRequest{
					WebSearchEnabled: tt.webSearchEnabled,
					KnowledgeBaseIDs: tt.kbIDs,
				},
				PipelineState: PipelineState{Intent: tt.intent},
			}
			if got := chat.NeedsRetrieval(); got != tt.want {
				t.Errorf("NeedsRetrieval() = %t, want %t", got, tt.want)
			}
		})
	}
}
