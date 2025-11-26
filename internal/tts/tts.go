package tts

import (
	"context"

	"crow/internal/config"
)

// State tts合成状态
type State int

const (
	// StateProcessing tts合成中
	StateProcessing State = iota
	// StateCompleted tts合成结束
	StateCompleted
)

// Listener 语音合成事件监听者
type Listener interface {
	// OnTtsResult 语音合成结果回调
	// @param data 合成音频数据
	// @param state 合成状态
	// @return 是否不再监听语音合成事件
	OnTtsResult(data []byte, state State) bool
}

// Config 需要请求tts的相关配置
type Config struct {
	config.TtsConfig
	// 可选参数
	Speaker    string  // 发音人
	Speed      float32 // 语速
	Volume     int     // 音量
	Pitch      float32 // 语调
	Format     string  // 合成音频的格式
	SampleRate int     // 合成音频的采样率
	Language   string  // 合成的语言
}

type Provider interface {
	// SetConfig 设置 Provider 的配置
	// @param cfg: 客户端需求的配置
	// @return *Config: 实际请求的配置
	SetConfig(cfg *Config) *Config
	// SetListener 设置 Provider 的监听者
	SetListener(listener Listener)
	// ToTTS 将文本给到 Provider 进行语音合成
	// @param text: 待合成的文本或文本片段
	ToTTS(ctx context.Context, text string) error
	// ToSessionFinish 发送会话结束消息，即需要合成的文本已发送结束
	ToSessionFinish() error
	// Reset 重置 Provider
	Reset() error
}
