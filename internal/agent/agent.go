package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"crow/internal/llm"
	"crow/internal/schema"
	"crow/pkg/log"
)

type ReAct interface {
	// GetTools 获取工具列表
	GetTools() []schema.Tool
	// GetToolChoice 获取工具选择方式
	GetToolChoice() schema.ToolChoice
	// ExecuteTool 执行工具
	// 注意特殊工具的使用，例如：当使用terminate工具时，需要将AgentState置为AgentStateFINISHED并返回
	ExecuteTool(context.Context, schema.ToolCall) (schema.AgentState, string)
	// Cleanup 清理资源
	Cleanup()
}

type Agent struct {
	log         *log.Logger
	name        string            // Agent的名称
	description string            // Agent的描述
	state       schema.AgentState // Agent的状态
	isAborted   int32             // 是否中止，0:未中止，1:已中止
	// Prompts
	systemPrompt   string // 系统提示信息
	nextStepPrompt string // 下一步的提示信息
	// Dependencies
	llm           llm.LLM        // LLM实例
	memory        *schema.Memory // Agent的记忆存储
	supportImages bool           // 是否支持图像
	// Execution control
	maxSteps       int           // 最大执行步骤，默认为20
	currentStep    int           // 当前执行步骤
	maxObserve     int           // 最大观测数目
	peerAskTimeout time.Duration // 每次询问模型的超时时间

	duplicateThreshold int // 重复阈值，默认为2

	reAct     ReAct
	toolCalls []schema.ToolCall // 需要被调用的工具
}

func NewAgent(agentName string, log *log.Logger, llm llm.LLM, reAct ReAct, opts ...Option) *Agent {
	agent := &Agent{
		name:  agentName,
		state: schema.AgentStateIDLE,
		llm:   llm,
		reAct: reAct,
		log:   log,
	}
	for _, fn := range opts {
		fn(agent)
	}
	if agent.maxSteps <= 0 {
		agent.maxSteps = 20
	}
	if agent.memory == nil {
		agent.memory = schema.NewMemory(100)
	}
	if agent.duplicateThreshold <= 0 {
		agent.duplicateThreshold = 2
	}
	return agent
}

func (a *Agent) Run(ctx context.Context, prompt string) error {
	if a.state != schema.AgentStateIDLE {
		a.log.Infof("Agent %s is not in IDLE state, current state: %s", a.name, a.state)
		return fmt.Errorf("agent %s is not in IDLE state, current state: %s", a.name, a.state)
	}

	atomic.StoreInt32(&a.isAborted, 0)

	a.currentStep = 0
	a.state = schema.AgentStateRUNNING
	defer func() {
		a.state = schema.AgentStateIDLE
	}()

	if strings.TrimSpace(prompt) != "" {
		_ = a.updateMemory(schema.UserMessage(prompt, ""))
	}

	var results []string
	for a.currentStep < a.maxSteps && a.state != schema.AgentStateFINISHED {
		a.currentStep++
		stepResult, err := a.step(ctx)
		if err != nil {
			return fmt.Errorf("error executing step %d: %v", a.currentStep, err)
		}

		if a.isStuck() {
			a.handleStuckState()
		}
		results = append(results, fmt.Sprintf("Step %d: %s", a.currentStep, stepResult))
	}

	if a.currentStep >= a.maxSteps {
		results = append(results, fmt.Sprintf("Terminated: Reached max steps (%d)", a.maxSteps))
	}

	if len(results) == 0 {
		return errors.New("no steps executed")
	}

	a.log.Debugf("agent step result: %v", strings.Join(results, "\n"))
	return nil
}

func (a *Agent) updateMemory(message schema.Message) error {
	if !schema.RoleSet[message.Role] {
		a.log.Errorf("unsupported message role: %v", message.Role)
		return fmt.Errorf("unsupported message role: %v", message.Role)
	}
	a.memory.AddMessage(message)
	return nil
}

// 处理React
func (a *Agent) step(ctx context.Context) (string, error) {
	shouldAct, err := a.think(ctx)
	if err != nil {
		return "", fmt.Errorf("errors during thinking: %v", err)
	}
	if !shouldAct {
		return "Thinking complete - no action needed", nil
	}
	return a.act(ctx)
}

func (a *Agent) think(ctx context.Context) (bool, error) {
	if atomic.LoadInt32(&a.isAborted) == 1 {
		return false, errors.New("agent is aborted")
	}

	if a.nextStepPrompt != "" {
		a.memory.AddMessage(schema.UserMessage(a.nextStepPrompt, ""))
	}

	message, err := a.llm.AskTool(ctx, llm.AskToolRequestParams{
		Timeout:         a.peerAskTimeout,
		ToolChoice:      a.reAct.GetToolChoice(),
		Tools:           a.reAct.GetTools(),
		SystemMessage:   schema.SystemMessage(a.systemPrompt),
		Messages:        a.memory.Messages,
		IsSupportImages: a.supportImages,
	})
	if err != nil {
		return false, err
	}
	if message == nil {
		return false, errors.New("no response received")
	}

	if a.reAct.GetToolChoice() == schema.ToolChoiceNone {
		if len(message.ToolCalls) > 0 {
			return false, fmt.Errorf("%s tried to use tools when they weren't available", a.name)
		}
		if message.Content != "" {
			a.memory.AddMessage(schema.AssistantMessage(message.Content, ""))
			return true, nil
		}
		return false, nil
	}

	// Create and add assistant message
	var assistantMsg schema.Message
	if len(message.ToolCalls) > 0 {
		a.toolCalls = message.ToolCalls
		assistantMsg = schema.FromToolCalls(a.toolCalls, message.Content, "")
	} else {
		a.toolCalls = nil
		assistantMsg = schema.AssistantMessage(message.Content, "")
	}
	a.memory.AddMessage(assistantMsg)

	if a.reAct.GetToolChoice() == schema.ToolChoiceRequired && len(a.toolCalls) == 0 {
		return true, nil // Will be handled in act()
	}
	// For 'auto' mode, continue with content if no commands but content exists
	if a.reAct.GetToolChoice() == schema.ToolChoiceAuto && len(a.toolCalls) == 0 {
		if message.Content != "" {
			return true, nil
		}
	}

	return len(a.toolCalls) > 0, nil
}

// act 执行函数调用操作，例如 function calling、MCPAgent 等
func (a *Agent) act(ctx context.Context) (string, error) {
	if atomic.LoadInt32(&a.isAborted) == 1 {
		// 如果请求中止，则不去执行tool，而是直接填补tool的执行结果为空
		for _, toolCall := range a.toolCalls {
			a.memory.AddMessage(schema.ToolMessage("", toolCall.Function.Name, toolCall.ID, ""))
		}
		a.state = schema.AgentStateFINISHED
		return "", errors.New("agent is aborted")
	}

	if len(a.toolCalls) == 0 {
		if a.reAct.GetToolChoice() == schema.ToolChoiceRequired {
			return "", errors.New("tool calls required but none provided")
		}
		if len(a.memory.Messages) != 0 && a.memory.GetRecentMessages(1)[0].Content != "" {
			return a.memory.GetRecentMessages(1)[0].Content, nil
		}
		return "No content or commands to execute", nil
	}

	var results []string
	for i, toolCall := range a.toolCalls {
		// 如果请求中止，则不去执行剩余tool，而是直接填补tool的执行结果为空
		if atomic.LoadInt32(&a.isAborted) == 1 {
			a.memory.AddMessage(schema.ToolMessage("", toolCall.Function.Name, toolCall.ID, ""))
			if i < len(a.toolCalls)-1 {
				continue
			}
			a.state = schema.AgentStateFINISHED
			return "", errors.New("agent is aborted")
		}

		state, result := a.reAct.ExecuteTool(ctx, toolCall)

		if a.maxObserve > 0 && a.maxObserve < len(result) {
			result = result[:a.maxObserve]
		}

		a.log.Debugf("Tool %s executed with result: %s", toolCall.Function.Name, result)

		// Add tool response to memory
		a.memory.AddMessage(schema.ToolMessage(result, toolCall.Function.Name, toolCall.ID, ""))
		results = append(results, result)

		if state == schema.AgentStateFINISHED {
			a.state = state
			a.log.Info("all tools are executed !")
			return "", nil
		}
	}
	return strings.Join(results, "\n\n"), nil
}

// isStuck 通过检查重复消息来判断是否陷入停滞状态
func (a *Agent) isStuck() bool {
	if len(a.memory.Messages) < a.duplicateThreshold {
		return false
	}
	lastMessage := a.memory.Messages[len(a.memory.Messages)-1]
	if lastMessage.Content == "" {
		return false
	}
	duplicateCount := 0
	for i := len(a.memory.Messages) - 2; i >= 0; i-- {
		msg := a.memory.Messages[i]
		if msg.Role == schema.RoleAssistant && msg.Content == lastMessage.Content {
			duplicateCount++
		}
	}
	return duplicateCount >= a.duplicateThreshold
}

func (a *Agent) handleStuckState() {
	stuckPrompt := "观察到重复响应，请考虑新的策略，避免重复已经尝试过的无效路径。"
	a.nextStepPrompt = fmt.Sprintf("%s\n%s", stuckPrompt, a.nextStepPrompt)
}

func (a *Agent) GetStreamReplyText() (string, bool) {
	return a.llm.GetStreamReplyText()
}

func (a *Agent) IsFinalFlag(text string) bool {
	return a.llm.IsFinalFlag(text)
}

// Abort 中止Agent的执行，等待下次请求
func (a *Agent) Abort() {
	atomic.StoreInt32(&a.isAborted, 1)
	a.currentStep = 0
}

func (a *Agent) Reset() {
	a.llm.Cleanup()
	a.reAct.Cleanup()
	a.currentStep = 0
	a.state = schema.AgentStateFINISHED
}
