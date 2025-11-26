package model

// ClientTextMessage 客户端发送的文本消息结构
// Type 字段用于区分不同的消息类型
// Type 为 hello 时，用于初始化连接
// Type 为 chat 时，用于发送聊天文本，需要带上 ChatText 字段
// Type 为 abort 时，用于终止当前的对话，不需要其他字段
type ClientTextMessage struct {
	Type      string `json:"type"`
	ChatText  string `json:"chat_text,omitempty"`
	EnableAsr bool   `json:"enable_asr,omitempty"`
	EnableTts bool   `json:"enable_tts,omitempty"`
	AsrParams struct {
		Format     string `json:"format,omitempty"`      // 音频格式，如 "pcm"
		SampleRate int    `json:"sample_rate,omitzero"`  // 采样率，如 16000
		Channels   int    `json:"channels,omitzero"`     // 声道数，如 1: 单声道，2: 双声道
		VadEos     int    `json:"vad_eos,omitempty"`     // VAD后端点，默认800，单位毫秒
		EnablePunc bool   `json:"enable_punc,omitempty"` // 是否启用标点符号，默认false
		Language   string `json:"language,omitempty"`    // 语言，如 "zh"
		Accent     string `json:"accent,omitempty"`      // 口音，如 "mandarin"
	} `json:"asr_params,omitzero"`
	TtsParams struct {
		Speaker    string  `json:"speaker,omitempty"`    // 发音人
		Format     string  `json:"format,omitempty"`     // 音频格式，如 "mp3"
		Speed      float32 `json:"speed,omitzero"`       // 语速，默认为1.0
		Volume     int     `json:"volume,omitzero"`      // 音量，默认为50
		Pitch      float32 `json:"pitch,omitzero"`       // 语调，默认为1.0
		SampleRate int     `json:"sample_rate,omitzero"` // 采样率，默认为16000
		Language   string  `json:"language,omitempty"`   // 语言，如 "zh"
	} `json:"tts_params,omitzero"`
}
