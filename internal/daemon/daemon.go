package daemon

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"sync"
	"time"

	"github.com/Emirfs/conclave/internal/domain"
	"github.com/Emirfs/conclave/internal/store"
)

const maxOutputBytes = 64 * 1024

type Daemon struct {
	store   *store.Store
	workers int
	timeout time.Duration
}

func New(store *store.Store, workers int, timeout time.Duration) *Daemon {
	if workers < 1 {
		workers = 1
	}
	return &Daemon{store: store, workers: workers, timeout: timeout}
}

func (d *Daemon) Run(ctx context.Context) {
	var workers sync.WaitGroup
	for range d.workers {
		workers.Add(1)
		go func() {
			defer workers.Done()
			d.worker(ctx)
		}()
	}
	<-ctx.Done()
	workers.Wait()
}

func (d *Daemon) worker(ctx context.Context) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := d.runNext(ctx); err != nil && !errors.Is(err, context.Canceled) {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (d *Daemon) runNext(ctx context.Context) error {
	run, err := d.store.ClaimRun(ctx)
	if err != nil || run == nil {
		return err
	}
	persist := context.WithoutCancel(ctx)
	for _, stage := range run.Stages {
		if stage.Status == domain.StatusPassed {
			continue
		}
		if ctx.Err() != nil {
			_ = d.store.RequeueRun(persist, run.ID)
			return ctx.Err()
		}
		if err := d.store.SetStageRunning(persist, stage.ID); err != nil {
			_ = d.store.BlockRemaining(persist, run.ID)
			_ = d.store.FinishRun(persist, run.ID, domain.StatusFailed)
			return err
		}
		exitCode, output := d.execute(ctx, run.Project, stage.Command)
		if ctx.Err() != nil {
			_ = d.store.RequeueRun(persist, run.ID)
			return ctx.Err()
		}
		status := domain.StatusPassed
		if exitCode != 0 {
			status = domain.StatusFailed
		}
		if err := d.store.SetStageResult(persist, stage.ID, status, exitCode, output); err != nil {
			_ = d.store.BlockRemaining(persist, run.ID)
			_ = d.store.FinishRun(persist, run.ID, domain.StatusFailed)
			return err
		}
		if status == domain.StatusFailed {
			if err := d.store.BlockRemaining(persist, run.ID); err != nil {
				return err
			}
			return d.store.FinishRun(persist, run.ID, domain.StatusFailed)
		}
	}
	return d.store.FinishRun(persist, run.ID, domain.StatusPassed)
}

func (d *Daemon) execute(parent context.Context, directory string, command []string) (int, string) {
	if len(command) == 0 {
		return -1, "empty command"
	}
	ctx, cancel := context.WithTimeout(parent, d.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = directory
	cmd.WaitDelay = 10 * time.Second
	configureProcessTree(cmd)
	buffer := &limitedBuffer{limit: maxOutputBytes}
	cmd.Stdout = buffer
	cmd.Stderr = buffer
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return -1, buffer.String() + "\n[stage timed out]"
	}
	if err == nil {
		return 0, buffer.String()
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return exitError.ExitCode(), buffer.String()
	}
	return -1, buffer.String() + "\n" + err.Error()
}

type limitedBuffer struct {
	data      bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(value []byte) (int, error) {
	original := len(value)
	remaining := b.limit - b.data.Len()
	if remaining <= 0 {
		b.truncated = true
		return original, nil
	}
	if len(value) > remaining {
		value = value[:remaining]
		b.truncated = true
	}
	_, err := b.data.Write(value)
	return original, err
}

func (b *limitedBuffer) String() string {
	if b.truncated {
		return b.data.String() + "\n[output truncated]"
	}
	return b.data.String()
}
