package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/mj52951/evm-tx-sender/internal/chain"
	"github.com/mj52951/evm-tx-sender/internal/config"
	txlog "github.com/mj52951/evm-tx-sender/internal/log"
	"github.com/mj52951/evm-tx-sender/internal/sender"
	"github.com/mj52951/evm-tx-sender/internal/server"
	"github.com/mj52951/evm-tx-sender/internal/wallet"
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

	// Dial every chain client up-front. A chain whose dial fails is skipped
	// and the service continues on the rest.
	clients := make(map[uint64]chain.Client, len(chains))
	for chainID, chainCfg := range chains {
		client, err := chain.New(ctx, chainCfg)
		if err != nil {
			// ALERTING: wire a production alert on this warn log so operators
			// know a chain is unreachable and the service is running degraded.
			slog.Warn("failed to dial chain, skipping", "chain_id", chainID, "error", err)
			continue
		}
		defer client.Close()
		clients[chainID] = client
	}

	registry := sender.NewJobRegistry()
	workers := make(map[sender.WorkerKey]*sender.Worker, len(clients)*len(wallets))
	for chainID, client := range clients {
		for _, signer := range wallets {
			executor := wallet.New(client, signer.PrivateKey)
			workers[sender.WorkerKey{ChainID: chainID, Sender: signer.Address}] = sender.NewWorker(
				executor, client, registry, envCfg.Sender,
			)
		}
	}
	service := sender.NewService(workers, registry)

	slog.Info("starting evm-tx-sender",
		"http_addr", envCfg.HTTPAddr,
		"chains", len(clients),
		"wallets", len(wallets),
		"workers", len(workers),
	)

	// Placeholder; real router is wired in a later commit.
	handler := http.NewServeMux()
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	errCh := make(chan error, 2)
	go func() { errCh <- service.Start(ctx) }()
	go func() { errCh <- server.Start(ctx, envCfg.HTTPAddr, handler) }()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	select {
	case received := <-sig:
		slog.Info("shutdown signal received", "signal", received.String())
		cancel()
	case err := <-errCh:
		cancel()
		if err != nil {
			return fmt.Errorf("component failed: %w", err)
		}
	}

	slog.Info("evm-tx-sender stopped")
	return nil
}
