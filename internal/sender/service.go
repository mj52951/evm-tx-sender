package sender

import (
	"context"
	"errors"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"golang.org/x/sync/errgroup"
)

var ErrUnknownSender = errors.New("no worker for the given chain and sender")

type WorkerKey struct {
	ChainID uint64
	Sender  common.Address
}

type Service struct {
	registry *JobRegistry
	workers  map[WorkerKey]*Worker
}

func NewService(workers map[WorkerKey]*Worker, registry *JobRegistry) *Service {
	return &Service{registry: registry, workers: workers}
}

// Submit routes the request to its (chain, sender) worker, blocks until the
// first broadcast attempt completes, and returns the final Job snapshot.
func (s *Service) Submit(ctx context.Context, req JobRequest) (*Job, error) {
	worker, ok := s.workers[WorkerKey{ChainID: req.ChainID, Sender: req.Sender}]
	if !ok {
		return nil, ErrUnknownSender
	}

	job := NewJob(req)
	s.registry.Create(job)

	if err := worker.Submit(job); err != nil {
		return nil, err
	}

	select {
	case <-job.Done():
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	final, _ := s.registry.Get(job.ID)
	return final, nil
}

// Get returns a snapshot of the Job with the given id.
func (s *Service) Get(id string) (*Job, bool) {
	return s.registry.Get(id)
}

// Start runs every worker in a sibling goroutine and blocks until ctx is
// cancelled or any worker returns an error (fail-fast).
func (s *Service) Start(ctx context.Context) error {
	group, ctx := errgroup.WithContext(ctx)
	for key, worker := range s.workers {
		group.Go(func() error {
			if err := worker.Start(ctx); err != nil {
				return fmt.Errorf("worker chain=%d sender=%s: %w", key.ChainID, key.Sender.Hex(), err)
			}
			return nil
		})
	}
	return group.Wait()
}
