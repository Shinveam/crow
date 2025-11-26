package memory

import "crow/internal/agent/schema"

type Memory interface {
	// FormatMessages 格式化消息
	// 在对话启动前，先调整历史消息格式，避免请求失败
	FormatMessages()
	// AddMessage 添加消息
	AddMessage(messages ...schema.Message)
	// GetAllMessages 获取所有消息
	GetAllMessages() []schema.Message
	// GetRecentMessages 获取最近的消息
	GetRecentMessages(n int) []schema.Message
	// Clear 清空消息
	Clear()
}

type DefaultMemory struct {
	messages    []schema.Message
	maxMessages int
}

func NewDefaultMemory(maxMessages int) *DefaultMemory {
	// 至少保留 5 条消息
	if maxMessages <= 5 {
		// 默认保留 20 条消息
		maxMessages = 20
	}
	return &DefaultMemory{
		messages:    make([]schema.Message, 0, maxMessages),
		maxMessages: maxMessages,
	}
}

func (m *DefaultMemory) FormatMessages() {
	if len(m.messages) == 0 {
		return
	}
	switch m.messages[len(m.messages)-1].Role {
	case schema.RoleAssistant:
		// 如果最后一条消息是 assistant 消息，且内容为空或包含工具调用，则不应该保留，否则调用模型会失败，影响模型上下文判断
		if m.messages[len(m.messages)-1].Content == "" || len(m.messages[len(m.messages)-1].ToolCalls) > 0 {
			// 移除最后一条 assistant 消息
			m.messages = m.messages[:len(m.messages)-1]
		}
	case schema.RoleTool:
		// 如果最后一条消息是 tool 消息，说明请求可能存在部分工具未被成功调用的情况，
		// 因此需要追溯到最近的 assistant 消息，判断存在多少个需要被调用的工具，
		// 如果 assistant 消息的工具调用数量与 tool 消息的工具调用数量不一致，则需要补充未被调用的 tool 信息
		toolMessages := make(map[string]struct{})
		var assistantMessage schema.Message
		for i := len(m.messages) - 1; i >= 0; i-- {
			if m.messages[i].Role == schema.RoleTool {
				toolMessages[m.messages[i].ToolCallID] = struct{}{}
			}
			if m.messages[i].Role == schema.RoleAssistant {
				assistantMessage = m.messages[i]
				break
			}
		}
		if len(assistantMessage.ToolCalls) != len(toolMessages) {
			// 补充未被调用的 tool 信息
			for _, toolCall := range assistantMessage.ToolCalls {
				if _, ok := toolMessages[toolCall.ID]; !ok {
					toolMsg := schema.ToolMessage("error: tool execution was interrupted", toolCall.Function.Name, toolCall.ID, "")
					m.messages = append(m.messages, toolMsg)
				}
			}
		}
	}
}

func (m *DefaultMemory) AddMessage(messages ...schema.Message) {
	m.messages = append(m.messages, messages...)
	if len(m.messages) <= m.maxMessages {
		return
	}

	// 删除超过 maxMessages 的消息
	// 按对话轮次删除，对话轮次除 system 消息外，必是以 user 消息开头
	systemMessage := make([]schema.Message, 0, 1)
	isDelUserMessage := false
	for i, v := range m.messages {
		switch v.Role {
		case schema.RoleSystem:
			systemMessage = append(systemMessage, v)
		case schema.RoleUser:
			if isDelUserMessage && len(systemMessage)+len(m.messages[i:]) <= m.maxMessages {
				m.messages = append(systemMessage, m.messages[i:]...)
				return
			}
			isDelUserMessage = true
		}
	}
}

func (m *DefaultMemory) GetAllMessages() []schema.Message {
	return m.messages
}

func (m *DefaultMemory) GetRecentMessages(n int) []schema.Message {
	if n <= 0 || len(m.messages) == 0 {
		return nil
	}
	if n > len(m.messages) {
		return m.messages
	}
	return m.messages[len(m.messages)-n:]
}

func (m *DefaultMemory) Clear() {
	m.messages = make([]schema.Message, 0, m.maxMessages)
}
