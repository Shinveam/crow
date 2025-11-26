package handler

import (
	"encoding/json"
	"fmt"

	"github.com/gorilla/websocket"

	"crow/internal/model"
)

func (h *Handler) sendErrorMessage(code int, msg string) error {
	errorMsg := model.BaseResponse{
		Type:      "error",
		SessionID: h.sessionID,
		ErrorCode: code,
		ErrorMsg:  msg,
	}
	data, err := json.Marshal(errorMsg)
	if err != nil {
		return fmt.Errorf("failed to marshal error message: %v", err)
	}
	if err = h.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		if h.conn.IsClosed() {
			h.close()
			return nil
		}
		return fmt.Errorf("failed to send error message: %v", err)
	}
	return nil
}

func (h *Handler) sendHelloMessage(msg model.HelloResponse) error {
	msg.BaseResponse.Type = "hello"
	msg.BaseResponse.SessionID = h.sessionID
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal hello message: %v", err)
	}
	if err = h.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		if h.conn.IsClosed() {
			h.close()
			return nil
		}
		return fmt.Errorf("failed to send hello message: %v", err)
	}
	return nil
}

func (h *Handler) sendAsrMessage(result string, state int) error {
	msg := model.AsrResponse{
		BaseResponse: model.BaseResponse{
			Type:      "asr",
			SessionID: h.sessionID,
		},
		Result: result,
		State:  state,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal asr message: %v", err)
	}
	if err = h.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		if h.conn.IsClosed() {
			h.close()
			return nil
		}
		return fmt.Errorf("failed to send asr message: %v", err)
	}
	return nil
}

func (h *Handler) sendChatMessage(text string) error {
	msg := model.ChatResponse{
		BaseResponse: model.BaseResponse{
			Type:      "chat",
			SessionID: h.sessionID,
		},
		Text: text,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal chat message: %v", err)
	}
	if err = h.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		if h.conn.IsClosed() {
			h.close()
			return nil
		}
		return fmt.Errorf("failed to send chat message: %v", err)
	}
	return nil
}

func (h *Handler) sendTtsMessage(audio string, state int) error {
	msg := model.TtsResponse{
		BaseResponse: model.BaseResponse{
			Type:      "tts",
			SessionID: h.sessionID,
		},
		Audio: audio,
		State: state,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal tts message: %v", err)
	}
	if err = h.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		if h.conn.IsClosed() {
			h.close()
			return nil
		}
		return fmt.Errorf("failed to send tts message: %v", err)
	}
	return nil
}
