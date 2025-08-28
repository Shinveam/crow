package schema

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

var RoleSet = map[Role]bool{
	RoleSystem:    true,
	RoleUser:      true,
	RoleAssistant: true,
	RoleTool:      true,
}

type ToolChoice string

const (
	ToolChoiceNone     ToolChoice = "none"
	ToolChoiceAuto     ToolChoice = "auto"
	ToolChoiceRequired ToolChoice = "required"
)

type AgentState string

const (
	AgentStateIDLE     AgentState = "IDLE"
	AgentStateRUNNING  AgentState = "RUNNING"
	AgentStateFINISHED AgentState = "FINISHED"
	AgentStateERROR    AgentState = "ERROR"
)

type ToolFunction struct {
	Name        string
	Description string
	Parameters  map[string]any
}

// Tool 工具定义
type Tool struct {
	Type     string // 固定值为 function
	Function ToolFunction
}

type ToolCallFunction struct {
	Name      string
	Arguments string
}

// ToolCall 工具调用--模型响应后需要调用的工具信息
type ToolCall struct {
	ID       string
	Type     string
	Function ToolCallFunction
}

type Message struct {
	Role        Role
	Content     string
	ToolCalls   []ToolCall
	Name        string
	ToolCallID  string
	Base64Image string
}

func UserMessage(content, base64Image string) Message {
	return Message{Role: RoleUser, Content: content, Base64Image: base64Image}
}

func SystemMessage(content string) Message {
	return Message{Role: RoleSystem, Content: content}
}

func AssistantMessage(content, base64Image string) Message {
	return Message{Role: RoleAssistant, Content: content, Base64Image: base64Image}
}

func ToolMessage(content, name, toolCallID, base64Image string) Message {
	return Message{Role: RoleTool, Content: content, Name: name, ToolCallID: toolCallID, Base64Image: base64Image}
}

func FromToolCalls(toolCalls []ToolCall, content, base64Image string) Message {
	return Message{Role: RoleAssistant, Content: content, ToolCalls: toolCalls, Base64Image: base64Image}
}

type Memory struct {
	Messages    []Message
	MaxMessages int
}

func NewMemory(maxMessages int) *Memory {
	// 至少保留 5 条消息
	if maxMessages <= 5 {
		// 默认保留 20 条消息
		maxMessages = 20
	}
	return &Memory{
		Messages:    make([]Message, 0, maxMessages),
		MaxMessages: maxMessages,
	}
}

func (m *Memory) AddMessage(messages ...Message) {
	m.Messages = append(m.Messages, messages...)
	if len(m.Messages) <= m.MaxMessages {
		return
	}

	// 删除超过 maxMessages 的消息
	// 按对话轮次删除，对话轮次除 system 消息外，必是以 user 消息开头
	var systemMessages []Message
	isDelUserMessage := false
	for i, msg := range messages {
		switch msg.Role {
		case RoleSystem:
			systemMessages = append(systemMessages, msg)
		case RoleUser:
			if isDelUserMessage && len(systemMessages)+len(m.Messages[i:]) <= m.MaxMessages {
				m.Messages = append(systemMessages, m.Messages[i:]...)
				return
			}
			isDelUserMessage = true
		}
	}
}

func (m *Memory) Clear() {
	m.Messages = make([]Message, 0, m.MaxMessages)
}

func (m *Memory) GetRecentMessages(n int) []Message {
	if n <= 0 || len(m.Messages) == 0 {
		return []Message{}
	}
	if n > len(m.Messages) {
		return m.Messages
	}
	return m.Messages[len(m.Messages)-n:]
}
