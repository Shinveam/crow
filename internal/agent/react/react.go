package react

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"crow/internal/agent"
	"crow/internal/agent/llm"
	"crow/internal/agent/memory"
	"crow/internal/agent/schema"
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

type ReActAgent struct {
	log      *log.Logger
	listener agent.Listener

	name        string // Agent的名称
	description string // Agent的描述
	// Prompts
	systemPrompt   string // 系统提示信息
	nextStepPrompt string // 下一步的提示信息
	// Dependencies
	reAct     ReAct             // ReAct 操作对象
	llm       llm.LLM           // LLM实例
	memory    memory.Memory     // Agent的记忆存储
	toolCalls []schema.ToolCall // 需要被调用的工具
	// Execution control
	supportImages      bool              // 是否支持图像
	maxSteps           int               // 最大执行步骤，默认为20
	currentStep        int               // 当前执行步骤
	maxObserve         int               // 最大观测数目
	peerAskTimeout     time.Duration     // 每次询问模型的超时时间
	duplicateThreshold int               // 重复阈值，默认为2
	state              schema.AgentState // Agent的状态

	lock      sync.Mutex
	interrupt int32 // 是否被打断，0：未打断，1：已打断
	connectId string
}

func NewReActAgent(agentName string, log *log.Logger, llm llm.LLM, reAct ReAct, opts ...Option) *ReActAgent {
	react := &ReActAgent{
		name:  agentName,
		state: schema.AgentStateIDLE,
		llm:   llm,
		reAct: reAct,
		log:   log,
	}
	for _, fn := range opts {
		fn(react)
	}
	if react.maxSteps <= 0 {
		react.maxSteps = 20
	}
	if react.memory == nil {
		react.memory = memory.NewDefaultMemory(20)
	}
	if react.duplicateThreshold <= 0 {
		react.duplicateThreshold = 2
	}
	return react
}

func (r *ReActAgent) SetConfig(cfg any) {
	return
}

func (r *ReActAgent) SetListener(listener agent.Listener) {
	r.listener = listener
}

func (r *ReActAgent) Run(ctx context.Context, userPrompt string) error {
	if userPrompt == "" {
		return errors.New("user prompt is empty")
	}

	r.lock.Lock()
	defer r.lock.Unlock()

	r.currentStep = 0
	r.state = schema.AgentStateRUNNING
	defer func() {
		// 如果不是被打断的，说明是正常结束的，则需要不乏一个结束标识
		if atomic.LoadInt32(&r.interrupt) == 0 {
			// agent处理结束后发送一个结束标识
			r.listener.OnAgentResult(ctx, "", agent.StateCompleted)
		}
		r.state = schema.AgentStateIDLE
		atomic.StoreInt32(&r.interrupt, 0)
		r.reAct.Cleanup()
	}()

	r.memory.FormatMessages()
	r.memory.AddMessage(schema.UserMessage(userPrompt, ""))

	var results []string
	for r.currentStep < r.maxSteps && r.state != schema.AgentStateFINISHED && atomic.LoadInt32(&r.interrupt) != 1 {
		r.currentStep++
		stepResult, err := r.step(ctx)
		if err != nil {
			return fmt.Errorf("error executing step %d: %v", r.currentStep, err)
		}

		if r.isStuck() {
			r.handleStuckState()
		}
		results = append(results, fmt.Sprintf("step %d: %s", r.currentStep, stepResult))
	}

	if r.currentStep >= r.maxSteps {
		results = append(results, fmt.Sprintf("terminated: Reached max steps (%d)", r.maxSteps))
	}

	if len(results) == 0 {
		return errors.New("no steps executed")
	}
	r.log.Debugf("agent step result: %v", strings.Join(results, "\n"))
	return nil
}

func (r *ReActAgent) Reset() error {
	return nil
}

func (r *ReActAgent) step(ctx context.Context) (string, error) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		r.recvLLMMessages(ctx)
	}()

	shouldAct, err := r.think(ctx)
	if err != nil {
		wg.Wait()
		return "", fmt.Errorf("errors during thinking: %v", err)
	}
	wg.Wait()

	if !shouldAct {
		r.state = schema.AgentStateFINISHED
		return "thinking complete - no action needed", nil
	}
	return r.act(ctx)
}

func (r *ReActAgent) think(ctx context.Context) (bool, error) {
	if r.nextStepPrompt != "" {
		r.memory.AddMessage(schema.UserMessage(r.nextStepPrompt, ""))
	}

	message, err := r.llm.Handle(ctx, &llm.Request{
		Timeout:         r.peerAskTimeout,
		ToolChoice:      r.reAct.GetToolChoice(),
		Tools:           r.reAct.GetTools(),
		SystemMessage:   schema.SystemMessage(r.systemPrompt),
		Messages:        r.memory.GetAllMessages(),
		IsSupportImages: r.supportImages,
	})
	if err != nil {
		return false, fmt.Errorf("llm handle error: %w", err)
	}
	if message == nil {
		return false, errors.New("no response received")
	}

	if r.reAct.GetToolChoice() == schema.ToolChoiceNone {
		if len(message.ToolCalls) > 0 {
			return false, fmt.Errorf("%s tried to use tools when they weren't available", r.name)
		}
		if message.Content != "" {
			r.memory.AddMessage(schema.AssistantMessage(message.Content, ""))
			return true, nil
		}
		return false, nil
	}

	// Create and add assistant message
	var assistantMsg schema.Message
	if len(message.ToolCalls) > 0 {
		r.toolCalls = message.ToolCalls
		assistantMsg = schema.FromToolCalls(r.toolCalls, message.Content, "")
	} else {
		r.toolCalls = nil
		assistantMsg = schema.AssistantMessage(message.Content, "")
	}
	r.memory.AddMessage(assistantMsg)

	if r.reAct.GetToolChoice() == schema.ToolChoiceRequired && len(r.toolCalls) == 0 {
		return true, nil // Will be handled in act()
	}
	// For 'auto' mode, continue with content if no commands but content exists
	if r.reAct.GetToolChoice() == schema.ToolChoiceAuto && len(r.toolCalls) == 0 {
		if message.Content != "" {
			return true, nil
		}
	}

	return len(r.toolCalls) > 0, nil
}

// act 执行函数调用操作，例如 function calling、MCPAgent 等
func (r *ReActAgent) act(ctx context.Context) (string, error) {
	if len(r.toolCalls) == 0 {
		if r.reAct.GetToolChoice() == schema.ToolChoiceRequired {
			return "", errors.New("tool calls required but none provided")
		}
		if len(r.memory.GetAllMessages()) != 0 && r.memory.GetRecentMessages(1)[0].Content != "" {
			return r.memory.GetRecentMessages(1)[0].Content, nil
		}
		return "No content or commands to execute", nil
	}

	var results []string
	for _, toolCall := range r.toolCalls {
		state, result := r.reAct.ExecuteTool(ctx, toolCall)

		if r.maxObserve > 0 && r.maxObserve < len(result) {
			result = result[:r.maxObserve]
		}

		r.log.Debugf("tool %s executed with result: %s", toolCall.Function.Name, result)

		// Add tool response to memory
		r.memory.AddMessage(schema.ToolMessage(result, toolCall.Function.Name, toolCall.ID, ""))
		results = append(results, result)

		if state == schema.AgentStateFINISHED {
			r.state = state
			r.log.Info("all tools are executed !")
			return "", nil
		}
	}
	return strings.Join(results, "\n\n"), nil
}

// isStuck 通过检查重复消息来判断是否陷入停滞状态
func (r *ReActAgent) isStuck() bool {
	if len(r.memory.GetAllMessages()) < r.duplicateThreshold {
		return false
	}
	lastMessage := r.memory.GetRecentMessages(1)
	if len(lastMessage) != 0 && lastMessage[0].Content == "" {
		return false
	}
	duplicateCount := 0
	for i := len(r.memory.GetAllMessages()) - 2; i >= 0; i-- {
		msg := r.memory.GetAllMessages()[i]
		if msg.Role == schema.RoleAssistant && len(lastMessage) != 0 && msg.Content == lastMessage[0].Content {
			duplicateCount++
		}
	}
	return duplicateCount >= r.duplicateThreshold
}

func (r *ReActAgent) handleStuckState() {
	stuckPrompt := "观察到重复响应，请考虑新的策略，避免重复已经尝试过的无效路径。"
	r.nextStepPrompt = fmt.Sprintf("%s\n%s", stuckPrompt, r.nextStepPrompt)
}

func (r *ReActAgent) recvLLMMessages(ctx context.Context) {
	for {
		reply, err := r.llm.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			r.log.Errorf("recv llm message error: %v", err)
			return
		}

		if finish := r.listener.OnAgentResult(ctx, reply, agent.StateProcessing); finish {
			atomic.StoreInt32(&r.interrupt, 1)
			return
		}
	}
}
