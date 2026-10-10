package sender_test

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mj52951/evm-tx-sender/internal/sender"
)

func TestJobRegistry_CreateAndGet(t *testing.T) {
	registry := sender.NewJobRegistry()
	job := sender.NewJob(sender.JobRequest{ChainID: 1, Value: big.NewInt(0)})
	registry.Create(job)

	got, ok := registry.Get(job.ID)
	require.True(t, ok)
	assert.Equal(t, job.ID, got.ID)

	_, ok = registry.Get("nonexistent")
	assert.False(t, ok)
}

func TestJobRegistry_UpdateBumpsTimestamp(t *testing.T) {
	registry := sender.NewJobRegistry()
	job := sender.NewJob(sender.JobRequest{ChainID: 1, Value: big.NewInt(0)})
	registry.Create(job)

	registry.Update(job.ID, func(j *sender.Job) {
		j.State = sender.StateMined
	})

	got, _ := registry.Get(job.ID)
	assert.Equal(t, sender.StateMined, got.State)
	assert.False(t, got.UpdatedAt.IsZero(), "UpdatedAt should be set after Update")
}

func TestJobRegistry_UpdateUnknownIDNoop(t *testing.T) {
	registry := sender.NewJobRegistry()
	// No panic, no state change.
	registry.Update("missing", func(j *sender.Job) {
		j.State = sender.StateMined
	})
}

