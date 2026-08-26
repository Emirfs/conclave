package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/Emirfs/conclave/internal/api"
	"github.com/Emirfs/conclave/internal/domain"
	"github.com/Emirfs/conclave/internal/statedir"
)

// App exposes the daemon to the frontend. The frontend never speaks HTTP and
// never sees the daemon token: JavaScript calls these methods, and only this Go
// side reads the token file and reaches the local API. That keeps the
// browser-origin rejection in internal/api intact.
type App struct {
	ctx     context.Context
	address string
}

func NewApp(address string) *App { return &App{address: address} }

func (a *App) startup(ctx context.Context) { a.ctx = ctx }

// client builds a request-scoped client so a token written after the app
// started is still picked up.
func (a *App) client() (*api.Client, error) {
	token, err := statedir.ReadToken(statedir.TokenPath())
	if err != nil {
		return nil, err
	}
	return api.NewClient("http://"+a.address, token), nil
}

// Snapshot returns the current daemon state for the canvas.
func (a *App) Snapshot() (domain.Snapshot, error) {
	client, err := a.client()
	if err != nil {
		return domain.Snapshot{}, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 3*time.Second)
	defer cancel()
	return client.Snapshot(ctx)
}


// Canvas returns the whole board: conversations and the nodes that place them.
func (a *App) Canvas() (domain.Canvas, error) {
	client, err := a.client()
	if err != nil {
		return domain.Canvas{}, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 3*time.Second)
	defer cancel()
	return client.Canvas(ctx)
}

func (a *App) CreateConversation(input domain.NewConversation) (domain.Conversation, error) {
	client, err := a.client()
	if err != nil {
		return domain.Conversation{}, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
	defer cancel()
	return client.CreateConversation(ctx, input)
}

func (a *App) CreateNote(input domain.NewNote) (domain.CanvasNode, error) {
	client, err := a.client()
	if err != nil {
		return domain.CanvasNode{}, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
	defer cancel()
	return client.CreateNote(ctx, input)
}

// PatchCanvasNode persists a move, resize or text edit.
func (a *App) PatchCanvasNode(patch domain.CanvasNodePatch) error {
	client, err := a.client()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
	defer cancel()
	return client.PatchCanvasNode(ctx, patch)
}

func (a *App) DeleteCanvasNode(id int64) error {
	client, err := a.client()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
	defer cancel()
	return client.DeleteCanvasNode(ctx, id)
}

// EnsureDaemon starts the daemon when it is not answering. The daemon's own
// flock guard makes a redundant start harmless.
func (a *App) EnsureDaemon() error {
	if _, err := a.Snapshot(); err == nil {
		return nil
	}
	binary, err := daemonBinary()
	if err != nil {
		return err
	}
	command := exec.Command(binary, "daemon", "--listen", a.address)
	command.Stdout = nil
	command.Stderr = nil
	detachProcess(command)
	if err := command.Start(); err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}
	// The daemon writes its token before listening, so wait for a real answer
	// rather than assuming the spawn succeeded.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(250 * time.Millisecond)
		if _, err := a.Snapshot(); err == nil {
			return nil
		}
	}
	return errors.New("daemon did not become reachable")
}

// daemonBinary prefers a conclave binary sitting next to this executable and
// falls back to PATH, so a development build and an installed build both work.
func daemonBinary() (string, error) {
	executable, err := os.Executable()
	if err == nil {
		neighbour := filepath.Join(filepath.Dir(executable), "conclave"+exeSuffix)
		if info, err := os.Stat(neighbour); err == nil && !info.IsDir() {
			return neighbour, nil
		}
	}
	path, err := exec.LookPath("conclave")
	if err != nil {
		return "", errors.New("conclave binary not found next to the app or on PATH")
	}
	return path, nil
}
