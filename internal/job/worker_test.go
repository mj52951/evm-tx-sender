package job

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/mj52951/evm-tx-sender/internal/chain/mocks"
	"github.com/mj52951/evm-tx-sender/internal/config"
	"github.com/mj52951/evm-tx-sender/internal/wallet"
)

func newTestWorker(t *testing.T, client *mocks.MockClient) *EVMWorker {
	t.Helper()
	privKey, err := crypto.GenerateKey()
	require.NoError(t, err)
	executor := wallet.New(client, privKey)
	registry := NewJobRegistry()
	cfg := config.WorkerConfig{
		ResendInterval: 30 * time.Second,
		MaxBumps:       3,
		BumpPercent:    125,
		TickInterval:   time.Second,
		RPCTimeout:     10 * time.Second,
	}
	return NewWorker(executor, client, registry, cfg)
}

func TestWorker_SubmitJob(t *testing.T) {
	chainID := big.NewInt(10)
	to := common.HexToAddress("0x000000000000000000000000000000000000dead")

	tests := []struct {
		name         string
		prepareMocks func(*mocks.MockClient)
		initialNonce uint64
		wantState    JobState
		wantNonce    uint64 // worker.nextNonce after
		wantInflight bool
	}{
		{
			name: "happy path advances nonce and tracks inflight",
			prepareMocks: func(client *mocks.MockClient) {
				client.EXPECT().ChainID().Return(chainID).AnyTimes()
				client.EXPECT().SuggestGasTipCap(gomock.Any()).Return(big.NewInt(1_000_000_000), nil)
				client.EXPECT().HeaderByNumber(gomock.Any(), gomock.Nil()).
					Return(&types.Header{BaseFee: big.NewInt(100_000_000)}, nil)
				client.EXPECT().EstimateGas(gomock.Any(), gomock.Any()).Return(uint64(21_000), nil)
				client.EXPECT().SendTransaction(gomock.Any(), gomock.Any()).Return(nil)
			},
			initialNonce: 5,
			wantState:    StateBroadcast,
			wantNonce:    6,
			wantInflight: true,
		},
		{
			name: "nonce error marks failed and resyncs from node",
			prepareMocks: func(client *mocks.MockClient) {
				client.EXPECT().ChainID().Return(chainID).AnyTimes()
				client.EXPECT().SuggestGasTipCap(gomock.Any()).Return(big.NewInt(1), nil)
				client.EXPECT().HeaderByNumber(gomock.Any(), gomock.Nil()).
					Return(&types.Header{BaseFee: big.NewInt(1)}, nil)
				client.EXPECT().EstimateGas(gomock.Any(), gomock.Any()).Return(uint64(21_000), nil)
				client.EXPECT().SendTransaction(gomock.Any(), gomock.Any()).
					Return(errors.New("nonce too low"))
				client.EXPECT().PendingNonceAt(gomock.Any(), gomock.Any()).Return(uint64(7), nil)
			},
			initialNonce: 5,
			wantState:    StateFailed,
			wantNonce:    7,
		},
		{
			name: "generic broadcast error marks failed without touching nonce",
			prepareMocks: func(client *mocks.MockClient) {
				client.EXPECT().ChainID().Return(chainID).AnyTimes()
				client.EXPECT().SuggestGasTipCap(gomock.Any()).Return(big.NewInt(1), nil)
				client.EXPECT().HeaderByNumber(gomock.Any(), gomock.Nil()).
					Return(&types.Header{BaseFee: big.NewInt(1)}, nil)
				client.EXPECT().EstimateGas(gomock.Any(), gomock.Any()).Return(uint64(21_000), nil)
				client.EXPECT().SendTransaction(gomock.Any(), gomock.Any()).
					Return(errors.New("rpc exploded"))
			},
			initialNonce: 5,
			wantState:    StateFailed,
			wantNonce:    5,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			client := mocks.NewMockClient(ctrl)
			test.prepareMocks(client)

			worker := newTestWorker(t, client)
			worker.nextNonce = test.initialNonce

			job := NewJob(JobRequest{ChainID: 10, To: &to, Value: big.NewInt(0)})
			worker.registry.Create(job)

			worker.SubmitJob(context.Background(), job)

			final, ok := worker.registry.Get(job.ID)
			require.True(t, ok)
			assert.Equal(t, test.wantState, final.State)
			assert.Equal(t, test.wantNonce, worker.nextNonce)

			if test.wantInflight {
				assert.Equal(t, job.ID, worker.inflight[test.initialNonce])
				require.Len(t, final.Execution.Attempts, 1)
			} else {
				assert.Empty(t, worker.inflight)
			}

			select {
			case <-job.Done():
			case <-time.After(100 * time.Millisecond):
				t.Fatal("done channel not closed")
			}
		})
	}
}

func TestWorker_UpdateInflight(t *testing.T) {
	chainID := big.NewInt(10)
	to := common.HexToAddress("0x000000000000000000000000000000000000beef")
	minedHash := common.HexToHash("0xaa")
	stuckHash := common.HexToHash("0xbb")

	tests := []struct {
		name            string
		prepareMocks    func(*mocks.MockClient)
		seed            func(worker *EVMWorker, job *Job)
		wantState       JobState
		wantInflightLen int
		wantAttempts    int // on job after tick
	}{
		{
			name: "receipt success transitions to mined",
			prepareMocks: func(client *mocks.MockClient) {
				client.EXPECT().ChainID().Return(chainID).AnyTimes()
				client.EXPECT().TransactionReceipt(gomock.Any(), minedHash).Return(&types.Receipt{
					Status:      types.ReceiptStatusSuccessful,
					TxHash:      minedHash,
					BlockNumber: big.NewInt(100),
				}, nil)
			},
			seed: func(worker *EVMWorker, job *Job) {
				worker.inflight[42] = job.ID
				worker.registry.Update(job.ID, func(j *Job) {
					j.State = StateBroadcast
					j.Execution.Nonce = 42
					j.Execution.Attempts = []Attempt{{
						Hash: minedHash, GasLimit: 21_000,
						TipCap: big.NewInt(1), FeeCap: big.NewInt(100),
						SentAt: time.Now(),
					}}
				})
			},
			wantState:       StateMined,
			wantInflightLen: 0,
			wantAttempts:    1,
		},
		{
			name: "receipt failed transitions to reverted",
			prepareMocks: func(client *mocks.MockClient) {
				client.EXPECT().ChainID().Return(chainID).AnyTimes()
				client.EXPECT().TransactionReceipt(gomock.Any(), minedHash).Return(&types.Receipt{
					Status:      types.ReceiptStatusFailed,
					TxHash:      minedHash,
					BlockNumber: big.NewInt(100),
				}, nil)
			},
			seed: func(worker *EVMWorker, job *Job) {
				worker.inflight[42] = job.ID
				worker.registry.Update(job.ID, func(j *Job) {
					j.State = StateBroadcast
					j.Execution.Nonce = 42
					j.Execution.Attempts = []Attempt{{
						Hash: minedHash, GasLimit: 21_000,
						TipCap: big.NewInt(1), FeeCap: big.NewInt(100),
						SentAt: time.Now(),
					}}
				})
			},
			wantState:       StateReverted,
			wantInflightLen: 0,
		},
		{
			name: "stale entry under max bumps is bumped and gets a new attempt",
			prepareMocks: func(client *mocks.MockClient) {
				client.EXPECT().ChainID().Return(chainID).AnyTimes()
				client.EXPECT().TransactionReceipt(gomock.Any(), gomock.Any()).
					Return(nil, ethereum.NotFound)
				client.EXPECT().SendTransaction(gomock.Any(), gomock.Any()).Return(nil)
			},
			seed: func(worker *EVMWorker, job *Job) {
				worker.inflight[42] = job.ID
				worker.registry.Update(job.ID, func(j *Job) {
					j.State = StateBroadcast
					j.Execution.Nonce = 42
					j.Execution.Attempts = []Attempt{{
						Hash:     common.HexToHash("0xcc"),
						GasLimit: 21_000,
						TipCap:   big.NewInt(1_000_000_000),
						FeeCap:   big.NewInt(50_000_000_000),
						SentAt:   time.Now().Add(-time.Hour),
					}}
				})
			},
			wantState:       StateBroadcast,
			wantInflightLen: 1,
			wantAttempts:    2,
		},
		{
			name: "stale entry at max bumps is marked stuck and resynced",
			prepareMocks: func(client *mocks.MockClient) {
				client.EXPECT().ChainID().Return(chainID).AnyTimes()
				// findReceipt polls every attempt - with 4 attempts (initial + 3 bumps)
				// this gets called 4 times.
				client.EXPECT().TransactionReceipt(gomock.Any(), gomock.Any()).
					Return(nil, ethereum.NotFound).AnyTimes()
				client.EXPECT().PendingNonceAt(gomock.Any(), gomock.Any()).Return(uint64(43), nil)
			},
			seed: func(worker *EVMWorker, job *Job) {
				worker.inflight[42] = job.ID
				oldAttempt := Attempt{
					Hash:     stuckHash,
					GasLimit: 21_000,
					TipCap:   big.NewInt(1_000_000_000),
					FeeCap:   big.NewInt(50_000_000_000),
					SentAt:   time.Now().Add(-time.Hour),
				}
				worker.registry.Update(job.ID, func(j *Job) {
					j.State = StateBroadcast
					j.Execution.Nonce = 42
					// MaxBumps is 3 → attempts length 4 (initial + 3 bumps) = at the limit
					j.Execution.Attempts = []Attempt{oldAttempt, oldAttempt, oldAttempt, oldAttempt}
				})
			},
			wantState:       StateStuck,
			wantInflightLen: 0,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			client := mocks.NewMockClient(ctrl)
			test.prepareMocks(client)

			worker := newTestWorker(t, client)
			job := NewJob(JobRequest{ChainID: 10, To: &to, Value: big.NewInt(0)})
			worker.registry.Create(job)
			test.seed(worker, job)

			worker.UpdateInflight(context.Background())

			final, ok := worker.registry.Get(job.ID)
			require.True(t, ok)
			assert.Equal(t, test.wantState, final.State)
			assert.Len(t, worker.inflight, test.wantInflightLen)
			if test.wantAttempts > 0 {
				assert.Len(t, final.Execution.Attempts, test.wantAttempts)
			}
		})
	}
}
