package paraformer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"crow/internal/asr"
	log2 "crow/pkg/log"
)

// 阿里 Paraformer 实时语音识别 WebSocket API 文档
// https://help.aliyun.com/zh/model-studio/websocket-for-paraformer-real-time-service

const (
	wsURL       = "wss://dashscope.aliyuncs.com/api-ws/v1/inference/" // WebSocket服务器地址
	idleTimeout = 30 * time.Second                                    // 没有新的文本数据则结束识别
)

type Paraformer struct {
	cfg *asr.Config
	log *log2.Logger

	conn     *websocket.Conn
	listener asr.Listener

	lock sync.Mutex

	isRunning bool
	reqID     string
	connectID string
	taskID    string

	sendDataCnt     int
	startListenTime time.Time
	silenceCount    int
}

func NewParaformer(listener asr.Listener, log *log2.Logger) *Paraformer {
	return &Paraformer{
		listener:  listener,
		connectID: fmt.Sprintf("%d", time.Now().UnixNano()),
		log:       log,
	}
}

func (p *Paraformer) SetConfig(cfg *asr.Config) *asr.Config {
	if cfg.Language == "" {
		cfg.Language = "zh"
	}
	if cfg.SampleRate <= 0 {
		cfg.SampleRate = 16000
	}
	if cfg.Format == "" {
		cfg.Format = "pcm"
	}
	if cfg.VadEos < 200 || cfg.VadEos > 6000 {
		cfg.VadEos = 800
	}
	p.cfg = cfg
	return p.cfg
}

func (p *Paraformer) SendAudio(ctx context.Context, data []byte) error {
	// 使用锁检查状态
	p.lock.Lock()
	isRunning := p.isRunning
	p.lock.Unlock()

	if !isRunning {
		if err := p.initConnection(ctx); err != nil {
			return err
		}
	}

	// 检查是否有实际数据需要发送
	if len(data) > 0 && p.isRunning {
		// 直接发送音频数据
		err := p.sendAudioData(data, false)
		if err != nil {
			return err
		}
		p.sendDataCnt++
		if p.sendDataCnt%20 == 0 {
			p.log.Debugf("send audio data cnt: %d", p.sendDataCnt)
		}
	}
	return nil
}

type Header struct {
	Action       string         `json:"action"`
	TaskID       string         `json:"task_id"`
	Streaming    string         `json:"streaming"`
	Event        string         `json:"event"`
	ErrorCode    string         `json:"error_code,omitempty"`
	ErrorMessage string         `json:"error_message,omitempty"`
	Attributes   map[string]any `json:"attributes"`
}

type Output struct {
	Sentence struct {
		SentenceEnd bool   `json:"sentence_end"`
		BeginTime   int64  `json:"begin_time"`
		EndTime     *int64 `json:"end_time"`
		Text        string `json:"text"`
		Words       []struct {
			BeginTime   int64  `json:"begin_time"`
			EndTime     *int64 `json:"end_time"`
			Text        string `json:"text"`
			Punctuation string `json:"punctuation"`
		} `json:"words"`
	} `json:"sentence"`
	Usage any `json:"usage"`
}

type Payload struct {
	TaskGroup  string `json:"task_group"`
	Task       string `json:"task"`
	Function   string `json:"function"`
	Model      string `json:"model"`
	Parameters Params `json:"parameters"`
	// 不使用热词功能时，不要传递resources参数
	// Resources  []Resource `json:"resources"`
	Input  Input  `json:"input"`
	Output Output `json:"output,omitempty"`
}

type Params struct {
	Format                       string   `json:"format"`
	SampleRate                   int      `json:"sample_rate"`
	VocabularyID                 string   `json:"vocabulary_id"`
	DisfluencyRemovalEnabled     bool     `json:"disfluency_removal_enabled"`     // 支持的音频格式：pcm、wav、mp3、opus、speex、aac、amr
	LanguageHints                []string `json:"language_hints"`                 // 识别语种，中文: zh, 英文: en，粤语: yue
	SemanticPunctuationEnabled   bool     `json:"semantic_punctuation_enabled"`   // 是否语义断句，默认false，采用vad
	MaxSentenceSilence           int      `json:"max_sentence_silence"`           // vad时长，semantic_punctuation_enabled为false时生效
	PunctuationPredictionEnabled bool     `json:"punctuation_prediction_enabled"` // 识别结果中自动添加标点
	Heartbeat                    bool     `json:"heartbeat"`                      // 是否在持续发送静音音频的情况下，保持与服务端的连接不中断
}

// Resource 不使用热词功能时，不要传递resources参数
type Resource struct {
	ResourceID   string `json:"resource_id"`
	ResourceType string `json:"resource_type"`
}

type Input struct{}

type Event struct {
	Header  Header  `json:"header"`
	Payload Payload `json:"payload"`
}

func (p *Paraformer) initConnection(ctx context.Context) error {
	p.log.Info("start asr")
	p.startListenTime = time.Now()

	p.lock.Lock()
	defer p.lock.Unlock()

	if p.isRunning {
		return nil
	}

	// 确保旧连接已关闭
	if p.conn != nil {
		p.closeConnection()
	}

	header := make(http.Header)
	header.Add("X-DashScope-DataInspection", "enable")
	header.Add("Authorization", fmt.Sprintf("bearer %s", p.cfg.APIKey))

	var (
		conn *websocket.Conn
		resp *http.Response
		err  error
	)
	maxRetries := 2 // 最大重试次数
	for i := 0; i < maxRetries; i++ {
		dialer := websocket.DefaultDialer
		conn, resp, err = dialer.DialContext(ctx, wsURL, header)
		if err == nil {
			break
		}

		if i < maxRetries {
			backoffTime := time.Duration(500*(i+1)) * time.Millisecond
			p.log.Warnf("failed to connect to the websocket, try %d/%d: %v, will try again %v", i+1, maxRetries+1, err, backoffTime)
			time.Sleep(backoffTime)
		}
	}

	if err != nil {
		statusCode := 0
		if resp != nil {
			statusCode = resp.StatusCode
		}
		return fmt.Errorf("falied to connect(status_code:%d): %v", statusCode, err)
	}

	// 发送run-task指令
	p.taskID, err = p.sendRunTaskCmd(conn)
	if err != nil {
		return fmt.Errorf("send run task cmd error: %v", err)
	}
	// 等待task-started事件
	_, message, err := conn.ReadMessage()
	if err != nil {
		return fmt.Errorf("get task-started event message error: %v", err)
	}
	var event Event
	err = json.Unmarshal(message, &event)
	if err != nil {
		return fmt.Errorf("error unmarshaling task-started event message: %v", err)
	}
	if event.Header.Event != "task-started" {
		return fmt.Errorf("unexpected task-started event, got: %s", event.Header.Event)
	}

	p.conn = conn
	p.isRunning = true
	p.reqID = fmt.Sprintf("%d", time.Now().UnixNano())

	p.log.Debugf("init asr succeed, connect_id: %s, req_id: %s", p.connectID, p.reqID)

	go p.readMessage()
	return nil
}

func (p *Paraformer) readMessage() {
	p.log.Info("paraformer read message started")

	defer func() {
		if err := recover(); err != nil {
			p.log.Errorf("asr read goroutine panic: %v", err)
		}
		p.lock.Lock()
		p.isRunning = false
		if p.conn != nil {
			p.closeConnection()
		}
		p.lock.Unlock()
		p.log.Info("paraformer read message stopped")
	}()

	for {
		// 检查连接状态，避免在连接关闭后继续读取
		p.lock.Lock()
		if !p.isRunning && p.conn == nil {
			p.lock.Unlock()
			p.log.Info("recognition has ended or the connection has been closed, exiting the read loop")
			return
		}
		p.lock.Unlock()

		// _ = p.conn.SetReadDeadline(time.Now().Add(30 * time.Second))

		_, message, err := p.conn.ReadMessage()
		if err != nil {
			p.setErrorAndClose(fmt.Errorf("failed to read message: %v", err))
			return
		}

		var event Event
		err = json.Unmarshal(message, &event)
		if err != nil {
			p.log.Errorf("failed to resolve the event: %v", err)
			continue
		}
		if p.handleEvent(event) {
			return
		}
	}
}

func (p *Paraformer) sendAudioData(data []byte, isLast bool) error {
	if len(data) == 0 && !isLast {
		return nil
	}

	defer func() {
		if err := recover(); err != nil {
			p.log.Errorf("asr send audio data panic: %v", err)
		}
	}()

	if err := p.conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
		return fmt.Errorf("send audio error: %v", err)
	}
	return nil
}

// 发送run-task指令
func (p *Paraformer) sendRunTaskCmd(conn *websocket.Conn) (string, error) {
	runTaskCmd, taskID, err := p.generateRunTaskCmd()
	if err != nil {
		return "", err
	}
	err = conn.WriteMessage(websocket.TextMessage, []byte(runTaskCmd))
	return taskID, err
}

// 生成run-task指令
func (p *Paraformer) generateRunTaskCmd() (string, string, error) {
	taskID := uuid.New().String()
	runTaskCmd := Event{
		Header: Header{
			Action:    "run-task",
			TaskID:    taskID,
			Streaming: "duplex",
		},
		Payload: Payload{
			TaskGroup: "audio",
			Task:      "asr",
			Function:  "recognition",
			Model:     "paraformer-realtime-v2",
			Parameters: Params{
				Format:                       p.cfg.Format,
				SampleRate:                   p.cfg.SampleRate,
				LanguageHints:                []string{p.cfg.Language},
				MaxSentenceSilence:           p.cfg.VadEos,
				PunctuationPredictionEnabled: p.cfg.EnablePunc,
				Heartbeat:                    true,
			},
			Input: Input{},
		},
	}
	runTaskCmdJSON, err := json.Marshal(runTaskCmd)
	return string(runTaskCmdJSON), taskID, err
}

// 发送finish-task指令
func (p *Paraformer) sendFinishTaskCmd() error {
	finishTaskCmd, err := p.generateFinishTaskCmd()
	if err != nil {
		return err
	}
	err = p.conn.WriteMessage(websocket.TextMessage, []byte(finishTaskCmd))
	return err
}

// 生成finish-task指令
func (p *Paraformer) generateFinishTaskCmd() (string, error) {
	finishTaskCmd := Event{
		Header: Header{
			Action:    "finish-task",
			TaskID:    p.taskID,
			Streaming: "duplex",
		},
		Payload: Payload{
			Input: Input{},
		},
	}
	finishTaskCmdJSON, err := json.Marshal(finishTaskCmd)
	return string(finishTaskCmdJSON), err
}

// 处理事件
func (p *Paraformer) handleEvent(event Event) bool {
	switch event.Header.Event {
	case "result-generated":
		text := event.Payload.Output.Sentence.Text
		if text == "" && !p.startListenTime.IsZero() && time.Since(p.startListenTime) > idleTimeout {
			p.silenceCount++
		} else if text != "" {
			p.silenceCount = 0 // 重置静音计数
		}
		state := asr.StateProcessing
		if event.Payload.Output.Sentence.SentenceEnd {
			state = asr.StateSentenceEnd
		}
		if finished := p.listener.OnAsrResult(text, state); finished {
			return true
		}
	case "task-finished":
		p.listener.OnAsrResult("", asr.StateCompleted)
		return true
	case "task-failed":
		if event.Header.ErrorMessage != "" {
			p.setErrorAndClose(errors.New(event.Header.ErrorMessage))
		} else {
			p.setErrorAndClose(errors.New("the task failed due to an unknown reason"))
		}
		p.listener.OnAsrResult("", asr.StateCompleted)
		return true
	default:
		p.log.Infof("unexpected events: %v", event)
	}
	return false
}

func (p *Paraformer) setErrorAndClose(err error) {
	p.lock.Lock()
	defer p.lock.Unlock()

	p.isRunning = false

	if strings.Contains(err.Error(), "use of closed network connection") {
		p.log.Debugf("setErrorAndClose: %v, sendDataCnt=%d", err, p.sendDataCnt)
	} else {
		p.log.Errorf("setErrorAndClose: %v, sendDataCnt=%d", err, p.sendDataCnt)
	}

	if p.conn != nil {
		p.closeConnection()
	}
}

func (p *Paraformer) closeConnection() {
	defer func() {
		if err := recover(); err != nil {
			p.log.Errorf("asr close error: %v", err)
		}
	}()
	// 发送finish-task指令
	if err := p.sendFinishTaskCmd(); err != nil {
		p.log.Errorf("send finish task cmd error: %v", err)
	}
	if p.conn != nil {
		_ = p.conn.Close()
		p.conn = nil
	}
}

func (p *Paraformer) Reset() error {
	// 使用锁保护状态变更
	p.lock.Lock()
	defer p.lock.Unlock()

	p.isRunning = false
	p.closeConnection()

	p.silenceCount = 0
	p.sendDataCnt = 0
	p.taskID = ""

	p.log.Info("paraformer reset")
	return nil
}

func (p *Paraformer) GetSilenceCount() int {
	return p.silenceCount
}
