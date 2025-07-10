package cosy_voice

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"crow/internal/tts"
	log2 "crow/pkg/log"
)

// 阿里语音合成 CosyVoice WebSocket API 文档
// https://help.aliyun.com/zh/model-studio/cosyvoice-websocket-api

const (
	wsURL = "wss://dashscope.aliyuncs.com/api-ws/v1/inference/" // WebSocket服务端地址
)

type CosyVoice struct {
	cfg *tts.Config
	log *log2.Logger

	conn     *websocket.Conn
	listener tts.Listener

	lock sync.Mutex

	isRunning   bool
	sendDataCnt int
	connectID   string
	reqID       string
	taskID      string
}

func NewCosyVoice(listener tts.Listener, log *log2.Logger) *CosyVoice {
	return &CosyVoice{
		log:       log,
		listener:  listener,
		connectID: fmt.Sprintf("%d", time.Now().UnixNano()),
	}
}

func (c *CosyVoice) SetConfig(cfg *tts.Config) *tts.Config {
	if cfg.Speaker == "" {
		cfg.Speaker = "longlaotie_v2"
	}
	if cfg.Speed < 0.5 || cfg.Speed > 2.0 {
		cfg.Speed = 1.0
	}
	if cfg.Volume < 0 || cfg.Volume > 100 {
		cfg.Volume = 50
	}
	if cfg.Pitch < 0.5 || cfg.Pitch > 2.0 {
		cfg.Pitch = 1.0
	}
	if cfg.Format == "" {
		cfg.Format = "mp3"
	}
	if cfg.SampleRate <= 0 {
		cfg.SampleRate = 16000
	}
	c.cfg = cfg
	return c.cfg
}

func (c *CosyVoice) ToTTS(ctx context.Context, text string) error {
	c.lock.Lock()
	isRunning := c.isRunning
	c.lock.Unlock()

	if !isRunning {
		if err := c.initConnection(ctx); err != nil {
			return err
		}
	}

	if len(text) > 0 && c.isRunning {
		// 直接发送文本数据
		err := c.sendTextData(text)
		if err != nil {
			return err
		}
		c.sendDataCnt++
		if c.sendDataCnt%20 == 0 {
			c.log.Debugf("send text data cnt: %d", c.sendDataCnt)
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

type Payload struct {
	TaskGroup  string     `json:"task_group"`
	Task       string     `json:"task"`
	Function   string     `json:"function"`
	Model      string     `json:"model"`
	Parameters Params     `json:"parameters"`
	Resources  []Resource `json:"resources"`
	Input      Input      `json:"input"`
}

type Params struct {
	TextType   string  `json:"text_type"`
	Voice      string  `json:"voice"`
	Format     string  `json:"format"`
	SampleRate int     `json:"sample_rate"`
	Volume     int     `json:"volume"`
	Rate       float32 `json:"rate"`
	Pitch      float32 `json:"pitch"`
}

type Resource struct {
	ResourceID   string `json:"resource_id"`
	ResourceType string `json:"resource_type"`
}

type Input struct {
	Text string `json:"text"`
}

type Event struct {
	Header  Header  `json:"header"`
	Payload Payload `json:"payload"`
}

func (c *CosyVoice) initConnection(ctx context.Context) error {
	c.log.Info("start tts")

	c.lock.Lock()
	defer c.lock.Unlock()

	if c.isRunning {
		return nil
	}

	if c.conn != nil {
		c.closeConnection()
	}

	header := make(http.Header)
	header.Add("X-DashScope-DataInspection", "enable")
	header.Add("Authorization", fmt.Sprintf("bearer %s", c.cfg.APIKey))

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
			c.log.Warnf("failed to connect to the websocket, try %d/%d: %v, will try again %v", i+1, maxRetries+1, err, backoffTime)
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
	c.taskID, err = c.sendRunTaskCmd(conn)
	if err != nil {
		return fmt.Errorf("send run task cmd error: %v", err)
	}
	// 等待task-started事件
	msgType, message, err := conn.ReadMessage()
	if err != nil {
		return fmt.Errorf("get task-started event message error: %v", err)
	}
	if msgType != websocket.TextMessage {
		return fmt.Errorf("unexpected message type: %v", msgType)
	}
	var event Event
	if err = json.Unmarshal(message, &event); err != nil {
		return fmt.Errorf("error unmarshaling task-started event message: %v", err)
	}
	if event.Header.Event != "task-started" {
		return fmt.Errorf("unexpected task-started event, got: %s", event.Header.Event)
	}

	c.conn = conn
	c.isRunning = true
	c.reqID = fmt.Sprintf("%d", time.Now().UnixNano())

	c.log.Debugf("init tts succeed, connect_id: %s, req_id: %s", c.connectID, c.reqID)

	go c.readMessage()
	return nil
}

func (c *CosyVoice) sendTextData(text string) error {
	c.log.Debugf("sendTextData: data=%s, sendDataCnt=%d", text, c.sendDataCnt)
	if text == "" {
		return nil
	}

	defer func() {
		if err := recover(); err != nil {
			c.log.Errorf("tts send text data panic: %v", err)
		}
	}()

	runTaskCmd, err := c.generateContinueTaskCmd(text)
	if err != nil {
		return fmt.Errorf("generate continue task cmd error: %v", err)
	}
	err = c.conn.WriteMessage(websocket.TextMessage, []byte(runTaskCmd))
	if err != nil {
		return fmt.Errorf("send text data error: %v", err)
	}
	return nil
}

func (c *CosyVoice) readMessage() {
	c.log.Info("cosy voice start read message")

	defer func() {
		if err := recover(); err != nil {
			c.log.Errorf("tts read message panic: %v", err)
		}
		c.lock.Lock()
		c.isRunning = false
		if c.conn != nil {
			c.closeConnection()
		}
		c.lock.Unlock()
		c.log.Info("cosy voice read message stopped")
	}()

	for {
		// 检查连接状态，避免在连接关闭后继续读取
		c.lock.Lock()
		if !c.isRunning && c.conn == nil {
			c.lock.Unlock()
			c.log.Info("流式识别已结束或连接已关闭，退出读取循环")
			return
		}
		conn := c.conn
		c.lock.Unlock()

		msgType, message, err := conn.ReadMessage()
		if err != nil {
			c.setErrorAndStop(err)
			return
		}

		if msgType == websocket.BinaryMessage {
			// 处理二进制音频流
			base64Message := base64.StdEncoding.EncodeToString(message)
			if finished := c.listener.OnTtsResult([]byte(base64Message), tts.StateProcessing); finished {
				return
			}
		} else {
			var event Event
			if err = json.Unmarshal(message, &event); err != nil {
				c.log.Errorf("json unmarshal error: %v", err)
				continue
			}
			if c.handleEvent(event) {
				return
			}
		}
	}
}

// 发送run-task指令
func (c *CosyVoice) sendRunTaskCmd(conn *websocket.Conn) (string, error) {
	runTaskCmd, taskID, err := c.generateRunTaskCmd()
	if err != nil {
		return "", err
	}
	err = conn.WriteMessage(websocket.TextMessage, []byte(runTaskCmd))
	return taskID, err
}

// 生成run-task指令
func (c *CosyVoice) generateRunTaskCmd() (string, string, error) {
	taskID := uuid.New().String()
	runTaskCmd := Event{
		Header: Header{
			Action:    "run-task",
			TaskID:    taskID,
			Streaming: "duplex",
		},
		Payload: Payload{
			TaskGroup: "audio",
			Task:      "tts",
			Function:  "SpeechSynthesizer",
			Model:     "cosyvoice-v2",
			Parameters: Params{
				TextType:   "PlainText",
				Voice:      c.cfg.Speaker,
				Format:     c.cfg.Format,
				SampleRate: c.cfg.SampleRate,
				Volume:     c.cfg.Volume,
				Rate:       c.cfg.Speed,
				Pitch:      c.cfg.Pitch,
			},
			Input: Input{},
		},
	}
	runTaskCmdJSON, err := json.Marshal(runTaskCmd)
	return string(runTaskCmdJSON), taskID, err
}

// 生成continue-task指令
func (c *CosyVoice) generateContinueTaskCmd(text string) (string, error) {
	runTaskCmd := Event{
		Header: Header{
			Action:    "continue-task",
			TaskID:    c.taskID,
			Streaming: "duplex",
		},
		Payload: Payload{
			Input: Input{
				Text: text,
			},
		},
	}
	runTaskCmdJSON, err := json.Marshal(runTaskCmd)
	return string(runTaskCmdJSON), err
}

// 处理事件
func (c *CosyVoice) handleEvent(event Event) bool {
	switch event.Header.Event {
	case "result-generated":
		// 忽略result-generated事件
		return false
	case "task-finished":
		c.listener.OnTtsResult(nil, tts.StateCompleted)
		return true
	case "task-failed":
		if event.Header.ErrorMessage != "" {
			c.setErrorAndStop(errors.New(event.Header.ErrorMessage))
		} else {
			c.setErrorAndStop(errors.New("未知原因导致任务失败"))
		}
		c.listener.OnTtsResult(nil, tts.StateCompleted)
		return true
	default:
		fmt.Printf("预料之外的事件：%v\n", event)
	}
	return false
}

func (c *CosyVoice) setErrorAndStop(err error) {
	c.lock.Lock()
	defer c.lock.Unlock()

	c.isRunning = false

	if strings.Contains(err.Error(), "use of closed network connection") {
		c.log.Debugf("setErrorAndStop: %v, sendDataCnt=%d", err, c.sendDataCnt)
	} else {
		c.log.Errorf("setErrorAndStop: %v, sendDataCnt=%d", err, c.sendDataCnt)
	}

	if c.conn != nil {
		c.closeConnection()
	}
}

func (c *CosyVoice) ToSessionFinish() error {
	if err := c.sendFinishTaskCmd(); err != nil {
		c.log.Errorf("send finish task cmd error: %v", err)
		return err
	}
	return nil
}

// 关闭连接
func (c *CosyVoice) closeConnection() {
	defer func() {
		if r := recover(); r != nil {
			c.log.Errorf("tts close error: %v", r)
		}
	}()

	if c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
	}
}

// 发送finish-task指令
func (c *CosyVoice) sendFinishTaskCmd() error {
	if c.conn == nil || !c.isRunning {
		return nil
	}
	finishTaskCmd, err := c.generateFinishTaskCmd()
	if err != nil {
		return err
	}
	err = c.conn.WriteMessage(websocket.TextMessage, []byte(finishTaskCmd))
	return err
}

// 生成finish-task指令
func (c *CosyVoice) generateFinishTaskCmd() (string, error) {
	finishTaskCmd := Event{
		Header: Header{
			Action:    "finish-task",
			TaskID:    c.taskID,
			Streaming: "duplex",
		},
		Payload: Payload{
			Input: Input{},
		},
	}
	finishTaskCmdJSON, err := json.Marshal(finishTaskCmd)
	return string(finishTaskCmdJSON), err
}

func (c *CosyVoice) Reset() error {
	c.lock.Lock()
	defer c.lock.Unlock()

	c.isRunning = false
	c.closeConnection()

	c.taskID = ""
	c.sendDataCnt = 0

	c.log.Info("cosy voice reset")
	return nil
}
