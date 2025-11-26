package agent

import "context"

// State agent状态
type State int

const (
	// StateProcessing 处理中
	StateProcessing State = iota
	// StateCompleted agent响应结束
	StateCompleted
)

// Listener 语音合成事件监听者
type Listener interface {
	// OnAgentResult agent结果回调
	// @param text 回复文本
	// @param state agent状态
	// @return 是否不再监听agent事件
	OnAgentResult(ctx context.Context, text string, state State) bool
}

// Provider Agent提供者
// 服务端流式Agent，一次文本请求，多次响应
type Provider interface {
	// SetConfig 设置Agent配置
	SetConfig(cfg any)
	// SetListener 设置 Provider 监听者
	SetListener(listener Listener)
	// Run 运行Agent
	// @param userPrompt 用户提示词
	Run(ctx context.Context, userPrompt string) error
	// Reset 重置Agent
	Reset() error
}
