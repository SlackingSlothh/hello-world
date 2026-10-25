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

	"hello-world/server"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	config, err := server.ReadConfig(os.Getenv)
	if err != nil {
		return err
	}
	application, err := server.New(config, server.Dependencies{})
	if err != nil {
		return err
	}
	if application.Authentication == nil {
		log.Print("authentication unavailable: database adapters have not been connected")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- application.HTTP.ListenAndServe()
	}()
	select {
	case err := <-serveErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := application.HTTP.Shutdown(shutdownContext); err != nil {
			_ = application.HTTP.Close()
			return err
		}
		return nil
	}
}
