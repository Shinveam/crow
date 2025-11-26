package llm

import (
	"context"
	"time"

	"crow/internal/agent/schema"
)

// Request 大模型请求
type Request struct {
	// IsSupportImages 是否支持图片
	IsSupportImages bool
	// Timeout 模型请求超时时间, 默认300秒
	Timeout time.Duration
	// ToolChoice 工具调用方式，默认auto
	ToolChoice schema.ToolChoice
	// Tools // 需要调用的工具
	Tools []schema.Tool
	// SystemMessage 系统消息--可不设置
	SystemMessage schema.Message
	// Messages 上下文
	Messages []schema.Message
}

// Response 大模型响应
type Response struct {
	// Content 模型响应的完整回复语
	Content string
	// ToolCalls 模型需要调用的工具
	ToolCalls []schema.ToolCall
}

// LLM 大模型接口，采用流式处理
type LLM interface {
	// Handle 处理用户请求
	// @param messages: 请求的消息列表(上下文消息)
	Handle(ctx context.Context, request *Request) (*Response, error)
	// Recv 接收模型响应
	// 流式处理，用于需要实时处理的场景
	// @return string: 模型响应的回复语
	// @return error: 接收过程中的错误，如果错误为 io.EOF 则表示模型响应结束
	Recv() (string, error)
	// Reset 重置 LLM
	Reset() error
}
