package job_test

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mj52951/evm-tx-sender/internal/job"
)

func TestJobRegistry_CreateAndGet(t *testing.T) {
	registry := job.NewJobRegistry()
	created := job.NewJob(job.JobRequest{ChainID: 1, Value: big.NewInt(0)})
	registry.Create(created)

	got, ok := registry.Get(created.ID)
	require.True(t, ok)
	assert.Equal(t, created.ID, got.ID)

	_, ok = registry.Get("nonexistent")
	assert.False(t, ok)
}

func TestJobRegistry_UpdateBumpsTimestamp(t *testing.T) {
	registry := job.NewJobRegistry()
	created := job.NewJob(job.JobRequest{ChainID: 1, Value: big.NewInt(0)})
	registry.Create(created)

	registry.Update(created.ID, func(j *job.Job) {
		j.State = job.StateMined
	})

	got, _ := registry.Get(created.ID)
	assert.Equal(t, job.StateMined, got.State)
	assert.False(t, got.UpdatedAt.IsZero(), "UpdatedAt should be set after Update")
}

func TestJobRegistry_UpdateUnknownIDNoop(t *testing.T) {
	registry := job.NewJobRegistry()
	// No panic, no state change.
	registry.Update("missing", func(j *job.Job) {
		j.State = job.StateMined
	})
}
