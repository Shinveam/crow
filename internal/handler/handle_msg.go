package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/gorilla/websocket"

	"crow/internal/asr"
	"crow/internal/model"
	"crow/internal/tts"
	errcode "crow/pkg/err-code"
)

func (h *Handler) handleMessage(messageType int, message []byte) error {
	switch messageType {
	case websocket.TextMessage:
		h.clientTextQueue <- string(message)
		return nil
	case websocket.BinaryMessage:
		if h.clientAudioQueue != nil {
			h.clientAudioQueue <- message
		}
		return nil
	default:
		return fmt.Errorf("unsupported message type: %d", messageType)
	}
}

func (h *Handler) handleClientTextMessages(ctx context.Context, content string) error {
	var data model.ClientTextMessage
	if err := json.Unmarshal([]byte(content), &data); err != nil {
		_ = h.sendErrorMessage(errcode.ErrInvalidDataType.Code(), errcode.ErrInvalidDataType.Msg())
		return fmt.Errorf("failed to unmarshal text message: %v", err)
	}
	switch data.Type {
	case "abort":
		return h.handleAbortChat()
	case "chat":
		// 如果有新的对话文本，则应该打断当前的对话
		_ = h.handleAbortChat()
		return h.handleChatMessage(ctx, data.ChatText)
	default:
		return fmt.Errorf("unsupported message type: %s", data.Type)
	}
}

func (h *Handler) handleHelloMessage(ctx context.Context) error {
	msg := model.HelloResponse{
		BaseResponse: model.BaseResponse{
			Type:      "hello",
			SessionID: h.sessionID,
		},
	}

	// 进行hello验证
	messageType, message, err := h.conn.ReadMessage()
	if err != nil {
		_ = h.sendErrorMessage(errcode.ErrInternal.Code(), errcode.ErrInternal.Msg())
		return fmt.Errorf("failed to read message: %v", err)
	}
	if messageType != websocket.TextMessage {
		_ = h.sendErrorMessage(errcode.ErrInvalidDataType.Code(), errcode.ErrInvalidDataType.Msg())
		return fmt.Errorf("unsupported message type: %d", messageType)
	}

	var data model.ClientTextMessage
	if err = json.Unmarshal(message, &data); err != nil {
		_ = h.sendErrorMessage(errcode.ErrInvalidDataType.Code(), errcode.ErrInvalidDataType.Msg())
		return fmt.Errorf("failed to unmarshal text message: %v", err)
	}

	h.enableAsr = data.EnableAsr
	h.enableTts = data.EnableTts

	if data.EnableAsr {
		asrCfg := &asr.Config{
			Language:   data.AsrParams.Language,
			Accent:     data.AsrParams.Accent,
			SampleRate: data.AsrParams.SampleRate,
			Format:     data.AsrParams.Format,
			EnablePunc: data.AsrParams.EnablePunc,
			VadEos:     data.AsrParams.VadEos,
		}
		if v, ok := h.cfg.SelectedModule["asr"]; ok {
			if cfg, ok := h.cfg.Asr[v]; ok {
				asrCfg.ApiKey = cfg.ApiKey
				asrCfg.AppID = cfg.AppID
				asrCfg.AccessToken = cfg.AccessToken
			}
		}
		asrCfg = h.asrProvider.SetConfig(asrCfg)

		msg.AsrParams.Language = asrCfg.Language
		msg.AsrParams.Accent = asrCfg.Accent
		msg.AsrParams.SampleRate = asrCfg.SampleRate
		msg.AsrParams.Format = asrCfg.Format
		msg.AsrParams.EnablePunc = asrCfg.EnablePunc
		msg.AsrParams.VadEos = asrCfg.VadEos

		// 开启asr后，需要开始监听客户端音频消息
		h.clientAudioQueue = make(chan []byte, 100)
		go h.listenClientAudioMessages(ctx)
	}

	// 只有启用了tts才需要设置
	if data.EnableTts {
		ttsCfg := &tts.Config{
			Speaker:    data.TtsParams.Speaker,
			Speed:      data.TtsParams.Speed,
			Volume:     data.TtsParams.Volume,
			Pitch:      data.TtsParams.Pitch,
			SampleRate: data.TtsParams.SampleRate,
			Format:     data.TtsParams.Format,
			Language:   data.TtsParams.Language,
		}
		if v, ok := h.cfg.SelectedModule["tts"]; ok {
			if cfg, ok := h.cfg.Tts[v]; ok {
				ttsCfg.ApiKey = cfg.ApiKey
				ttsCfg.AppID = cfg.AppID
				ttsCfg.Token = cfg.Token
				ttsCfg.Cluster = cfg.Cluster
				ttsCfg.ResourceID = cfg.ResourceID
			}
		}
		ttsCfg = h.ttsProvider.SetConfig(ttsCfg)

		msg.TtsParams.Speaker = ttsCfg.Speaker
		msg.TtsParams.Speed = ttsCfg.Speed
		msg.TtsParams.Volume = ttsCfg.Volume
		msg.TtsParams.Pitch = ttsCfg.Pitch
		msg.TtsParams.SampleRate = ttsCfg.SampleRate
		msg.TtsParams.Format = ttsCfg.Format
		msg.TtsParams.Language = ttsCfg.Language
	}

	// 开始监听客户端文本消息
	h.clientTextQueue = make(chan string, 100)
	go h.listenClientTextMessages(ctx)
	return h.sendHelloMessage(msg)
}

func (h *Handler) handleAbortChat() error {
	h.log.Infof("client abort chat")
	atomic.StoreInt32(&h.interrupt, 1)
	if h.agentProvider != nil {
		_ = h.agentProvider.Reset()
	}
	if h.ttsProvider != nil {
		_ = h.ttsProvider.Reset()
	}
	return nil

}

func (h *Handler) handleChatMessage(ctx context.Context, text string) error {
	if text == "" {
		_ = h.handleAbortChat()
		return errors.New("empty text message, skip")
	}

	h.chatRound++
	h.log.Infof("start new chat round: %d", h.chatRound)

	if h.isExit(text) {
		h.closeAfterChat = true           // 存在退出意图则在此次对话后关闭连接
		atomic.StoreInt32(&h.stopRecv, 1) // 不再接收客户端消息
		h.log.Info("user request exit, abort chat")
	}

	// 如果有中断信号，须关闭中断，保证下一轮对话可打断
	if atomic.LoadInt32(&h.interrupt) == 1 {
		atomic.StoreInt32(&h.interrupt, 0)
	}

	// 开启协程运行agent，避免agent运行时无法打断处理
	go func() {
		if err := h.agentProvider.Run(ctx, text); err != nil {
			// 如果无法正常运行agent，且需要在此次对话后关闭连接，则直接关闭连接
			if h.closeAfterChat {
				h.close()
			}
			h.log.Errorf("agent run error: %v", err)
			return
		}

		// 对话结束后关闭连接
		if h.closeAfterChat {
			h.log.Info("close after chat")
			h.close()
			return
		}
	}()

	return nil
}
