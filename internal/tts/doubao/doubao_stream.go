package doubao

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"crow/internal/tts"
	"crow/pkg/log"
)

// 豆包双向流式websocket-V3-支持复刻2.0/混音mix WebSocket 接口
// https://www.volcengine.com/docs/6561/1329505
// 对接大语言模型时，推荐用此接口，不要额外增加切句或者攒句的逻辑，且更为自然，情绪更饱满

const (
	wsStreamURL = "wss://openspeech.bytedance.com/api/v3/tts/bidirection" // WebSocket服务端地址
)

type DoubaoStream struct {
	cfg *tts.Config
	log *log.Logger

	conn     *websocket.Conn
	listener tts.Listener

	lock sync.Mutex

	isRunning   bool
	sendDataCnt int
	connectID   string
	reqID       string
	taskID      string
	sessionID   string
}

func NewDoubaoStream(log *log.Logger) *DoubaoStream {
	return &DoubaoStream{
		log:       log,
		connectID: fmt.Sprintf("%d", time.Now().UnixNano()),
	}
}

func (d *DoubaoStream) SetConfig(cfg *tts.Config) *tts.Config {
	if cfg.Speaker == "" {
		cfg.Speaker = "zh_male_guangxiyuanzhou_moon_bigtts"
	}
	if cfg.Speed < -50 || cfg.Speed > 100 {
		cfg.Speed = 0
	}
	if cfg.Volume < -50 || cfg.Volume > 100 {
		cfg.Volume = 0
	}
	if cfg.Pitch < -12 || cfg.Pitch > 12 {
		cfg.Pitch = 0
	}
	if cfg.Format == "" {
		cfg.Format = "mp3"
	}
	if cfg.SampleRate <= 0 {
		cfg.SampleRate = 16000
	}
	d.cfg = cfg
	return cfg
}

func (d *DoubaoStream) SetListener(listener tts.Listener) {
	d.listener = listener
}

func (d *DoubaoStream) ToTTS(ctx context.Context, text string) error {
	d.lock.Lock()
	isRunning := d.isRunning
	d.lock.Unlock()

	if !isRunning {
		if err := d.initConnection(ctx); err != nil {
			return err
		}
	}

	if len(text) > 0 && d.isRunning {
		// 直接发送文本数据
		err := d.sendTextData(text)
		if err != nil {
			return err
		}
		d.sendDataCnt++
		if d.sendDataCnt%20 == 0 {
			d.log.Debugf("send text data cnt: %d", d.sendDataCnt)
		}
	}
	return nil
}

func (d *DoubaoStream) initConnection(ctx context.Context) error {
	d.log.Info("start tts")

	d.lock.Lock()
	defer d.lock.Unlock()

	if d.isRunning {
		return nil
	}

	if d.conn != nil {
		d.closeConnection()
	}

	header := make(http.Header)
	header.Add("X-Api-App-Key", d.cfg.AppID)
	header.Add("X-Api-Access-Key", d.cfg.Token)
	header.Add("X-Api-Resource-Id", d.cfg.ResourceID)
	header.Add("X-Api-Connect-Id", fmt.Sprintf("%d", time.Now().UnixNano()))

	var (
		conn *websocket.Conn
		resp *http.Response
		err  error
	)
	maxRetries := 2 // 最大重试次数
	for i := 0; i < maxRetries; i++ {
		dialer := websocket.DefaultDialer
		conn, resp, err = dialer.DialContext(ctx, wsStreamURL, header)
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

	// start connection
	if err = startConnection(conn); err != nil {
		return fmt.Errorf("start connection error: %v", err)
	}

	// wait connection
	_, err = waitForEvent(conn, MsgTypeFullServerResponse, EventType_ConnectionStarted)
	if err != nil {
		return fmt.Errorf("wait for connection started event error: %v", err)
	}

	payload := d.setupInput(int(EventType_StartSession), "")
	// start session
	sessionID := uuid.New().String()
	if err := startSession(conn, payload, sessionID); err != nil {
		return fmt.Errorf("start session error: %v", err)
	}
	// wait session started
	msg, err := waitForEvent(conn, MsgTypeFullServerResponse, EventType_SessionStarted)
	if err != nil {
		return fmt.Errorf("wait for session started event error: %v, msg: %v", err, msg)
	}

	d.conn = conn
	d.isRunning = true
	d.reqID = fmt.Sprintf("%d", time.Now().UnixNano())
	d.sessionID = sessionID

	d.log.Debugf("init tts succeed, connect_id: %s, req_id: %s, log_id: %s", d.connectID, d.reqID, resp.Header.Get("x-tt-logid"))

	go d.readMessage()
	return nil
}

func (d *DoubaoStream) setupInput(event int, text string) []byte {
	params := map[string]any{
		"user": map[string]any{
			"uid": uuid.New().String(),
		},
		"event":     event,
		"namespace": "BidirectionalTTS",
		"req_params": map[string]any{
			"text":    text,
			"speaker": d.cfg.Speaker,
			"audio_params": map[string]any{
				"format":           d.cfg.Format,     // mp3/ogg_opus/pcm
				"sample_rate":      d.cfg.SampleRate, // 8000, 16000, 22050, 24000, 32000, 44100, 48000
				"speech_rate":      d.cfg.Speed,      // 取值范围[-50,100]，100代表2.0倍速，-50代表0.5倍数
				"loudness_rate":    d.cfg.Volume,     // 取值范围[-50,100]，100代表2.0倍音量，-50代表0.5倍音量（mix音色暂不支持）
				"enable_timestamp": true,             // true 返回字与音素时间戳，默认false
			},
			"additions": func() string {
				str, _ := json.Marshal(map[string]any{
					"disable_markdown_filter":          true,  // 是否开启markdown解析过滤，为false时，不解析不过滤，例如，**你好**，会读为“星星‘你好’星星”，默认false
					"silence_duration":                 0,     // 设置该参数可在句尾增加静音时长，范围0~30000ms。（注：增加的句尾静音主要针对传入文本最后的句尾，而非每句话的句尾），默认0
					"enable_language_detector":         false, // 自动识别语种
					"disable_emoji_filter":             false, // 开启emoji表情在文本中不过滤显示，默认为false，建议搭配时间戳参数一起使用。
					"mute_cut_remain_ms":               "",    // 该参数需配合mute_cut_threshold参数一起使用，其中："mute_cut_threshold": "400", --静音判断的阈值（音量小于该值时判定为静音） "mute_cut_remain_ms": "50", --需要保留的静音长度 注：参数和value都为string格式
					"max_length_to_filter_parenthesis": 0,     // 是否过滤括号内的部分，0为不过滤，100为过滤
					"post_process": map[string]any{
						"pitch": int(d.cfg.Pitch), // 音调取值范围是[-12,12]
					},
				})
				return string(str)
			}(),
		},
	}
	resStr, _ := json.Marshal(params)
	return resStr
}

func (d *DoubaoStream) sendTextData(text string) error {
	d.log.Debugf("sendTextData: data=%s, sendDataCnt=%d", text, d.sendDataCnt)
	if text == "" {
		return nil
	}

	if d.conn == nil || !d.isRunning {
		return fmt.Errorf("tts connection is not running")
	}

	payload := d.setupInput(int(EventType_TaskRequest), text)
	// send task request
	if err := taskRequest(d.conn, payload, d.sessionID); err != nil {
		return fmt.Errorf("task request error: %v", err)
	}

	return nil
}

func (d *DoubaoStream) readMessage() {
	d.log.Info("cosy voice start read message")

	var isSentTtsStateCompleted bool // 是否已经发送的结束状态
	defer func() {
		if err := recover(); err != nil {
			d.log.Errorf("tts read message panic: %v", err)
		}
		if !isSentTtsStateCompleted {
			d.listener.OnTtsResult(nil, tts.StateCompleted)
		}
		d.lock.Lock()
		d.isRunning = false
		if d.conn != nil {
			d.closeConnection()
		}
		d.lock.Unlock()
		d.log.Info("cosy voice read message stopped")
	}()

	for {
		// 检查连接状态，避免在连接关闭后继续读取
		d.lock.Lock()
		if !d.isRunning || d.conn == nil {
			d.lock.Unlock()
			d.log.Info("流式识别已结束或连接已关闭，退出读取循环")
			return
		}
		conn := d.conn
		d.lock.Unlock()

		msgType, message, err := conn.ReadMessage()
		if err != nil {
			d.setErrorAndStop(err)
			return
		}

		if msgType != websocket.TextMessage && msgType != websocket.BinaryMessage {
			d.setErrorAndStop(fmt.Errorf("unexpected Websocket message type: %d", msgType))
			return
		}

		newMsg, err := newMessageFromBytes(message)
		if err != nil {
			d.setErrorAndStop(err)
			return
		}
		switch newMsg.MsgType {
		case MsgTypeFullServerResponse:
		case MsgTypeAudioOnlyServer:
			base64Message := base64.StdEncoding.EncodeToString(newMsg.Payload)
			if finished := d.listener.OnTtsResult([]byte(base64Message), tts.StateProcessing); finished {
				return
			}
		default:
			return
		}
		if newMsg.EventType == EventType_SessionFinished {
			isSentTtsStateCompleted = true
			if finished := d.listener.OnTtsResult(nil, tts.StateCompleted); finished {
				return
			}
			return
		}
	}
}

func (d *DoubaoStream) setErrorAndStop(err error) {
	d.lock.Lock()
	defer d.lock.Unlock()

	d.isRunning = false

	if strings.Contains(err.Error(), "use of closed network connection") {
		d.log.Debugf("setErrorAndStop: %v, sendDataCnt=%d", err, d.sendDataCnt)
	} else {
		d.log.Errorf("setErrorAndStop: %v, sendDataCnt=%d", err, d.sendDataCnt)
	}

	if d.conn != nil {
		d.closeConnection()
	}
}

func (d *DoubaoStream) ToSessionFinish() error {
	return nil
}

// 关闭连接
func (d *DoubaoStream) closeConnection() {
	defer func() {
		if r := recover(); r != nil {
			d.log.Errorf("tts close error: %v", r)
		}
	}()

	if d.conn == nil {
		return
	}

	// finish session
	if err := finishSession(d.conn, d.sessionID); err != nil {
		d.log.Errorf("finish session error: %v", err)
		return
	}

	// finish connection
	if err := finishConnection(d.conn); err != nil {
		d.log.Errorf("finish connect error: %v", err)
		return
	}
	// wait connection finished
	msg, err := waitForEvent(d.conn, MsgTypeFullServerResponse, EventType_ConnectionFinished)
	if err != nil {
		d.log.Errorf("wait finish connect error: %v, msg: %v", err, msg)
		return
	}

	_ = d.conn.Close()
	d.conn = nil
}

func (d *DoubaoStream) Reset() error {
	d.lock.Lock()
	defer d.lock.Unlock()

	d.isRunning = false
	d.closeConnection()

	d.taskID = ""
	d.sendDataCnt = 0

	d.log.Info("cosy voice reset")
	return nil
}
