package job

import (
	"sync"
	"time"
)

type JobRegistry struct {
	mu   sync.RWMutex
	jobs map[string]*Job
}

func NewJobRegistry() *JobRegistry {
	return &JobRegistry{
		jobs: make(map[string]*Job),
	}
}

func (r *JobRegistry) Create(job *Job) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.jobs[job.ID] = job
}

// Get returns a shallow copy of the Job so HTTP readers get a consistent
// snapshot without exposing the registry's internal pointer.
func (r *JobRegistry) Get(id string) (*Job, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	job, ok := r.jobs[id]
	if !ok {
		return nil, false
	}
	cloned := *job
	cloned.Execution.Attempts = append([]Attempt(nil), job.Execution.Attempts...)
	return &cloned, true
}

// Update applies mutate to the Job under the write lock and bumps UpdatedAt.
func (r *JobRegistry) Update(id string, mutate func(*Job)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	job, ok := r.jobs[id]
	if !ok {
		return
	}
	mutate(job)
	job.UpdatedAt = time.Now()
}

