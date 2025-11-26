package handler

import (
	"fmt"

	"github.com/gin-gonic/gin"

	"crow/internal/config"
	"crow/pkg/log"
)

type WebsocketServer struct {
	cfg *config.Config
	log *log.Logger
}

func NewWebsocketServer(cfg *config.Config, log *log.Logger) *WebsocketServer {
	return &WebsocketServer{
		cfg: cfg,
		log: log,
	}
}

func (w *WebsocketServer) Server(ctx *gin.Context) {
	conn, err := newWebsocketConn(ctx.Writer, ctx.Request)
	if err != nil {
		w.log.Errorf("failed to create websocket connection: %v", err)
		return
	}

	w.log.Infof("client %s connected", fmt.Sprintf("%p", conn))

	handler := NewHandler(w.cfg, w.log, conn)
	handler.Handle(ctx.Request.Context())
}
