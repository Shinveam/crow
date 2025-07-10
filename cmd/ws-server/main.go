package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"crow/internal/config"
	"crow/internal/router"
)

func main() {
	cfg := config.NewConfig()
	if cfg == nil {
		panic("failed to load config")
	}

	r := router.NewRouter(cfg)
	s := http.Server{
		Addr:           cfg.Server.IP + ":" + cfg.Server.Port,
		Handler:        r,
		MaxHeaderBytes: 1 << 20,
	}

	go func() {
		if err := s.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("s.ListenAndServe err: %v", err)
		}
	}()

	quit := make(chan os.Signal)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM) // 接收系统信号量
	<-quit
	log.Println("shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		log.Fatal("server forced to shutdown:", err)
	}

	log.Println("server exiting")
}
