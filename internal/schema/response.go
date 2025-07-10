package schema

type BaseResponse struct {
	ErrorCode int    `json:"error_code,omitempty"` // 默认0，成功
	ErrorMsg  string `json:"error_msg,omitempty"`
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
}

type HelloResponse struct {
	BaseResponse
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

type AsrResponse struct {
	BaseResponse
	Result string `json:"result"`
	State  int    `json:"state"`
}

type ChatResponse struct {
	BaseResponse
	Text string `json:"text"`
}

type TtsResponse struct {
	BaseResponse
	Audio string `json:"audio"` // base64编码的音频数据
	State int    `json:"state"`
}
