package job

import (
	"context"
	"errors"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"golang.org/x/sync/errgroup"
)

var ErrUnknownSender = errors.New("no worker for this sender")

type Service interface {
	Submit(ctx context.Context, req JobRequest) (*Job, error)
	Start(ctx context.Context) error
}

type EVMService struct {
	chainID  uint64
	registry *JobRegistry
	workers  map[common.Address]Worker
}

// NewService builds an EVMService that routes jobs within one chain to the
// right wallet's worker.
func NewService(chainID uint64, workers map[common.Address]Worker, registry *JobRegistry) *EVMService {
	return &EVMService{chainID: chainID, workers: workers, registry: registry}
}

// Submit routes the request to the sender's worker, blocks until the first
// broadcast attempt completes, and returns the final Job snapshot.
func (s *EVMService) Submit(ctx context.Context, req JobRequest) (*Job, error) {
	worker, ok := s.workers[req.Sender]
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

// Start runs every worker in a sibling goroutine and blocks until ctx is
// cancelled or any worker returns an error on startup
func (s *EVMService) Start(ctx context.Context) error {
	group, ctx := errgroup.WithContext(ctx)
	for address, worker := range s.workers {
		group.Go(func() error {
			if err := worker.Start(ctx); err != nil {
				return fmt.Errorf("worker chain=%d sender=%s: %w", s.chainID, address.Hex(), err)
			}
			return nil
		})
	}
	return group.Wait()
}

// StartAll runs every per-chain Service in parallel. First worker init failure
// in any chain cancels the shared ctx and surfaces here.
func StartAll(ctx context.Context, services map[uint64]Service) error {
	group, ctx := errgroup.WithContext(ctx)
	for chainID, service := range services {
		group.Go(func() error {
			if err := service.Start(ctx); err != nil {
				return fmt.Errorf("chain %d: %w", chainID, err)
			}
			return nil
		})
	}
	return group.Wait()
}
