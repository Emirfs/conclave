package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Emirfs/conclave/internal/domain"
	"github.com/Emirfs/conclave/internal/store"
)

// loopPoll is how often a worker looks for a card whose cycle is due. A cycle
// interval is measured from the end of the previous run, so this only bounds
// how quickly a due card is noticed.
const loopPoll = 500 * time.Millisecond

// loopWorker runs card cycles: ordered steps against a project, repeating
// according to the card's mode. This is the same execution path pipelines use,
// so a step can be anything on the machine — a flasher, a serial listener, a
// test runner.
func (d *Daemon) loopWorker(ctx context.Context) {
	ticker := time.NewTicker(loopPoll)
	defer ticker.Stop()
	for {
		if err := d.runNextLoop(ctx); err != nil && !errors.Is(err, context.Canceled) {
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

func (d *Daemon) runNextLoop(ctx context.Context) error {
	job, err := d.store.ClaimLoopRun(ctx)
	if err != nil || job == nil {
		return err
	}
	persist := context.WithoutCancel(ctx)
	started := time.Now().UTC().Format(time.RFC3339Nano)

	run, signature := d.runCycle(ctx, job)
	run.StartedAt = started
	if ctx.Err() != nil {
		// Shutting down: leave the card armed so it resumes on restart.
		return ctx.Err()
	}

	fresh, err := d.store.FinishLoopRun(persist, job, run, signature)
	if err != nil {
		return err
	}
	// Only a new failure is worth interrupting the card with. A cycle that
	// keeps failing the same way would otherwise post a message every pass.
	if fresh && job.Notify {
		return d.store.CreateLoopFailureTurn(persist, job.ConversationID, failurePrompt(job, run))
	}
	return nil
}

// runCycle executes every step in order and stops at the first failure, the
// way a pipeline does. The returned signature identifies this outcome so a
// repeat of the same failure can be recognised.
func (d *Daemon) runCycle(ctx context.Context, job *store.LoopJob) (domain.CardRun, string) {
	for _, step := range job.Steps {
		command := splitCommand(step.Command)
		if len(command) == 0 {
			continue
		}
		exitCode, output := d.executeStep(ctx, job.Project, command, step.TimeoutSeconds)
		if ctx.Err() != nil {
			return domain.CardRun{Status: domain.StatusBlocked, StepName: step.Name}, ""
		}
		if exitCode != 0 {
			run := domain.CardRun{
				Status:   domain.StatusFailed,
				StepName: stepLabel(step),
				ExitCode: exitCode,
				Output:   output,
			}
			return run, failureSignature(run)
		}
	}
	return domain.CardRun{Status: domain.StatusPassed}, "passed"
}

// executeStep runs one step, honouring its own timeout when it sets one. A
// serial listener never exits on its own, so its timeout is what ends it.
func (d *Daemon) executeStep(parent context.Context, project string, command []string, timeoutSeconds int) (int, string) {
	if timeoutSeconds <= 0 {
		return d.execute(parent, project, command)
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(timeoutSeconds)*time.Second)
	defer cancel()
	return d.execute(ctx, project, command)
}

func stepLabel(step domain.CardStep) string {
	if step.Name != "" {
		return step.Name
	}
	return step.Command
}

// failureSignature collapses an outcome into a short, stable fingerprint. The
// output is included so a different error is treated as a different failure,
// but only its hash is kept.
func failureSignature(run domain.CardRun) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%s", run.StepName, run.ExitCode, run.Output)))
	return hex.EncodeToString(sum[:8])
}

func failurePrompt(job *store.LoopJob, run domain.CardRun) string {
	var builder strings.Builder
	builder.WriteString("Döngü adımı başarısız oldu.\n\n")
	builder.WriteString("Adım: ")
	builder.WriteString(run.StepName)
	builder.WriteString("\nÇıkış kodu: ")
	builder.WriteString(fmt.Sprint(run.ExitCode))
	builder.WriteString("\nProje: ")
	builder.WriteString(job.Project)
	builder.WriteString("\n\nÇıktı:\n")
	builder.WriteString(strings.TrimSpace(run.Output))
	builder.WriteString("\n\nSorunu bul ve düzelt. Döngü çalışmaya devam ediyor.")
	return builder.String()
}
