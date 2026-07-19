package memory

import (
	"context"
	"time"
)

type Projector interface {
	ProjectNext(context.Context) (bool, error)
}

type Worker struct {
	projector Projector
	poll      time.Duration
}

func NewWorker(projector Projector, poll time.Duration) *Worker {
	return &Worker{projector: projector, poll: poll}
}

func (w *Worker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.poll)
	defer ticker.Stop()
	for {
		projected, err := w.projector.ProjectNext(ctx)
		if err != nil {
			return err
		}
		if projected {
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
