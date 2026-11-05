package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"
)

const (
	serverHeaderTimeout = 5 * time.Second
	serverIdleTimeout   = 30 * time.Second
	responseWriteBudget = 5 * time.Second
)

// newHTTPServer bounds idle connections and gives a shared refresh time to finish
// before timing out response writes. Callers still need a total request deadline.
func newHTTPServer(addr string, handler http.Handler, refreshTimeout time.Duration) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: serverHeaderTimeout,
		ReadTimeout:       serverHeaderTimeout,
		IdleTimeout:       serverIdleTimeout,
		WriteTimeout:      responseWriteTimeout(refreshTimeout),
	}
}

func responseWriteTimeout(refreshTimeout time.Duration) time.Duration {
	const maxDuration = time.Duration(1<<63 - 1)
	if refreshTimeout <= maxDuration-responseWriteBudget {
		return refreshTimeout + responseWriteBudget
	}
	return maxDuration
}

// serveUntilShutdown stops accepting requests on cancellation and gives active
// HTTP handlers their configured drain budget. At the deadline, Close cancels
// their request contexts; detached credential workers retain their own budget.
func serveUntilShutdown(ctx context.Context, srv *http.Server, listener net.Listener, timeout time.Duration) error {
	defer func() { _ = listener.Close() }()
	served := make(chan error, 1)
	go func() { served <- srv.Serve(listener) }()
	select {
	case err := <-served:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		log.Print("shutdown signal received, draining...")
		drainCtx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if err := srv.Shutdown(drainCtx); err != nil {
			_ = srv.Close()
			return fmt.Errorf("graceful shutdown: %w", err)
		}
		return nil
	}
}
