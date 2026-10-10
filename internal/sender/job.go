package sender

import (
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/google/uuid"

	"github.com/mj52951/evm-tx-sender/internal/wallet"
)

type JobState string

const (
	StateBroadcast JobState = "broadcast" // on the wire, waiting to mine
	StateMined     JobState = "mined"
	StateReverted  JobState = "reverted"
	StateFailed    JobState = "failed" // broadcast errored unrecoverably
	StateStuck     JobState = "stuck"  // bumps exhausted, no receipt
)

// JobRequest is the client-supplied part of a Job, immutable after creation.
type JobRequest struct {
	ChainID   uint64
	Sender    common.Address
	To        *common.Address
	Value     *big.Int
	Data      []byte
	Overrides wallet.Fees
}

// JobExecution captures the on-chain progress of a Job as the worker drives it.
type JobExecution struct {
	Nonce    uint64
	Attempts []Attempt
	Receipt  *types.Receipt
	ErrorMsg string
}

// Attempt records a single broadcast of a Job (initial or replacement). The
// full fee tuple is captured so the worker can reconstruct the bump base
// without a sibling in-memory structure.
type Attempt struct {
	Hash     common.Hash
	GasLimit uint64
	TipCap   *big.Int
	FeeCap   *big.Int
	SentAt   time.Time
}

type Job struct {
	ID        string
	State     JobState
	Request   JobRequest
	Execution JobExecution
	CreatedAt time.Time
	UpdatedAt time.Time

	// done is closed by the worker when the initial broadcast attempt returns.
	// Callers block on Done() to implement sync-broadcast HTTP semantics.
	done chan struct{}
}

// NewJob builds a Job with a fresh ID and signalling channel. The caller
// stores it in the registry and hands it to a Worker's Submit.
func NewJob(request JobRequest) *Job {
	return &Job{
		ID:        uuid.NewString(),
		Request:   request,
		CreatedAt: time.Now(),
		done:      make(chan struct{}),
	}
}

// Done returns a channel closed when the initial broadcast attempt completes
// (success or failure). Readers then fetch the final state via the registry.
func (j *Job) Done() <-chan struct{} { return j.done }
