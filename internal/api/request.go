package api

import (
	"errors"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"

	"github.com/mj52951/evm-tx-sender/internal/sender"
	"github.com/mj52951/evm-tx-sender/internal/wallet"
)

type SubmitRequest struct {
	Network              uint64 `json:"network"`
	Sender               string `json:"sender"`
	To                   string `json:"to,omitempty"`
	Value                string `json:"value,omitempty"`
	Data                 string `json:"data,omitempty"`
	GasLimit             uint64 `json:"gasLimit,omitempty"`
	MaxFeePerGas         string `json:"maxFeePerGas,omitempty"`
	MaxPriorityFeePerGas string `json:"maxPriorityFeePerGas,omitempty"`
}

func (r SubmitRequest) ToJobRequest() (sender.JobRequest, error) {
	if r.Network == 0 {
		return sender.JobRequest{}, errors.New("network is required")
	}
	if !common.IsHexAddress(r.Sender) {
		return sender.JobRequest{}, fmt.Errorf("invalid sender address %q", r.Sender)
	}

	var to *common.Address
	if r.To != "" {
		if !common.IsHexAddress(r.To) {
			return sender.JobRequest{}, fmt.Errorf("invalid to address %q", r.To)
		}
		address := common.HexToAddress(r.To)
		to = &address
	}

	value := big.NewInt(0)
	if r.Value != "" {
		wei, err := parseBigInt(r.Value)
		if err != nil {
			return sender.JobRequest{}, fmt.Errorf("value: %w", err)
		}
		value = wei
	}

	var payload []byte
	if r.Data != "" {
		decoded, err := hexutil.Decode(r.Data)
		if err != nil {
			return sender.JobRequest{}, fmt.Errorf("invalid data hex: %w", err)
		}
		payload = decoded
	}

	var tipCap *big.Int
	if r.MaxPriorityFeePerGas != "" {
		wei, err := parseBigInt(r.MaxPriorityFeePerGas)
		if err != nil {
			return sender.JobRequest{}, fmt.Errorf("maxPriorityFeePerGas: %w", err)
		}
		tipCap = wei
	}

	var feeCap *big.Int
	if r.MaxFeePerGas != "" {
		wei, err := parseBigInt(r.MaxFeePerGas)
		if err != nil {
			return sender.JobRequest{}, fmt.Errorf("maxFeePerGas: %w", err)
		}
		feeCap = wei
	}

	return sender.JobRequest{
		ChainID: r.Network,
		Sender:  common.HexToAddress(r.Sender),
		To:      to,
		Value:   value,
		Data:    payload,
		Overrides: wallet.Fees{
			GasLimit: r.GasLimit,
			TipCap:   tipCap,
			FeeCap:   feeCap,
		},
	}, nil
}

func parseBigInt(raw string) (*big.Int, error) {
	number, ok := new(big.Int).SetString(raw, 10)
	if !ok || number.Sign() < 0 {
		return nil, fmt.Errorf("invalid number %q (expected non-negative decimal)", raw)
	}
	return number, nil
}
