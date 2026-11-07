package main

import (
	"context"
	"errors"
	"image_upload/router"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	r := router.SetRouter()
	srv := &http.Server{
		Addr:    ":8080",
		Handler: r,
	}
	errCh := make(chan error, 1)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	select {
	case err := <-errCh:
		log.Fatalf("server error: %v", err)
	case <-ctx.Done():
		log.Printf("server shutdown")
	}
	shoutdown, stop := context.WithTimeout(context.Background(), time.Second*5)
	defer stop()
	if err := srv.Shutdown(shoutdown); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Printf("server shutdown error: %v", err)
		// 超时后强制关闭
		if closeErr := srv.Close(); closeErr != nil {
			log.Printf("force close failed: %v", closeErr)
		}
	}
	log.Println("server stopped")
}
