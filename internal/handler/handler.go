package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"

	"crow/internal/agent"
	"crow/internal/agent/llm/openai"
	"crow/internal/agent/prompt"
	"crow/internal/agent/react"
	"crow/internal/asr"
	doubaoasr "crow/internal/asr/doubao"
	"crow/internal/asr/paraformer"
	"crow/internal/config"
	"crow/internal/tts"
	cosyvoice "crow/internal/tts/cosy-voice"
	doubaotts "crow/internal/tts/doubao"
	"crow/pkg/log"
	"crow/pkg/util"
)

type Handler struct {
	cfg *config.Config
	log *log.Logger

	conn Connection
	once sync.Once // 用于确保只执行一次关闭操作

	sessionID string
	enableAsr bool
	enableTts bool

	asrProvider   asr.Provider
	agentProvider agent.Provider
	ttsProvider   tts.Provider

	chatRound      int   // chatRound 对话轮次
	closeAfterChat bool  // closeAfterChat 是否对话结束后关闭连接
	stopRecv       int32 // stopRecv 停止接收客户端消息，0：不停止，1：停止
	interrupt      int32 // interrupt 中断对话，0：不中断，1：中断

	stopChan         chan struct{}
	clientTextQueue  chan string
	clientAudioQueue chan []byte
}

func NewHandler(cfg *config.Config, log *log.Logger, conn Connection) *Handler {
	handler := &Handler{
		cfg:       cfg,
		log:       log,
		conn:      conn,
		sessionID: uuid.New().String(),
		stopChan:  make(chan struct{}),
	}
	switch cfg.SelectedModule["asr"] {
	case "paraformer":
		handler.asrProvider = paraformer.NewParaformer(log)
	case "doubao":
		handler.asrProvider = doubaoasr.NewDoubao(log)
	}
	if handler.asrProvider != nil {
		handler.asrProvider.SetListener(handler)
	}

	switch cfg.SelectedModule["tts"] {
	case "cosy_voice":
		handler.ttsProvider = cosyvoice.NewCosyVoice(log)
	case "doubao":
		handler.ttsProvider = doubaotts.NewDoubao(log)
	case "doubao_stream":
		handler.ttsProvider = doubaotts.NewDoubaoStream(log)
	}
	if handler.ttsProvider != nil {
		handler.ttsProvider.SetListener(handler)
	}
	return handler
}

func (h *Handler) initAgent(ctx context.Context) error {
	var llmCfg config.LLMConfig
	if v, ok := h.cfg.SelectedModule["llm"]; ok {
		if _, ok = h.cfg.LLM[v]; ok {
			llmCfg = h.cfg.LLM[v]
		}
	}
	llm := openai.NewOpenAI(llmCfg.Model, llmCfg.APIKey, llmCfg.BaseURL)
	mcpReAct, err := react.NewMCPAgent(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to create mcp agent: %v", err)
	}

	type toolInfo struct {
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
		Properties  any    `json:"properties,omitempty"`
	}

	toolPrompt := ""
	toolDesc := "<tool>\n%s\n</tool>\n\n"
	for _, tool := range mcpReAct.GetTools() {
		info := toolInfo{
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
			Properties:  tool.Function.Parameters["properties"],
		}
		jsonData, _ := json.Marshal(&info)
		toolPrompt += fmt.Sprintf(toolDesc, string(jsonData))
	}

	h.agentProvider = react.NewReActAgent("crow", h.log, llm, mcpReAct,
		react.WithSystemPrompt(fmt.Sprintf(prompt.SystemPrompt, toolPrompt)),
		react.WithNextStepPrompt(prompt.NextStepPrompt),
		react.WithMaxObserve(500),
		react.WithMemoryMaxMessages(20))
	h.agentProvider.SetListener(h)
	return nil
}

func (h *Handler) Handle(ctx context.Context) {
	// 接收并处理hello消息
	if err := h.handleHelloMessage(ctx); err != nil {
		h.log.Errorf("failed to handle hello message: %v", err)
		return
	}

	// 初始化agent
	if err := h.initAgent(context.Background()); err != nil {
		h.log.Errorf("failed to init agent: %v", err)
		return
	}

	// 开始接收客户端消息
	h.listenClientMessages(ctx)
}

func (h *Handler) listenClientMessages(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-h.stopChan:
			return
		default:
			messageType, message, err := h.conn.ReadMessage()
			if err != nil {
				h.log.Errorf("failed to read message: %v", err)
				return
			}
			if err = h.handleMessage(messageType, message); err != nil {
				h.log.Errorf("failed to handle message: %v", err)
			}
		}
	}
}

func (h *Handler) listenClientAudioMessages(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-h.stopChan:
			return
		case audio := <-h.clientAudioQueue:
			if atomic.LoadInt32(&h.stopRecv) == 1 {
				continue
			}
			if err := h.asrProvider.SendAudio(ctx, audio); err != nil {
				h.log.Errorf("failed to send audio data: %v", err)
			}
		}
	}
}

func (h *Handler) listenClientTextMessages(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-h.stopChan:
			return
		case text := <-h.clientTextQueue:
			if atomic.LoadInt32(&h.stopRecv) == 1 {
				continue
			}
			h.log.Infof("received text data: %v", text)
			if err := h.handleClientTextMessages(ctx, text); err != nil {
				h.log.Errorf("failed to process client text message: %v", err)
			}
		}
	}
}

func (h *Handler) OnAsrResult(ctx context.Context, result string, state asr.State) bool {
	var isSystemMsg bool
	if h.asrProvider.GetSilenceCount() >= 2 {
		h.log.Infof("连续检测到两次静音，结束对话")
		h.closeAfterChat = true
		atomic.StoreInt32(&h.stopRecv, 1)
		state = asr.StateCompleted
		result = "长时间未检测到用户说话，请礼貌的结束对话"
		isSystemMsg = true
	}
	if result == "" && state == asr.StateProcessing {
		return false
	}

	// 非系统消息则向客户端发送ASR结果
	if !isSystemMsg {
		if err := h.sendAsrMessage(result, int(state)); err != nil {
			return true
		}
	}

	switch state {
	case asr.StateSentenceEnd:
		if err := h.handleChatMessage(ctx, result); err != nil {
			h.log.Errorf("failed to handle chat message: %v", err)
		}
		return false
	case asr.StateCompleted:
		_ = h.asrProvider.Reset() // 重置ASR，准备下一次识别
		if err := h.handleChatMessage(ctx, result); err != nil {
			h.log.Errorf("failed to handle chat message: %v", err)
		}
		return true
	default:
		// 如果有新的语音识别结果，则应该打断当前的对话
		if atomic.LoadInt32(&h.interrupt) == 0 {
			_ = h.handleAbortChat()
		}
	}
	return false
}

func (h *Handler) OnAgentResult(ctx context.Context, text string, state agent.State) bool {
	if text == "" && state != agent.StateCompleted {
		return false
	}
	// 向客户端发送回复消息
	if err := h.sendChatMessage(text); err != nil {
		h.log.Errorf("failed to send chat message: %v", err)
		return true
	}

	// 向TTS服务发送文本
	if h.ttsProvider != nil {
		if err := h.ttsProvider.ToTTS(ctx, text); err != nil {
			h.log.Errorf("failed to convert text to tts: %v", err)
			return false
		}
	}

	if state == agent.StateCompleted {
		_ = h.agentProvider.Reset()
		return true
	}
	return false
}

func (h *Handler) OnTtsResult(data []byte, state tts.State) bool {
	// 检测到中断信号，不再下发tts数据
	if atomic.LoadInt32(&h.interrupt) == 1 {
		return false
	}

	if len(data) == 0 && state != tts.StateCompleted {
		return false
	}
	if err := h.sendTtsMessage(string(data), int(state)); err != nil {
		h.log.Errorf("failed to send tts message: %v", err)
	}
	if state == tts.StateCompleted {
		_ = h.ttsProvider.Reset()
		return true
	}
	return false
}

func (h *Handler) isExit(text string) bool {
	if len(h.cfg.CMDExit) == 0 {
		return false
	}
	// 移除标点符号
	text = util.RemoveAllPunctuation(text)
	for _, cmd := range h.cfg.CMDExit {
		if text == cmd {
			return true
		}
	}
	return false
}

func (h *Handler) close() {
	h.once.Do(func() {
		_ = h.conn.Close()
		close(h.stopChan)

		if h.asrProvider != nil {
			if err := h.asrProvider.Reset(); err != nil {
				h.log.Errorf("failed to reset asr provider: %v", err)
			}
		}
		if h.agentProvider != nil {
			if err := h.agentProvider.Reset(); err != nil {
				h.log.Errorf("failed to reset agent provider: %v", err)
			}
		}
		if h.ttsProvider != nil {
			if err := h.ttsProvider.Reset(); err != nil {
				h.log.Errorf("failed to reset tts provider: %v", err)
			}
		}
	})
}
