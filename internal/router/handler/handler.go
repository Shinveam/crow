package handler

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"

	"crow/internal/agent"
	"crow/internal/asr"
	"crow/internal/asr/paraformer"
	"crow/internal/config"
	"crow/internal/llm/openai"
	"crow/internal/prompt"
	"crow/internal/tts"
	cosyvoice "crow/internal/tts/cosy-voice"
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

	asrProvider asr.Provider
	agent       *agent.Agent
	ttsProvider tts.Provider

	chatRound      int   // 对话轮次
	closeAfterChat bool  // 是否对话结束后关闭连接
	serverStopRecv int32 // 1表示true服务端停止不再接收数据
	serverStopSend int32 // 1表示true服务端停止不再下发数据

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
	handler.asrProvider = paraformer.NewParaformer(handler, log)
	handler.ttsProvider = cosyvoice.NewCosyVoice(handler, log)
	return handler
}

func (h *Handler) initAgent(ctx context.Context) error {
	var llmCfg config.LLMConfig
	if v, ok := h.cfg.SelectedModule["llm"]; ok {
		if _, ok = h.cfg.LLM[v]; ok {
			llmCfg = h.cfg.LLM[v]
		}
	}
	llm := openai.NewLLM(llmCfg.Model, llmCfg.APIKey, llmCfg.BaseURL, true)
	mcpReAct, err := agent.NewMCPAgent(ctx)
	if err != nil {
		return fmt.Errorf("failed to create mcp agent: %v", err)
	}
	morePrompt := ""
	for _, tool := range mcpReAct.GetTools() {
		morePrompt += fmt.Sprintf("#### %s\n", tool.Function.Name)
		if tool.Function.Description != "" {
			morePrompt += fmt.Sprintf("* 描述: %s\n", tool.Function.Description)
		}
		if properties, ok := tool.Function.Parameters["properties"].(map[string]any); ok {
			var params string
			for k, v := range properties {
				if v, ok := v.(map[string]any); ok && v["description"] != nil {
					params += fmt.Sprintf("    - %s: %s\n", k, v["description"])
					continue
				} else {
					params += fmt.Sprintf("    - %s\n", k)
				}
			}
			if params != "" {
				morePrompt += fmt.Sprintf("* 参数:\n%s\n", params)
			}
		}
	}
	h.agent = agent.NewAgent("crow", h.log, llm, mcpReAct,
		agent.WithSystemPrompt(prompt.SystemPrompt+morePrompt),
		agent.WithNextStepPrompt(prompt.NextStepPrompt),
		agent.WithMaxObserve(500),
		agent.WithMemoryMaxMessages(50),
	)
	return nil
}

func (h *Handler) Handle(ctx context.Context) {
	defer func() {
		_ = h.conn.Close()
	}()

	// 接收并处理hello消息
	if err := h.handleHelloMessage(ctx); err != nil {
		h.log.Errorf("failed to handle hello message: %v", err)
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
			if atomic.LoadInt32(&h.serverStopRecv) == 1 {
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
			h.log.Infof("received text data: %v", text)
			if err := h.handleClientTextMessages(ctx, text); err != nil {
				h.log.Errorf("failed to process client text message: %v", err)
			}
		}
	}
}

func (h *Handler) OnAsrResult(result string, state asr.State) bool {
	if h.asrProvider.GetSilenceCount() >= 2 {
		h.log.Infof("连续检测到两次静音，结束对话")
		h.closeAfterChat = true
		state = asr.StateCompleted
		result = "长时间未检测到用户说话，请礼貌的结束对话"
	}
	if result == "" {
		return false
	}

	// 允许服务端下发数据
	atomic.StoreInt32(&h.serverStopSend, 0)

	_ = h.sendAsrMessage(result, int(state))
	switch state {
	case asr.StateSentenceEnd:
		atomic.StoreInt32(&h.serverStopRecv, 1)
		_ = h.handleChatMessage(context.Background(), result)
		return false
	case asr.StateCompleted:
		atomic.StoreInt32(&h.serverStopRecv, 1)
		_ = h.asrProvider.Reset() // 重置ASR，准备下一次识别
		_ = h.handleChatMessage(context.Background(), result)
		return true
	}
	return false
}

func (h *Handler) OnAgentResult(ctx context.Context) {
	for {
		text, ok := h.agent.GetStreamReplyText()
		if !ok {
			break
		}
		// 服务停止下发后直接跳出循环
		if atomic.LoadInt32(&h.serverStopSend) == 1 {
			continue
		}
		if h.agent.IsFinalFlag(text) {
			if h.enableTts {
				_ = h.ttsProvider.ToSessionFinish()
			}
			continue
		}
		if err := h.sendChatMessage(text); err != nil {
			h.log.Errorf("failed to send chat message: %v", err)
			continue
		}

		if !h.enableTts {
			continue
		}
		// 再次判断服务端语音是否停止，否则不再合成
		if atomic.LoadInt32(&h.serverStopSend) == 1 {
			continue
		}
		// 去合成语音
		if err := h.ttsProvider.ToTTS(ctx, text); err != nil {
			h.log.Errorf("failed to send tts message: %v", err)
		}
	}
}

func (h *Handler) OnTtsResult(data []byte, state tts.State) bool {
	if atomic.LoadInt32(&h.serverStopSend) == 1 {
		// 停止下发数据后，不再下发tts数据
		_ = h.ttsProvider.Reset()
		_ = h.sendTtsMessage("", int(tts.StateCompleted)) // 若服务已停止，则发送tts结束消息
		h.log.Info("server voice stop, stop send tts data")
		return true
	}
	if len(data) == 0 && state != tts.StateCompleted {
		return false
	}
	if err := h.sendTtsMessage(string(data), int(state)); err != nil {
		h.log.Errorf("failed to send tts message: %v", err)
	}
	if state == tts.StateCompleted {
		if h.closeAfterChat { // 对话结束后关闭连接
			h.log.Info("close after chat")
			h.close()
			return true
		}
		_ = h.ttsProvider.Reset()
		return true
	}
	return false
}

func (h *Handler) clientAbortChat() error {
	h.log.Infof("client abort chat")
	// 客户端中止对话后，应停止服务端下发数据
	atomic.StoreInt32(&h.serverStopSend, 1)
	// 中止agent运行
	if h.agent != nil {
		h.agent.Abort()
	}
	// 继续接收客户端数据
	atomic.StoreInt32(&h.serverStopRecv, 0)
	return nil
}

func (h *Handler) isExit(text string) bool {
	if len(h.cfg.CMDExit) == 0 {
		return false
	}
	// 移除标点符号
	text = util.RemoveAllPunctuation(text)
	for _, cmd := range h.cfg.CMDExit {
		if text == cmd {
			h.log.Info("exit dialogue")
			h.close()
			return true
		}
	}
	return false
}

func (h *Handler) close() {
	h.once.Do(func() {
		close(h.stopChan)
		// 关闭对话后，停止服务端下发数据
		atomic.StoreInt32(&h.serverStopSend, 1)

		if h.asrProvider != nil {
			if err := h.asrProvider.Reset(); err != nil {
				h.log.Errorf("failed to reset asr provider: %v", err)
			}
		}
		if h.agent != nil {
			h.agent.Reset()
		}
		if h.ttsProvider != nil {
			if err := h.ttsProvider.Reset(); err != nil {
				h.log.Errorf("failed to reset tts provider: %v", err)
			}
		}
	})
}
