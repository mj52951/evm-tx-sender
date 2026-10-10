package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/mj52951/evm-tx-sender/internal/config"
	txlog "github.com/mj52951/evm-tx-sender/internal/log"
	"github.com/mj52951/evm-tx-sender/internal/server"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	envCfg, err := config.LoadEnvConfig()
	if err != nil {
		return fmt.Errorf("load env config: %w", err)
	}

	txlog.Setup(envCfg.Log)

	chains, err := config.LoadChains(envCfg)
	if err != nil {
		return fmt.Errorf("load chains: %w", err)
	}
	wallets, err := config.LoadWallets(envCfg)
	if err != nil {
		return fmt.Errorf("load wallets: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx = txlog.WithLogger(ctx, slog.Default())

	slog.Info("starting evm-tx-sender",
		"http_addr", envCfg.HTTPAddr,
		"chains", len(chains),
		"wallets", len(wallets),
	)

	// Placeholder; real router is wired in a later commit.
	handler := http.NewServeMux()

	errCh := make(chan error, 1)
	go func() { errCh <- server.Start(ctx, envCfg.HTTPAddr, handler) }()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	select {
	case s := <-sig:
		slog.Info("shutdown signal received", "signal", s.String())
		cancel()
		if err := <-errCh; err != nil {
			return fmt.Errorf("http server: %w", err)
		}
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("http server: %w", err)
		}
	}

	slog.Info("evm-tx-sender stopped")
	return nil
}
