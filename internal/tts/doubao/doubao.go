package doubao

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"crow/internal/tts"
	"crow/pkg/log"
)

// 豆包语音合成大模型 WebSocket 接口
// https://www.volcengine.com/docs/6561/1257584

const (
	wsURL = "wss://openspeech.bytedance.com/api/v1/tts/ws_binary" // WebSocket服务端地址
)

var splitPunctuation = map[rune]bool{',': true, '.': true, '!': true, '?': true, ';': true, ':': true, '，': true, '。': true, '！': true, '？': true, '；': true, '：': true}

type Doubao struct {
	cfg *tts.Config
	log *log.Logger

	listener tts.Listener

	connectID string
	reqID     string

	text string
}

func NewDoubao(log *log.Logger) *Doubao {
	return &Doubao{
		log:       log,
		connectID: fmt.Sprintf("%d", time.Now().UnixNano()),
	}
}

func (d *Doubao) SetConfig(cfg *tts.Config) *tts.Config {
	if cfg.Speaker == "" {
		cfg.Speaker = "zh_male_guangxiyuanzhou_moon_bigtts"
	}
	if cfg.Speed < 0.2 || cfg.Speed > 3.0 {
		cfg.Speed = 1.0
	}
	if cfg.Volume < 0 || cfg.Volume > 100 {
		cfg.Volume = 50
	}
	if cfg.Pitch < 0.1 || cfg.Pitch > 3.0 {
		cfg.Pitch = 1.0
	}
	if cfg.Format == "" {
		cfg.Format = "mp3"
	}
	if cfg.SampleRate < 8000 || cfg.SampleRate > 24000 {
		cfg.SampleRate = 16000
	}
	d.cfg = cfg
	if d.cfg.Volume < 5 {
		d.cfg.Volume = 5
	}
	d.cfg.Volume /= 50 // 豆包音量范围是[0.1~3.0]，而我们的音量范围是0-100，最大按2处理，
	return cfg
}

func (d *Doubao) SetListener(listener tts.Listener) {
	d.listener = listener
}

func (d *Doubao) ToTTS(ctx context.Context, text string) error {
	if text == "" {
		return nil
	}
	// 拼接文本，按标点分割语句后再进行tts
	var builder strings.Builder
	for _, v := range []rune(text) {
		builder.WriteRune(v)
		if splitPunctuation[v] {
			d.text += builder.String()
			_ = d.sendMessage(ctx, d.text)
			d.text = ""
			builder.Reset()
		}
	}
	d.text += builder.String()
	return nil
}

// version: b0001 (4 bits)
// header size: b0001 (4 bits)
// message type: b0001 (Full client request) (4bits)
// message type specific flags: b0000 (none) (4bits)
// message serialization method: b0001 (JSON) (4 bits)
// message compression: b0001 (gzip) (4bits)
// reserved data: 0x00 (1 byte)
var defaultHeader = []byte{0x11, 0x10, 0x11, 0x00}

type serverResponse struct {
	Reqid    string `json:"reqid"`    // 请求ID，与传入的参数中reqid一致
	Code     int    `json:"code"`     // 请求状态码
	Message  string `json:"message"`  // 请求状态信息，错误信息
	Sequence int    `json:"sequence"` // 音频段序号，负数表示合成完毕
	Data     string `json:"data"`     // 合成音频，base64编码
	Addition struct {
		Duration string `json:"duration"` // 音频时长，返回音频的长度，单位 ms
	} `json:"addition"` // 额外信息
}

type synResp struct {
	Code   int
	ErrMsg string
	Audio  []byte
	IsLast bool
}

func (d *Doubao) setupInput(text string) []byte {
	params := map[string]any{
		"app": map[string]any{
			"appid":   d.cfg.AppID,
			"token":   d.cfg.Token,
			"cluster": d.cfg.Cluster,
		},
		"user": map[string]any{
			"uid": d.connectID,
		},
		"audio": map[string]any{
			"voice_type":        d.cfg.Speaker,
			"encoding":          d.cfg.Format,
			"speed_ratio":       d.cfg.Speed,
			"loudness_ratio":    d.cfg.Volume,
			"pitch_ratio":       d.cfg.Pitch,
			"rate":              d.cfg.SampleRate,
			"explicit_language": "zh",
		},
		"request": map[string]any{
			"reqid": d.reqID,
			"text":  text,
			// "text_type": "plain",
			"operation": "submit", // submit 流式，query 非流式(一次性合成)
		},
	}
	resStr, _ := json.Marshal(params)
	return resStr
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

func (d *Doubao) parseResponse(res []byte) (synResp, error) {
	headSize := res[0] & 0x0f
	messageType := res[1] >> 4
	messageTypeSpecificFlags := res[1] & 0x0f
	messageCompression := res[2] & 0x0f
	messageSerializationMethod := res[2] >> 4

	payload := res[headSize*4:]

	var resp synResp
	switch messageType {
	case 0xb: // audio-only server response
		// no sequence number as ACK
		if messageTypeSpecificFlags != 0 {
			sequenceNumber := int32(binary.BigEndian.Uint32(payload[0:4]))
			payload = payload[8:]
			if sequenceNumber < 0 {
				resp.IsLast = true
			}
		} else {
			payload = payload[4:]
		}
	case 0xf: // error message from server
		code := int32(binary.BigEndian.Uint32(payload[0:4]))
		errMsg := payload[8:]
		if messageCompression == 1 {
			errMsg = d.gzipDecompress(errMsg)
		}
		return resp, fmt.Errorf("error code: %d, msg: %s", code, string(errMsg))
	default:
		return resp, errors.New("wrong message type")
	}

	if len(payload) != 0 {
		if messageCompression == 0x1 { // 经过gzip压缩的消息
			payload = d.gzipDecompress(payload)
		}

		if messageSerializationMethod == 0x1 { // json格式
			var jsonData serverResponse
			if err := json.Unmarshal(payload, &jsonData); err != nil {
				return resp, fmt.Errorf("failed to parse the JSON response: %v", err)
			}
			resp.Code = jsonData.Code
			resp.ErrMsg = jsonData.Message
			resp.IsLast = jsonData.Sequence < 0
			resp.Audio = append(resp.Audio, []byte(jsonData.Data)...)
		} else if messageSerializationMethod == 0x0 { // 无序列化
			audio := base64.StdEncoding.EncodeToString(payload)
			resp.Audio = append(resp.Audio, audio...)
		}
	}

	return resp, nil
}

func (d *Doubao) sendMessage(ctx context.Context, text string) error {
	d.log.Info("start tts")

	header := make(http.Header)
	header.Add("Authorization", fmt.Sprintf("Bearer;%s", d.cfg.Token))

	input := d.setupInput(text)
	input = d.gzipCompress(input)
	payloadSize := len(input)
	payloadArr := make([]byte, 4)
	binary.BigEndian.PutUint32(payloadArr, uint32(payloadSize))
	clientRequest := make([]byte, len(defaultHeader))
	copy(clientRequest, defaultHeader)
	clientRequest = append(clientRequest, payloadArr...)
	clientRequest = append(clientRequest, input...)

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

	if err = conn.WriteMessage(websocket.BinaryMessage, clientRequest); err != nil {
		return fmt.Errorf("failed to send client request: %v", err)
	}

	d.reqID = uuid.New().String()
	d.log.Debugf("init tts succeed, connect_id: %s, req_id: %s", d.connectID, d.reqID)

	d.readMessage(conn)
	return nil
}

func (d *Doubao) readMessage(conn *websocket.Conn) {
	d.log.Info("doubao tts start read message")

	defer func() {
		if err := recover(); err != nil {
			d.log.Errorf("tts read message panic: %v", err)
		}
		d.log.Info("doubao tts read message stopped")
	}()

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			d.setErrorAndClose(conn, err)
			return
		}

		result, err := d.parseResponse(message)
		if err != nil {
			d.setErrorAndClose(conn, err)
			return
		}

		if result.Code != 0 && result.Code != 3000 {
			d.log.Infof("code field detected: parsing result = %v", result)
			d.setErrorAndClose(conn, fmt.Errorf("TTS server side error: code = %d", result.Code))
			return
		}

		state := tts.StateProcessing
		if result.IsLast {
			state = tts.StateCompleted
		}

		if finished := d.listener.OnTtsResult(result.Audio, state); finished {
			return
		}
	}
}

func (d *Doubao) setErrorAndClose(conn *websocket.Conn, err error) {
	if strings.Contains(err.Error(), "use of closed network connection") {
		d.log.Debugf("setErrorAndStop: %v", err)
	} else {
		d.log.Errorf("setErrorAndStop: %v", err)
	}

	d.closeConnection(conn)
}

func (d *Doubao) ToSessionFinish() error {
	// 如果还有文本没有发送，需要将剩余的文本继续发送
	if d.text != "" {
		_ = d.sendMessage(context.Background(), d.text)
	}
	return nil
}

func (d *Doubao) closeConnection(conn *websocket.Conn) {
	defer func() {
		if r := recover(); r != nil {
			d.log.Errorf("tts close error: %v", r)
		}
	}()

	if conn != nil {
		_ = conn.Close()
	}
}

func (d *Doubao) Reset() error {
	return nil
}
