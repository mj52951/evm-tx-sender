package chain

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"

	"github.com/mj52951/evm-tx-sender/internal/config"
)

const dialTimeout = 15 * time.Second

type Client interface {
	ChainID() *big.Int
	PendingNonceAt(ctx context.Context, account common.Address) (uint64, error)
	SuggestGasTipCap(ctx context.Context) (*big.Int, error)
	HeaderByNumber(ctx context.Context, number *big.Int) (*types.Header, error)
	EstimateGas(ctx context.Context, msg ethereum.CallMsg) (uint64, error)
	SendTransaction(ctx context.Context, tx *types.Transaction) error
	TransactionReceipt(ctx context.Context, hash common.Hash) (*types.Receipt, error)
	Close()
}

type ethAdapter struct {
	*ethclient.Client
	chainID *big.Int
}

// New dials the RPC and verifies it reports the configured chain id.
func New(ctx context.Context, chainConfig config.ChainConfig) (Client, error) {
	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	client, err := ethclient.DialContext(dialCtx, chainConfig.RPCURL)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", chainConfig.RPCURL, err)
	}
	chainID, err := client.ChainID(dialCtx)
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("fetch chain id for %d: %w", chainConfig.ChainID, err)
	}
	if chainID.Uint64() != chainConfig.ChainID {
		client.Close()
		return nil, fmt.Errorf("chain id mismatch: config %d, rpc returned %s", chainConfig.ChainID, chainID.String())
	}
	return &ethAdapter{Client: client, chainID: chainID}, nil
}

// ChainID returns the cached chain id
func (a *ethAdapter) ChainID() *big.Int {
	return new(big.Int).Set(a.chainID)
}
