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
	return &Memory{MaxMessages: maxMessages}
}

func (m *Memory) AddMessage(msgs ...Message) {
	m.Messages = append(m.Messages, msgs...)
	if len(m.Messages) > m.MaxMessages {
		m.Messages = m.Messages[len(m.Messages)-m.MaxMessages:]
	}
	// 去除头部的 tool 消息，避免 llm 调用出错
	for len(m.Messages) > 0 && m.Messages[0].Role == RoleTool {
		m.Messages = m.Messages[1:]
	}
}

func (m *Memory) Clear() {
	m.Messages = []Message{}
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
