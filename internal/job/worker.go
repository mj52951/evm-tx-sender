package job

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"math/big"
	"slices"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/mj52951/evm-tx-sender/internal/chain"
	"github.com/mj52951/evm-tx-sender/internal/config"
	"github.com/mj52951/evm-tx-sender/internal/wallet"
)

// intakeBuffer is the capacity of each worker's submit channel
const intakeBuffer = 1024

var ErrBusy = errors.New("worker intake full")

type Worker interface {
	Submit(job *Job) error
	Start(ctx context.Context) error
}

type EVMWorker struct {
	executor *wallet.Executor
	client   chain.Client
	registry *JobRegistry
	cfg      config.WorkerConfig

	jobs      chan *Job
	nextNonce uint64
	inflight  map[uint64]string // nonce -> jobID
}

// NewWorker builds an EVMWorker bound to one (chain, wallet) pair.
func NewWorker(
	executor *wallet.Executor,
	client chain.Client,
	registry *JobRegistry,
	cfg config.WorkerConfig,
) *EVMWorker {
	return &EVMWorker{
		executor: executor,
		client:   client,
		registry: registry,
		cfg:      cfg,
		jobs:     make(chan *Job, intakeBuffer),
		inflight: make(map[uint64]string),
	}
}

// Submit queues the job for broadcast
func (w *EVMWorker) Submit(job *Job) error {
	select {
	case w.jobs <- job:
		return nil
	default:
		return ErrBusy
	}
}

// Start blocks until ctx is cancelled. Submits and ticks share a single
// goroutine so nonce assignment and the inflight map stay lock-free.
func (w *EVMWorker) Start(ctx context.Context) error {
	rpcCtx, cancel := context.WithTimeout(ctx, w.cfg.RPCTimeout)
	nonce, err := w.executor.PendingNonce(rpcCtx)
	cancel()
	if err != nil {
		return fmt.Errorf("init nonce: %w", err)
	}
	w.nextNonce = nonce
	slog.Info("worker started",
		"chain_id", w.executor.ChainID().Uint64(),
		"sender", w.executor.Address().Hex(),
		"nonce", nonce,
	)

	tick := time.NewTicker(w.cfg.TickInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case job := <-w.jobs:
			w.SubmitJob(ctx, job)
		case <-tick.C:
			w.UpdateInflight(ctx)
		}
	}
}

// SubmitJob runs the initial broadcast attempt and closes job.done on return.
func (w *EVMWorker) SubmitJob(ctx context.Context, job *Job) {
	defer close(job.done)
	log := slog.With("job_id", job.ID, "nonce", w.nextNonce)

	params := wallet.BroadcastParams{
		Nonce: w.nextNonce,
		To:    job.Request.To,
		Value: job.Request.Value,
		Data:  job.Request.Data,
	}
	opts := []wallet.TxOption{}
	if job.Request.Overrides.GasLimit != 0 {
		opts = append(opts, wallet.WithGasLimit(job.Request.Overrides.GasLimit))
	}
	if job.Request.Overrides.TipCap != nil || job.Request.Overrides.FeeCap != nil {
		opts = append(opts, wallet.WithDynamicFee(job.Request.Overrides.TipCap, job.Request.Overrides.FeeCap))
	}

	rpcCtx, cancel := context.WithTimeout(ctx, w.cfg.RPCTimeout)
	result, err := w.executor.Broadcast(rpcCtx, params, opts...)
	cancel()

	if err != nil {
		msg := err.Error()
		if strings.Contains(strings.ToLower(msg), "nonce") {
			log.Warn("nonce error on broadcast; resyncing", "error", msg)
			w.resyncNonce(ctx)
		} else {
			log.Error("broadcast failed", "error", msg)
		}
		w.registry.Update(job.ID, func(j *Job) {
			j.State = StateFailed
			j.Execution.ErrorMsg = msg
		})
		return
	}

	attempt := Attempt{
		Hash:     result.Hash,
		GasLimit: result.Fees.GasLimit,
		TipCap:   result.Fees.TipCap,
		FeeCap:   result.Fees.FeeCap,
		SentAt:   time.Now(),
	}
	w.registry.Update(job.ID, func(j *Job) {
		j.State = StateBroadcast
		j.Execution.Nonce = params.Nonce
		j.Execution.Attempts = append(j.Execution.Attempts, attempt)
	})
	w.inflight[params.Nonce] = job.ID
	w.nextNonce++
	log.Info("broadcast ok", "hash", result.Hash.Hex())
}

// UpdateInflight walks inflight in nonce order. All entries get receipt-polled;
// at most the lowest unminded entry is considered for a bump.
func (w *EVMWorker) UpdateInflight(ctx context.Context) {
	for _, nonce := range slices.Sorted(maps.Keys(w.inflight)) {
		jobID := w.inflight[nonce]
		job, ok := w.registry.Get(jobID)
		if !ok {
			slog.Warn("inflight job missing from registry", "nonce", nonce, "job_id", jobID)
			delete(w.inflight, nonce)
			continue
		}
		if len(job.Execution.Attempts) == 0 {
			continue
		}

		receipt := w.findReceipt(ctx, job.Execution.Attempts)
		if receipt == nil {
			w.tryBump(ctx, nonce, jobID, job)
			return
		}

		w.finalise(nonce, jobID, receipt)
	}
}

// findReceipt returns the first receipt found across the given attempts, or nil.
func (w *EVMWorker) findReceipt(ctx context.Context, attempts []Attempt) *types.Receipt {
	for _, attempt := range attempts {
		rpcCtx, cancel := context.WithTimeout(ctx, w.cfg.RPCTimeout)
		receipt, err := w.client.TransactionReceipt(rpcCtx, attempt.Hash)
		cancel()
		if err != nil && !errors.Is(err, ethereum.NotFound) {
			slog.Debug("receipt lookup failed", "hash", attempt.Hash.Hex(), "error", err)
			continue
		}
		if receipt != nil {
			return receipt
		}
	}
	return nil
}

// tryBump decides what to do with the lowest unminded entry: wait, bump, or
// give up
func (w *EVMWorker) tryBump(ctx context.Context, nonce uint64, jobID string, job *Job) {
	lastAttempt := job.Execution.Attempts[len(job.Execution.Attempts)-1]
	if time.Since(lastAttempt.SentAt) < w.cfg.ResendInterval {
		return
	}

	bumpCount := len(job.Execution.Attempts) - 1
	if bumpCount >= w.cfg.MaxBumps {
		w.markStuck(ctx, nonce, jobID)
		return
	}

	w.bumpInflight(ctx, job, lastAttempt)
}

// finalise transitions the job to mined/reverted and removes it from inflight.
func (w *EVMWorker) finalise(nonce uint64, jobID string, receipt *types.Receipt) {
	state := StateMined
	if receipt.Status == types.ReceiptStatusFailed {
		state = StateReverted
	}
	w.registry.Update(jobID, func(j *Job) {
		j.State = state
		j.Execution.Receipt = receipt
	})
	delete(w.inflight, nonce)
	slog.Info("job finalised",
		"job_id", jobID,
		"nonce", nonce,
		"state", string(state),
		"hash", receipt.TxHash.Hex(),
	)
}

// markStuck records the stuck state, drops the inflight entry, and resyncs the nonce.
func (w *EVMWorker) markStuck(ctx context.Context, nonce uint64, jobID string) {
	w.registry.Update(jobID, func(j *Job) {
		j.State = StateStuck
		j.Execution.ErrorMsg = fmt.Sprintf("exceeded %d bumps without receipt", w.cfg.MaxBumps)
	})
	delete(w.inflight, nonce)
	// ALERTING: wire a production alert on this error log. The MVP does not attempt
	// automated recovery (cancel-tx, escalating bumps, etc.)
	slog.Error("job stuck - WALLET REQUIRES INTERVENTION",
		"job_id", jobID,
		"nonce", nonce,
		"chain_id", w.executor.ChainID().Uint64(),
		"sender", w.executor.Address().Hex(),
	)
	w.resyncNonce(ctx)
}

// bumpInflight re-broadcasts the job with ceiling-bumped tip and fee caps.
func (w *EVMWorker) bumpInflight(ctx context.Context, job *Job, lastAttempt Attempt) {
	bumpCount := len(job.Execution.Attempts) - 1
	log := slog.With("job_id", job.ID, "nonce", job.Execution.Nonce, "bump", bumpCount+1)

	percent := big.NewInt(int64(w.cfg.BumpPercent))
	// +99 then /100 gives a ceiling divide so the result always clears
	// geth's +10% replacement floor in integer math.
	newTip := new(big.Int).Mul(lastAttempt.TipCap, percent)
	newTip.Add(newTip, big.NewInt(99)).Quo(newTip, big.NewInt(100))
	newFee := new(big.Int).Mul(lastAttempt.FeeCap, percent)
	newFee.Add(newFee, big.NewInt(99)).Quo(newFee, big.NewInt(100))

	params := wallet.BroadcastParams{
		Nonce: job.Execution.Nonce,
		To:    job.Request.To,
		Value: job.Request.Value,
		Data:  job.Request.Data,
	}
	opts := []wallet.TxOption{
		wallet.WithGasLimit(lastAttempt.GasLimit),
		wallet.WithDynamicFee(newTip, newFee),
	}
	rpcCtx, cancel := context.WithTimeout(ctx, w.cfg.RPCTimeout)
	result, err := w.executor.Broadcast(rpcCtx, params, opts...)
	cancel()

	if err != nil {
		log.Warn("replacement broadcast failed", "error", err)
		return
	}

	attempt := Attempt{
		Hash:     result.Hash,
		GasLimit: lastAttempt.GasLimit,
		TipCap:   newTip,
		FeeCap:   newFee,
		SentAt:   time.Now(),
	}
	w.registry.Update(job.ID, func(j *Job) {
		j.Execution.Attempts = append(j.Execution.Attempts, attempt)
	})
	log.Info("replacement broadcast ok", "hash", result.Hash.Hex())
}

// resyncNonce refreshes nextNonce from the chain. Logs on failure, no retry.
func (w *EVMWorker) resyncNonce(ctx context.Context) {
	rpcCtx, cancel := context.WithTimeout(ctx, w.cfg.RPCTimeout)
	nonce, err := w.executor.PendingNonce(rpcCtx)
	cancel()
	if err != nil {
		slog.Warn("nonce resync failed",
			"chain_id", w.executor.ChainID().Uint64(),
			"sender", w.executor.Address().Hex(),
			"error", err,
		)
		return
	}
	slog.Info("nonce resynced",
		"chain_id", w.executor.ChainID().Uint64(),
		"sender", w.executor.Address().Hex(),
		"old", w.nextNonce,
		"new", nonce,
	)
	w.nextNonce = nonce
}
