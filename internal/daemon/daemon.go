package daemon

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/Emirfs/conclave/internal/domain"
	"github.com/Emirfs/conclave/internal/provider"
	"github.com/Emirfs/conclave/internal/store"
)

const maxOutputBytes = 64 * 1024

// maxLineBytes bounds a single streamed JSON event; providers emit large init
// payloads, so this is well above the default scanner limit.
const maxLineBytes = 4 * 1024 * 1024

// progressInterval bounds how often partial output is written to SQLite.
const progressInterval = 250 * time.Millisecond

type Daemon struct {
	store       *store.Store
	workers     int
	chatWorkers int
	timeout     time.Duration
}

func New(store *store.Store, workers, chatWorkers int, timeout time.Duration) *Daemon {
	if workers < 1 {
		workers = 1
	}
	if chatWorkers < 1 {
		chatWorkers = 1
	}
	return &Daemon{store: store, workers: workers, chatWorkers: chatWorkers, timeout: timeout}
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
	for range d.chatWorkers {
		workers.Add(1)
		go func() {
			defer workers.Done()
			d.chatWorker(ctx)
		}()
	}
	<-ctx.Done()
	workers.Wait()
}

func (d *Daemon) chatWorker(ctx context.Context) {
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := d.runNextChat(ctx); err != nil && !errors.Is(err, context.Canceled) {
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

func (d *Daemon) runNextChat(ctx context.Context) error {
	job, err := d.store.ClaimChatResponse(ctx)
	if err != nil || job == nil {
		return err
	}
	persist := context.WithoutCancel(ctx)
	invocation, err := provider.ChatInvocation(job.Provider, job.Prompt)
	if err != nil {
		return d.store.FinishChatResponse(persist, job.ResponseID, domain.StatusFailed, "", err.Error())
	}
	outcome := d.executeChat(ctx, invocation, d.chatProgress(persist, job.ResponseID))
	if ctx.Err() != nil {
		_ = d.store.RequeueChatResponse(persist, job.ResponseID)
		return ctx.Err()
	}
	if outcome.quota != nil {
		_ = d.store.RecordProviderQuota(persist, job.Provider, *outcome.quota)
	}
	if outcome.failure != "" {
		return d.store.FinishChatResponse(persist, job.ResponseID, domain.StatusFailed, "", outcome.failure)
	}
	return d.store.FinishChatResponse(persist, job.ResponseID, domain.StatusPassed, outcome.content, "")
}


// chatProgress persists partial provider output so the answer appears while it
// is still being produced. Writes are throttled: a token-by-token provider
// would otherwise turn one answer into thousands of UPDATE statements.
func (d *Daemon) chatProgress(ctx context.Context, responseID int64) func(string) {
	var mutex sync.Mutex
	var last time.Time
	return func(partial string) {
		mutex.Lock()
		if time.Since(last) < progressInterval {
			mutex.Unlock()
			return
		}
		last = time.Now()
		mutex.Unlock()
		text := strings.TrimSpace(partial)
		if text == "" {
			return
		}
		// A failure here only costs a frame of liveness; the final write is
		// what makes the answer durable.
		_ = d.store.UpdateChatResponseContent(ctx, responseID, text)
	}
}

// chatResult is what one provider run produced.
type chatResult struct {
	content   string
	failure   string
	sessionID string
	quota     *provider.Quota
}

// executeChat runs a provider and reads its stdout line by line so a streaming
// format can report the answer while it is still being written. progress is
// called with the text so far.
func (d *Daemon) executeChat(parent context.Context, invocation provider.Invocation, progress func(string)) chatResult {
	workdir, err := os.MkdirTemp("", "conclave-chat-")
	if err != nil {
		return chatResult{failure: err.Error()}
	}
	defer os.RemoveAll(workdir)
	ctx, cancel := context.WithTimeout(parent, d.timeout)
	defer cancel()

	command := exec.CommandContext(ctx, invocation.Command[0], invocation.Command[1:]...)
	command.Dir = workdir
	command.Stdin = strings.NewReader(invocation.Stdin)
	command.Env = safeEnvironment()
	command.WaitDelay = 10 * time.Second
	configureProcessTree(command)

	stdout, err := command.StdoutPipe()
	if err != nil {
		return chatResult{failure: err.Error()}
	}
	stderr := &limitedBuffer{limit: maxOutputBytes}
	command.Stderr = stderr

	if err := command.Start(); err != nil {
		return chatResult{failure: err.Error()}
	}

	result := d.consumeStream(stdout, invocation.Stream, progress)

	if err := command.Wait(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return chatResult{failure: "provider timed out"}
		}
		// A provider that reported its own error explains the failure better
		// than an exit status does.
		if result.failure != "" {
			return result
		}
		failure := strings.TrimSpace(stderr.String())
		if failure == "" {
			failure = err.Error()
		}
		return chatResult{failure: failure, sessionID: result.sessionID, quota: result.quota}
	}
	if result.failure != "" {
		return result
	}
	result.content = strings.TrimSpace(result.content)
	if result.content == "" {
		return chatResult{failure: "provider returned an empty response", sessionID: result.sessionID, quota: result.quota}
	}
	return result
}

// consumeStream reads provider output to completion. It must drain the pipe
// even after hitting the size cap, otherwise the provider blocks on a full pipe
// and the run never ends.
func (d *Daemon) consumeStream(stdout io.Reader, format provider.StreamFormat, progress func(string)) chatResult {
	var result chatResult
	var accumulated strings.Builder
	var final string
	capped := false

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineBytes)
	for scanner.Scan() {
		update := provider.DecodeStreamLine(format, scanner.Text())
		if update.SessionID != "" {
			result.sessionID = update.SessionID
		}
		if update.Quota != nil {
			result.quota = update.Quota
		}
		if update.Failure != "" {
			result.failure = update.Failure
		}
		if update.Final != "" {
			final = update.Final
		}
		if update.Delta != "" && !capped {
			if accumulated.Len()+len(update.Delta) > maxOutputBytes {
				capped = true
				accumulated.WriteString("\n[output truncated]")
			} else {
				accumulated.WriteString(update.Delta)
				if progress != nil {
					progress(accumulated.String())
				}
			}
		}
	}
	// A scanner error still leaves whatever arrived before it usable.
	if err := scanner.Err(); err != nil && result.failure == "" && final == "" && accumulated.Len() == 0 {
		result.failure = err.Error()
	}
	if final != "" {
		result.content = final
	} else {
		result.content = accumulated.String()
	}
	return result
}

func safeEnvironment() []string {
	environment := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		upper := strings.ToUpper(name)
		if strings.Contains(upper, "TOKEN") || strings.Contains(upper, "SECRET") ||
			strings.Contains(upper, "PASSWORD") || strings.Contains(upper, "CREDENTIAL") ||
			strings.HasSuffix(upper, "_KEY") || strings.Contains(upper, "APIKEY") ||
			strings.Contains(upper, "API_KEY") {
			continue
		}
		environment = append(environment, entry)
	}
	return environment
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

// limitedBuffer caps how much provider output is retained and, when onGrow is
// set, reports the text captured so far as it arrives. The mutex matters
// because the daemon reads the partial text while os/exec is still writing.
type limitedBuffer struct {
	mutex     sync.Mutex
	data      bytes.Buffer
	limit     int
	truncated bool
	onGrow    func(string)
}

func (b *limitedBuffer) Write(value []byte) (int, error) {
	original := len(value)
	b.mutex.Lock()
	remaining := b.limit - b.data.Len()
	if remaining <= 0 {
		b.truncated = true
		b.mutex.Unlock()
		return original, nil
	}
	if len(value) > remaining {
		value = value[:remaining]
		b.truncated = true
	}
	_, err := b.data.Write(value)
	partial := b.data.String()
	b.mutex.Unlock()
	if err == nil && b.onGrow != nil {
		b.onGrow(partial)
	}
	return original, err
}

func (b *limitedBuffer) String() string {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	if b.truncated {
		return b.data.String() + "\n[output truncated]"
	}
	return b.data.String()
}
