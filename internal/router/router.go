package router

import (
	"github.com/gin-gonic/gin"

	"crow/internal/config"
	"crow/internal/router/handler"
	"crow/pkg/log"
)

func NewRouter(cfg *config.Config) *gin.Engine {
	gin.SetMode(cfg.Server.Mode)

	r := gin.Default()

	ws := handler.NewWebsocketServer(cfg, log.NewLogger(&log.Option{
		Hook:        nil,
		Mode:        cfg.Server.Mode,
		ServiceName: "crow",
		EncodeType:  log.EncodeTypeJson,
	}))
	r.GET("/crow/v1", ws.Server)
	return r
}
