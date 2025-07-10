package asr

import "context"

// State asr识别状态
type State int

const (
	// StateProcessing asr识别中
	StateProcessing State = iota
	// StateSentenceEnd asr单句识别结束
	StateSentenceEnd
	// StateCompleted asr识别结束
	StateCompleted
)

type Listener interface {
	OnAsrResult(result string, state State) bool
}

type Config struct {
	APIKey string
	// 以下为可选参数
	Language   string // 语种
	Accent     string // 方言
	SampleRate int    // 音频采样率
	Format     string // 音频格式
	EnablePunc bool   // 是否启用标点符号
	VadEos     int    // 语音活动检测时长后端点(vad_eos)，0为关闭，单位毫秒
}

type Provider interface {
	// SetConfig 设置 Provider 的配置
	// @param cfg: 客户端需求的配置
	// @return *Config: 实际请求的配置
	SetConfig(cfg *Config) *Config
	// SendAudio 发送音频数据
	// @param data: 终端上传的待识别音频数据
	SendAudio(ctx context.Context, data []byte) error
	// GetSilenceCount 获取当前的静音次数
	GetSilenceCount() int
	// Reset 重置 Provider
	Reset() error
}
