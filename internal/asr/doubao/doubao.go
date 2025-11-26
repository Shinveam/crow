package doubao

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"crow/internal/asr"
	"crow/pkg/log"
)

// 豆包大模型流式语音识别 API 文档
// https://www.volcengine.com/docs/6561/1354869

const (
	wsURL       = "wss://openspeech.bytedance.com/api/v3/sauc/bigmodel_async"
	idleTimeout = 30 * time.Second
)

type Doubao struct {
	cfg *asr.Config
	log *log.Logger

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

func NewDoubao(log *log.Logger) *Doubao {
	return &Doubao{
		connectID: fmt.Sprintf("%d", time.Now().UnixNano()),
		log:       log,
	}
}

func (d *Doubao) SetConfig(cfg *asr.Config) *asr.Config {
	if cfg.Language == "" || cfg.Language == "zh" {
		cfg.Language = "zh-CN"
	}
	cfg.SampleRate = 16000 // 仅支持 16000
	if cfg.Format == "" {
		cfg.Format = "pcm"
	}
	if cfg.VadEos < 200 {
		cfg.VadEos = 800
	}
	d.cfg = cfg
	return d.cfg
}

func (d *Doubao) SetListener(listener asr.Listener) {
	d.listener = listener
}

func (d *Doubao) SendAudio(ctx context.Context, data []byte) error {
	// 使用锁检查状态
	d.lock.Lock()
	isRunning := d.isRunning
	d.lock.Unlock()

	if !isRunning {
		if err := d.initConnection(ctx); err != nil {
			return err
		}
	}

	// 检查是否有实际数据需要发送
	if len(data) > 0 && d.isRunning {
		// 直接发送音频数据
		err := d.sendAudioData(data, false)
		if err != nil {
			return err
		}
		d.sendDataCnt++
		if d.sendDataCnt%20 == 0 {
			d.log.Debugf("send audio data cnt: %d", d.sendDataCnt)
		}
	}
	return nil
}

// Protocol constants
const (
	clientFullRequest  = 0x1
	clientAudioRequest = 0x2
)

// Sequence types
const (
	noSequence  = 0x0
	negSequence = 0x2
)

// Serialization methods
const (
	noSerialization = 0x0
	jsonFormat      = 0x1
	gzipCompression = 0x1
)

// generateHeader 生成协议头
func (d *Doubao) generateHeader(messageType uint8, flags uint8, serializationMethod uint8) []byte {
	header := make([]byte, 4)
	header[0] = (1 << 4) | 1                                 // 协议版本(4位) + 头大小(4位)
	header[1] = (messageType << 4) | flags                   // 消息类型(4位) + 消息标志(4位)
	header[2] = (serializationMethod << 4) | gzipCompression // 序列化方法(4位) + 压缩方法(4位)
	header[3] = 0                                            // 保留字段
	return header
}

// constructRequest 构造请求数据
func (d *Doubao) constructRequest() map[string]any {
	return map[string]any{
		"user": map[string]any{
			"uid": d.connectID,
		},
		"audio": map[string]any{
			"format":   d.cfg.Format,     // pcm(pcm_s16le)/wav(pcm_s16le)
			"codec":    "raw",            // 默认raw音频格式，opus/raw
			"rate":     d.cfg.SampleRate, // 目前只有16000
			"bits":     16,
			"channel":  1,
			"language": d.cfg.Language, // Added language as per doc example
		},
		"request": map[string]any{
			"model_name":           "bigmodel", // 目前只有bigmodel
			"enable_itn":           true,
			"enable_punc":          d.cfg.EnablePunc,
			"enable_ddc":           false,
			"show_utterances":      true,         // 输出语音停顿、分句、分词信息，默认false
			"result_type":          "single",     // 默认为"full"，全量返回，设置为"single"则为增量结果返回，即不返回之前分句的结果
			"vad_segment_duration": 3000,         // 单位ms，默认为3000。当静音时间超过该值时，会将文本分为两个句子。不决定判停，所以不会修改definite出现的位置。在end_window_size配置后，该参数失效。
			"end_window_size":      d.cfg.VadEos, // 单位ms，默认为800，最小200。静音时长超过该值，会直接判停，输出definite。配置该值，不使用语义分句，根据静音时长来分句。用于实时性要求较高场景，可以提前获得definite句子
			"force_to_speech_time": 1000,         // 单位ms，默认为10000，最小1。音频时长超过该值之后，才会判停，根据静音时长输出definite，需配合end_window_size使用。用于解决短音频+实时性要求较高场景，不配置该参数，只使用end_window_size时，前10s不会判停。推荐设置1000，可能会影响识别准确率。
		},
	}
}

type serverResponse struct {
	Result struct {
		Text       string `json:"text"`
		Utterances []struct {
			Text     string `json:"text"`
			Definite bool   `json:"definite"`
		} `json:"utterances"`
	} `json:"result"`
}

type synResp struct {
	Code       int32
	ErrMsg     string
	Text       string // 识别文本
	IsLast     bool   // 是否已结束
	IsDefinite bool   // 是否明确是分句
}

func (d *Doubao) gzipCompress(input []byte) []byte {
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	_, _ = w.Write(input)
	_ = w.Close()
	return b.Bytes()
}

func (d *Doubao) gzipDecompress(input []byte) []byte {
	b := bytes.NewBuffer(input)
	r, _ := gzip.NewReader(b)
	out, _ := io.ReadAll(r)
	_ = r.Close()
	return out
}

// parseResponse 解析响应数据
func (d *Doubao) parseResponse(data []byte) (synResp, error) {
	headerSize := data[0] & 0x0f
	messageType := data[1] >> 4
	messageTypeSpecificFlags := data[1] & 0x0f
	messageCompression := data[2] & 0x0f
	messageSerializationMethod := data[2] >> 4

	payload := data[headerSize*4:]

	var resp synResp

	switch messageType {
	case 0x9: // full server response
		if messageTypeSpecificFlags != 0 {
			sequenceNumber := int32(binary.BigEndian.Uint32(payload[0:4]))
			payload = payload[8:]
			if sequenceNumber < 0 {
				resp.IsLast = true
			}
		} else {
			payload = payload[4:]
		}
	case 0xf: // 服务端处理错误时下发的消息类型
		resp.Code = int32(binary.BigEndian.Uint32(payload[:4]))
		payload = payload[8:]
		if messageCompression == 1 {
			payload = d.gzipDecompress(payload)
		}
		resp.ErrMsg = string(payload)
		return resp, nil
	}

	if len(payload) != 0 {
		if messageCompression == gzipCompression {
			payload = d.gzipDecompress(payload)
		}

		if messageSerializationMethod == 0x1 { // json格式
			var jsonData serverResponse
			if err := json.Unmarshal(payload, &jsonData); err != nil {
				return resp, fmt.Errorf("failed to parse the JSON response: %v", err)
			}
			resp.Text = jsonData.Result.Text
			if len(jsonData.Result.Utterances) != 0 {
				resp.IsDefinite = jsonData.Result.Utterances[0].Definite
			}
		} else if messageSerializationMethod != noSerialization { // 无序列化
			resp.Text = string(payload)
		}
	}
	return resp, nil
}

func (d *Doubao) initConnection(ctx context.Context) error {
	d.log.Info("start asr")
	d.startListenTime = time.Now()

	d.lock.Lock()
	defer d.lock.Unlock()

	if d.isRunning {
		return nil
	}

	// 确保旧连接已关闭
	if d.conn != nil {
		d.closeConnection()
	}

	// 建立WebSocket连接
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second, // 设置握手超时
	}
	header := make(http.Header)
	header.Add("X-Api-App-Key", d.cfg.AppID)
	header.Add("X-Api-Access-Key", d.cfg.AccessToken)
	header.Add("X-Api-Resource-Id", "volc.bigasr.sauc.duration") // 小时版：volc.bigasr.sauc.duration，并发版：volc.bigasr.sauc.concurrent
	header.Add("X-Api-Connect-Id", d.connectID)

	// 重试机制
	var (
		conn *websocket.Conn
		resp *http.Response
		err  error
	)
	maxRetries := 2 // 最大重试次数
	for i := 0; i < maxRetries; i++ {
		conn, resp, err = dialer.DialContext(ctx, wsURL, header)
		if err == nil {
			break
		}

		if i+1 < maxRetries {
			backoffTime := time.Duration(500*(i+1)) * time.Millisecond
			d.log.Warnf("failed to connect to the websocket, try %d/%d: %v, will try again %v", i+1, maxRetries+1, err, backoffTime)
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

	// 发送初始请求
	request := d.constructRequest()
	requestBytes, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("error unmarshaling task-started event message: %v", err)
	}

	compressedRequest := d.gzipCompress(requestBytes)
	headers := d.generateHeader(clientFullRequest, noSequence, jsonFormat)

	// 构造完整请求
	size := make([]byte, 4)
	binary.BigEndian.PutUint32(size, uint32(len(compressedRequest)))
	fullRequest := append(headers, size...)
	fullRequest = append(fullRequest, compressedRequest...)

	// 发送请求
	if err = conn.WriteMessage(websocket.BinaryMessage, fullRequest); err != nil {
		return fmt.Errorf("send fullRequest data failed: %v", err)
	}

	// 读取响应
	_, response, err := conn.ReadMessage()
	if err != nil {
		return fmt.Errorf("failed to read first message error: %v", err)
	}

	initialResult, err := d.parseResponse(response)
	if err != nil {
		return fmt.Errorf("the parsing response failed: %v", err)
	}

	// 检查初始响应状态
	if initialResult.Code != 0 && initialResult.Code != 20000000 {
		return fmt.Errorf("asr initialization error: %v", initialResult.ErrMsg)
	}

	d.conn = conn
	d.isRunning = true
	d.reqID = uuid.New().String()

	d.log.Debugf("init asr succeed, connect_id: %s, req_id: %s", d.connectID, d.reqID)

	go d.readMessage(ctx)
	return nil
}

func (d *Doubao) readMessage(ctx context.Context) {
	d.log.Info("doubao read message started")

	var isSentAsrStateCompleted bool // 是否已经发送的结束状态
	defer func() {
		if err := recover(); err != nil {
			d.log.Errorf("asr read goroutine panic: %v", err)
		}

		if !isSentAsrStateCompleted {
			d.listener.OnAsrResult(ctx, "", asr.StateCompleted)
		}

		d.lock.Lock()
		d.isRunning = false
		if d.conn != nil {
			d.closeConnection()
		}
		d.lock.Unlock()
		d.log.Info("doubao read message stopped")
	}()

	for {
		// 检查连接状态，避免在连接关闭后继续读取
		d.lock.Lock()
		if !d.isRunning || d.conn == nil {
			d.lock.Unlock()
			d.log.Info("recognition has ended or the connection has been closed, exiting the read loop")
			return
		}
		d.lock.Unlock()

		// _ = d.conn.SetReadDeadline(time.Now().Add(30 * time.Second))

		_, message, err := d.conn.ReadMessage()
		if err != nil {
			d.setErrorAndClose(fmt.Errorf("failed to read message: %v", err))
			return
		}

		result, err := d.parseResponse(message)
		if err != nil {
			d.setErrorAndClose(fmt.Errorf("response parsing failed: %v", err))
			return
		}

		if result.Code != 0 && result.Code != 20000000 {
			d.log.Infof("code field detected: parsing result = %v", result)
			d.setErrorAndClose(fmt.Errorf("ASR server side error: code = %d", result.Code))
			return
		}

		// 处理正常响应
		if result.Text == "" && !d.startListenTime.IsZero() && time.Since(d.startListenTime) > idleTimeout {
			d.silenceCount++
		} else if result.Text != "" {
			d.silenceCount = 0 // 重置静音计数
		}

		state := asr.StateProcessing
		if result.IsDefinite {
			state = asr.StateSentenceEnd
		}
		if result.IsLast {
			state = asr.StateCompleted
			isSentAsrStateCompleted = true
		}

		if finished := d.listener.OnAsrResult(ctx, result.Text, state); finished {
			return
		}
	}
}

func (d *Doubao) sendAudioData(data []byte, isLast bool) error {
	if len(data) == 0 && !isLast {
		return nil
	}

	defer func() {
		if err := recover(); err != nil {
			d.log.Errorf("asr send audio data panic: %v", err)
		}
	}()

	if d.conn == nil {
		return errors.New("connection not initialized")
	}

	var compressBuffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressBuffer)
	if _, err := gzipWriter.Write(data); err != nil {
		return fmt.Errorf("compress audio data failed: %v", err)
	}
	_ = gzipWriter.Close()

	compressedAudio := compressBuffer.Bytes()
	flags := uint8(0)
	if isLast {
		flags = negSequence
	}

	header := d.generateHeader(clientAudioRequest, flags, noSerialization)
	size := make([]byte, 4)
	binary.BigEndian.PutUint32(size, uint32(len(compressedAudio)))

	audioMessage := append(header, size...)
	audioMessage = append(audioMessage, compressedAudio...)

	if err := d.conn.WriteMessage(websocket.BinaryMessage, audioMessage); err != nil {
		return fmt.Errorf("send audio data failed: %v", err)
	}
	return nil
}

func (d *Doubao) setErrorAndClose(err error) {
	d.lock.Lock()
	defer d.lock.Unlock()

	d.isRunning = false

	if strings.Contains(err.Error(), "use of closed network connection") {
		d.log.Debugf("setErrorAndClose: %v, sendDataCnt=%d", err, d.sendDataCnt)
	} else {
		d.log.Errorf("setErrorAndClose: %v, sendDataCnt=%d", err, d.sendDataCnt)
	}

	if d.conn != nil {
		d.closeConnection()
	}
}

func (d *Doubao) closeConnection() {
	defer func() {
		if err := recover(); err != nil {
			d.log.Errorf("asr close error: %v", err)
		}
	}()

	if d.conn != nil {
		_ = d.conn.Close()
		d.conn = nil
	}
}

func (d *Doubao) GetSilenceCount() int {
	return d.silenceCount
}

func (d *Doubao) Reset() error {
	// 使用锁保护状态变更
	d.lock.Lock()
	defer d.lock.Unlock()

	d.isRunning = false
	d.closeConnection()

	d.silenceCount = 0
	d.sendDataCnt = 0
	d.taskID = ""

	d.log.Info("doubao reset")
	return nil
}
