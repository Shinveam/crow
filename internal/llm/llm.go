package llm

import (
	"context"
	"time"

	"crow/internal/schema"
)

type AskRequestParams struct {
	IsSupportImages bool
	SystemMessage   schema.Message   // 系统消息--可不设置
	Messages        []schema.Message // 用户消息
}

type AskToolRequestParams struct {
	IsSupportImages bool
	Timeout         time.Duration     // 模型请求超时时间, 默认300秒
	ToolChoice      schema.ToolChoice // 工具调用方式，默认auto
	Tools           []schema.Tool     // 需要调用的工具
	SystemMessage   schema.Message    // 系统消息--可不设置
	Messages        []schema.Message  // 用户消息
}

type AskToolResponse struct {
	Content   string            // 模型响应的完整回复语
	ToolCalls []schema.ToolCall // 模型需要调用的工具
}

type LLM interface {
	// GetStreamReplyText 获取流式请求的响应文本
	// @return string: 过程中的文本
	// @return bool: 是否还有数据
	GetStreamReplyText() (string, bool)
	// Ask 纯文本请求，无function calling处理，支持视觉模型
	// @return string: 模型响应的完整回复语
	Ask(context.Context, AskRequestParams) (string, error)
	// AskTool 含function calling的请求，支持视觉模型
	AskTool(context.Context, AskToolRequestParams) (*AskToolResponse, error)
	// IsFinalFlag 判断是否是结束标识，用于流式响应
	IsFinalFlag(text string) bool
	// Cleanup 模型请求完成后的清理，例如流式响应需要关闭其中的管道等操作
	Cleanup()
}
