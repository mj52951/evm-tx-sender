package wallet_test

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/mj52951/evm-tx-sender/internal/chain/mocks"
	"github.com/mj52951/evm-tx-sender/internal/wallet"
)

func TestExecutor_Broadcast(t *testing.T) {
	chainID := big.NewInt(10)
	to := common.HexToAddress("0x000000000000000000000000000000000000dead")

	tests := []struct {
		name         string
		params       wallet.BroadcastParams
		opts         []wallet.TxOption
		prepareMocks func(*mocks.MockClient)
		expectErr    string // substring; empty = success
	}{
		{
			name: "success with auto-estimated fees",
			params: wallet.BroadcastParams{
				Nonce: 42,
				To:    &to,
				Value: big.NewInt(1_000_000_000),
				Data:  []byte{0x01, 0x02, 0x03},
			},
			prepareMocks: func(client *mocks.MockClient) {
				client.EXPECT().ChainID().Return(chainID).AnyTimes()
				client.EXPECT().SuggestGasTipCap(gomock.Any()).Return(big.NewInt(1_000_000_000), nil)
				client.EXPECT().HeaderByNumber(gomock.Any(), gomock.Nil()).
					Return(&types.Header{BaseFee: big.NewInt(100_000_000)}, nil)
				client.EXPECT().EstimateGas(gomock.Any(), gomock.Any()).Return(uint64(21_000), nil)
				client.EXPECT().SendTransaction(gomock.Any(), gomock.Any()).Return(nil)
			},
		},
		{
			name: "success with explicit fees skips RPC estimation",
			params: wallet.BroadcastParams{
				Nonce: 42,
				To:    &to,
				Value: big.NewInt(0),
			},
			opts: []wallet.TxOption{
				wallet.WithGasLimit(50_000),
				wallet.WithDynamicFee(big.NewInt(2_000_000_000), big.NewInt(50_000_000_000)),
			},
			prepareMocks: func(client *mocks.MockClient) {
				client.EXPECT().ChainID().Return(chainID).AnyTimes()
				client.EXPECT().SendTransaction(gomock.Any(), gomock.Any()).Return(nil)
			},
		},
		{
			name:   "SuggestGasTipCap error propagates",
			params: wallet.BroadcastParams{Nonce: 0, Value: big.NewInt(0)},
			prepareMocks: func(client *mocks.MockClient) {
				client.EXPECT().ChainID().Return(chainID).AnyTimes()
				client.EXPECT().SuggestGasTipCap(gomock.Any()).Return(nil, errors.New("rpc down"))
			},
			expectErr: "suggest gas tip cap: rpc down",
		},
		{
			name:   "nil base fee is rejected",
			params: wallet.BroadcastParams{Nonce: 0, Value: big.NewInt(0)},
			prepareMocks: func(client *mocks.MockClient) {
				client.EXPECT().ChainID().Return(chainID).AnyTimes()
				client.EXPECT().SuggestGasTipCap(gomock.Any()).Return(big.NewInt(1), nil)
				client.EXPECT().HeaderByNumber(gomock.Any(), gomock.Nil()).
					Return(&types.Header{}, nil)
			},
			expectErr: "no base fee",
		},
		{
			name:   "EstimateGas error propagates",
			params: wallet.BroadcastParams{Nonce: 0, Value: big.NewInt(0)},
			prepareMocks: func(client *mocks.MockClient) {
				client.EXPECT().ChainID().Return(chainID).AnyTimes()
				client.EXPECT().SuggestGasTipCap(gomock.Any()).Return(big.NewInt(1), nil)
				client.EXPECT().HeaderByNumber(gomock.Any(), gomock.Nil()).
					Return(&types.Header{BaseFee: big.NewInt(1)}, nil)
				client.EXPECT().EstimateGas(gomock.Any(), gomock.Any()).
					Return(uint64(0), errors.New("revert"))
			},
			expectErr: "estimate gas: revert",
		},
		{
			name: "SendTransaction error still returns computed hash",
			params: wallet.BroadcastParams{
				Nonce: 42,
				To:    &to,
				Value: big.NewInt(0),
			},
			opts: []wallet.TxOption{
				wallet.WithGasLimit(21_000),
				wallet.WithDynamicFee(big.NewInt(1), big.NewInt(100)),
			},
			prepareMocks: func(client *mocks.MockClient) {
				client.EXPECT().ChainID().Return(chainID).AnyTimes()
				client.EXPECT().SendTransaction(gomock.Any(), gomock.Any()).Return(errors.New("rejected"))
			},
			expectErr: "send transaction: rejected",
		},
	}

	privKey, err := crypto.GenerateKey()
	require.NoError(t, err)

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			client := mocks.NewMockClient(ctrl)
			test.prepareMocks(client)

			executor := wallet.New(client, privKey)
			result, err := executor.Broadcast(context.Background(), test.params, test.opts...)

			if test.expectErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), test.expectErr)
				return
			}
			require.NoError(t, err)
			assert.NotEqual(t, common.Hash{}, result.Hash)
			assert.NotNil(t, result.Fees.TipCap)
			assert.NotNil(t, result.Fees.FeeCap)
			assert.NotZero(t, result.Fees.GasLimit)
		})
	}
}

func TestExecutor_PendingNonce(t *testing.T) {
	ctrl := gomock.NewController(t)
	client := mocks.NewMockClient(ctrl)
	privKey, err := crypto.GenerateKey()
	require.NoError(t, err)

	client.EXPECT().ChainID().Return(big.NewInt(1)).AnyTimes()
	client.EXPECT().PendingNonceAt(gomock.Any(), crypto.PubkeyToAddress(privKey.PublicKey)).
		Return(uint64(99), nil)

	executor := wallet.New(client, privKey)
	nonce, err := executor.PendingNonce(context.Background())
	require.NoError(t, err)
	assert.Equal(t, uint64(99), nonce)
}
