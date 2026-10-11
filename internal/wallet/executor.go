package wallet

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/mj52951/evm-tx-sender/internal/chain"
)

const (
	tipBufferPct      = 120 // tipCap = SuggestGasTipCap * 1.2
	baseFeeBufferPct  = 200 // feeCap = BaseFee * 2
	gasLimitBufferPct = 130 // gasLimit = EstimateGas * 1.3
)

type Fees struct {
	GasLimit uint64
	TipCap   *big.Int
	FeeCap   *big.Int
}

type TxOption func(*Fees)

func WithGasLimit(limit uint64) TxOption {
	return func(fees *Fees) { fees.GasLimit = limit }
}

func WithDynamicFee(tipCap, feeCap *big.Int) TxOption {
	return func(fees *Fees) {
		fees.TipCap = tipCap
		fees.FeeCap = feeCap
	}
}

type BroadcastParams struct {
	Nonce uint64
	To    *common.Address // nil = contract creation
	Value *big.Int
	Data  []byte
}

type BroadcastResult struct {
	Hash common.Hash
	Fees Fees
}

type Executor struct {
	client  chain.Client
	privKey *ecdsa.PrivateKey
	sender  common.Address
}

func New(client chain.Client, privKey *ecdsa.PrivateKey) *Executor {
	return &Executor{
		client:  client,
		privKey: privKey,
		sender:  crypto.PubkeyToAddress(privKey.PublicKey),
	}
}

func (e *Executor) Address() common.Address { return e.sender }
func (e *Executor) ChainID() *big.Int       { return e.client.ChainID() }

func (e *Executor) PendingNonce(ctx context.Context) (uint64, error) {
	return e.client.PendingNonceAt(ctx, e.sender)
}

// Broadcast signs and sends a DynamicFeeTx. The returned hash is computed
// locally and is valid even if SendTransaction returned an error.
func (e *Executor) Broadcast(
	ctx context.Context,
	params BroadcastParams,
	opts ...TxOption,
) (BroadcastResult, error) {
	fees := Fees{}
	for _, opt := range opts {
		opt(&fees)
	}
	fees, err := e.applyFees(ctx, params, fees)
	if err != nil {
		return BroadcastResult{}, err
	}

	tx := types.NewTx(&types.DynamicFeeTx{
		Nonce:     params.Nonce,
		GasTipCap: fees.TipCap,
		GasFeeCap: fees.FeeCap,
		Gas:       fees.GasLimit,
		To:        params.To,
		Value:     params.Value,
		Data:      params.Data,
	})
	signedTx, err := types.SignTx(tx, types.NewCancunSigner(e.ChainID()), e.privKey)
	if err != nil {
		return BroadcastResult{}, fmt.Errorf("sign transaction: %w", err)
	}

	result := BroadcastResult{Hash: signedTx.Hash(), Fees: fees}
	if err := e.client.SendTransaction(ctx, signedTx); err != nil {
		return result, fmt.Errorf("send transaction: %w", err)
	}
	return result, nil
}

// applyFees fills any unset tip, fee cap or gas limit on fees by estimating
// them. Caller-provided values are preserved.
func (e *Executor) applyFees(ctx context.Context, params BroadcastParams, fees Fees) (Fees, error) {
	if fees.TipCap == nil || fees.FeeCap == nil {
		tipCap, feeCap, err := e.suggestFees(ctx)
		if err != nil {
			return Fees{}, err
		}
		if fees.TipCap == nil {
			fees.TipCap = tipCap
		}
		if fees.FeeCap == nil {
			fees.FeeCap = feeCap
		}
	}
	if fees.GasLimit == 0 {
		gasLimit, err := e.estimateGasLimit(ctx, params)
		if err != nil {
			return Fees{}, err
		}
		fees.GasLimit = gasLimit
	}
	return fees, nil
}

// suggestFees returns tip and fee caps with safety buffers applied.
// feeCap = baseFee * 2 + tipCap so the tx stays valid if base fee spikes.
func (e *Executor) suggestFees(ctx context.Context) (*big.Int, *big.Int, error) {
	tipCap, err := e.client.SuggestGasTipCap(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("suggest gas tip cap: %w", err)
	}
	tipCap = applyPct(tipCap, tipBufferPct)

	header, err := e.client.HeaderByNumber(ctx, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("fetch latest header: %w", err)
	}
	if header.BaseFee == nil {
		return nil, nil, errors.New("latest header has no base fee")
	}
	feeCap := applyPct(header.BaseFee, baseFeeBufferPct)
	feeCap.Add(feeCap, tipCap)

	return tipCap, feeCap, nil
}

// estimateGasLimit returns the estimated gas with a safety buffer applied.
func (e *Executor) estimateGasLimit(ctx context.Context, params BroadcastParams) (uint64, error) {
	estimated, err := e.client.EstimateGas(ctx, ethereum.CallMsg{
		From:  e.sender,
		To:    params.To,
		Value: params.Value,
		Data:  params.Data,
	})
	if err != nil {
		return 0, fmt.Errorf("estimate gas: %w", err)
	}
	return (estimated * gasLimitBufferPct) / 100, nil
}

func applyPct(value *big.Int, pct int) *big.Int {
	return new(big.Int).Div(
		new(big.Int).Mul(value, big.NewInt(int64(pct))),
		big.NewInt(100),
	)
}
