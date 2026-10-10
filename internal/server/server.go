package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"
)

const (
	defaultReadTimeout   = 10 * time.Second
	defaultWriteTimeout  = 15 * time.Second
	defaultIdleTimeout   = 60 * time.Second
	defaultShutdownGrace = 10 * time.Second
)

// Start listens on addr with handler until ctx is cancelled. Returns nil on
// clean shutdown, or the first non-ErrServerClosed error.
func Start(ctx context.Context, addr string, handler http.Handler) error {
	srv := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  defaultReadTimeout,
		WriteTimeout: defaultWriteTimeout,
		IdleTimeout:  defaultIdleTimeout,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("api listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), defaultShutdownGrace)
	defer cancel()
	slog.Info("api shutting down")
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	slog.Info("api stopped")
	return nil
}
