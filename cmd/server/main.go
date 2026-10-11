package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/ethereum/go-ethereum/common"

	"github.com/mj52951/evm-tx-sender/internal/api"
	"github.com/mj52951/evm-tx-sender/internal/chain"
	"github.com/mj52951/evm-tx-sender/internal/config"
	"github.com/mj52951/evm-tx-sender/internal/job"
	txlog "github.com/mj52951/evm-tx-sender/internal/log"
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

	clients := make(map[uint64]chain.Client, len(chains))
	for chainID, chainCfg := range chains {
		client, err := chain.New(ctx, chainCfg)
		if err != nil {
			// ALERTING: make sure to alert the chain hasn't been properly start up
			slog.Warn("failed to dial chain, skipping", "chain_id", chainID, "error", err)
			continue
		}
		defer client.Close()
		clients[chainID] = client
	}

	registry := job.NewJobRegistry()
	services := make(map[uint64]job.Service, len(clients))
	for chainID, client := range clients {
		workers := make(map[common.Address]job.Worker, len(wallets))
		for _, signer := range wallets {
			executor := wallet.New(client, signer.PrivateKey)
			workers[signer.Address] = job.NewWorker(executor, client, registry, envCfg.WorkerCfg)
		}
		services[chainID] = job.NewService(chainID, workers, registry)
	}

	slog.Info("starting evm-tx-sender",
		"http_addr", envCfg.HTTPAddr,
		"chains", len(services),
		"wallets", len(wallets),
	)

	handler := api.New(services, registry)

	errCh := make(chan error, 2)
	go func() { errCh <- job.StartAll(ctx, services) }()
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
