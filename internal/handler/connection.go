package handler

import (
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

var (
	ErrConnectionClosed = errors.New("websocket connection is closed")
)

type Connection interface {
	ReadMessage() (messageType int, p []byte, err error)
	WriteMessage(messageType int, data []byte) error
	Close() error
	IsClosed() bool
}

type websocketConn struct {
	conn     *websocket.Conn
	lock     sync.Mutex
	isClosed int32 // 连接状态标记: 0:open, 1:closed; 使用原子操作降低开销
}

func newWebsocketConn(w http.ResponseWriter, r *http.Request) (*websocketConn, error) {
	upGrader := websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		CheckOrigin:     func(r *http.Request) bool { return true },
	}

	conn, err := upGrader.Upgrade(w, r, nil)
	if err != nil {
		return nil, err
	}
	return &websocketConn{conn: conn, isClosed: 0}, nil
}

func (w *websocketConn) ReadMessage() (messageType int, p []byte, err error) {
	// 检查连接状态
	if atomic.LoadInt32(&w.isClosed) == 1 {
		return 0, nil, ErrConnectionClosed
	}

	// 设置读取超时
	_ = w.conn.SetReadDeadline(time.Now().Add(time.Minute))

	messageType, p, err = w.conn.ReadMessage()
	if err != nil {
		// 读取出错时连接已关闭，因此将isClosed设置为已关闭
		atomic.StoreInt32(&w.isClosed, 1)
		return 0, nil, ErrConnectionClosed
	}

	return messageType, p, err
}

func (w *websocketConn) WriteMessage(messageType int, data []byte) error {
	// 检查连接状态
	if atomic.LoadInt32(&w.isClosed) == 1 {
		return ErrConnectionClosed
	}

	w.lock.Lock()
	defer w.lock.Unlock()

	// 再次检查连接是否关闭，避免在获取锁的过程中连接被关闭
	if atomic.LoadInt32(&w.isClosed) == 1 {
		return ErrConnectionClosed
	}

	// 设置写入超时时间
	_ = w.conn.SetWriteDeadline(time.Now().Add(30 * time.Second))

	err := w.conn.WriteMessage(messageType, data)
	if err != nil {
		// 如果读取出错，则标记为连接已关闭
		atomic.StoreInt32(&w.isClosed, 1)
		return ErrConnectionClosed
	}

	return nil
}

func (w *websocketConn) Close() error {
	// 原子操作避免重复关闭
	if !atomic.CompareAndSwapInt32(&w.isClosed, 0, 1) {
		return nil
	}

	w.lock.Lock()
	defer w.lock.Unlock()

	// 发送关闭帧
	closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "connection closed")
	_ = w.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_ = w.conn.WriteMessage(websocket.CloseMessage, closeMsg)

	return w.conn.Close()
}

func (w *websocketConn) IsClosed() bool {
	return atomic.LoadInt32(&w.isClosed) == 1
}
