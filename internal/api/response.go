package api

import (
	"time"

	"github.com/ethereum/go-ethereum/core/types"

	"github.com/mj52951/evm-tx-sender/internal/job"
)

type AttemptResponse struct {
	Hash     string    `json:"hash"`
	GasLimit uint64    `json:"gasLimit"`
	TipCap   string    `json:"tipCap"`
	FeeCap   string    `json:"feeCap"`
	SentAt   time.Time `json:"sentAt"`
}

type ReceiptResponse struct {
	BlockNumber       string `json:"blockNumber"`
	TxHash            string `json:"txHash"`
	Status            string `json:"status"`
	GasUsed           uint64 `json:"gasUsed"`
	EffectiveGasPrice string `json:"effectiveGasPrice"`
}

type JobResponse struct {
	JobID     string            `json:"jobId"`
	Status    string            `json:"status"`
	ChainID   uint64            `json:"chainId"`
	Sender    string            `json:"sender"`
	Nonce     uint64            `json:"nonce"`
	Attempts  []AttemptResponse `json:"attempts"`
	Receipt   *ReceiptResponse  `json:"receipt,omitempty"`
	Error     string            `json:"error,omitempty"`
	CreatedAt time.Time         `json:"createdAt"`
	UpdatedAt time.Time         `json:"updatedAt"`
}

func NewJobResponse(jobEntry *job.Job) JobResponse {
	attempts := make([]AttemptResponse, len(jobEntry.Execution.Attempts))
	for i, attempt := range jobEntry.Execution.Attempts {
		attempts[i] = AttemptResponse{
			Hash:     attempt.Hash.Hex(),
			GasLimit: attempt.GasLimit,
			TipCap:   attempt.TipCap.String(),
			FeeCap:   attempt.FeeCap.String(),
			SentAt:   attempt.SentAt,
		}
	}
	return JobResponse{
		JobID:     jobEntry.ID,
		Status:    string(jobEntry.State),
		ChainID:   jobEntry.Request.ChainID,
		Sender:    jobEntry.Request.Sender.Hex(),
		Nonce:     jobEntry.Execution.Nonce,
		Attempts:  attempts,
		Receipt:   newReceiptResponse(jobEntry.Execution.Receipt),
		Error:     jobEntry.Execution.ErrorMsg,
		CreatedAt: jobEntry.CreatedAt,
		UpdatedAt: jobEntry.UpdatedAt,
	}
}

func newReceiptResponse(receipt *types.Receipt) *ReceiptResponse {
	if receipt == nil {
		return nil
	}
	status := "success"
	if receipt.Status == types.ReceiptStatusFailed {
		status = "reverted"
	}
	return &ReceiptResponse{
		BlockNumber:       receipt.BlockNumber.String(),
		TxHash:            receipt.TxHash.Hex(),
		Status:            status,
		GasUsed:           receipt.GasUsed,
		EffectiveGasPrice: receipt.EffectiveGasPrice.String(),
	}
}
