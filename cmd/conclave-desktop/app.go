package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/Emirfs/conclave/internal/api"
	"github.com/Emirfs/conclave/internal/domain"
	"github.com/Emirfs/conclave/internal/statedir"
	"github.com/Emirfs/conclave/internal/update"
	"github.com/Emirfs/conclave/internal/vcs"
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

// SendTurn posts a message into a conversation. Every provider the
// conversation targets answers it.
func (a *App) SendTurn(conversationID int64, prompt string) error {
	client, err := a.client()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
	defer cancel()
	_, err = client.CreateTurn(ctx, conversationID, prompt)
	return err
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

// PickProjectDirectory opens the native folder chooser and returns the picked
// path, or an empty string when the user cancels.
func (a *App) PickProjectDirectory(current string) (string, error) {
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title:                "Kart icin proje dizini sec",
		DefaultDirectory:     current,
		CanCreateDirectories: false,
	})
}

// SetProject points one card at a directory and access level. Each card holds
// its own, so two cards can work on two different projects at once.
func (a *App) SetProject(conversationID int64, path, access string) error {
	client, err := a.client()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
	defer cancel()
	return client.SetProject(ctx, conversationID, domain.ProjectRequest{ProjectPath: path, Access: access})
}

// ProjectChanges lists what has changed in a card's project.
func (a *App) ProjectChanges(conversationID int64) (vcs.Status, error) {
	client, err := a.client()
	if err != nil {
		return vcs.Status{}, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 25*time.Second)
	defer cancel()
	return client.ProjectChanges(ctx, conversationID)
}

// FileDiff returns the unified diff of one changed file.
func (a *App) FileDiff(conversationID int64, path string) (vcs.Diff, error) {
	client, err := a.client()
	if err != nil {
		return vcs.Diff{}, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 25*time.Second)
	defer cancel()
	return client.FileDiff(ctx, conversationID, path)
}

// LinkNodes relays the source card's answers into the target card.
func (a *App) LinkNodes(sourceID, targetID int64) (domain.CanvasLink, error) {
	client, err := a.client()
	if err != nil {
		return domain.CanvasLink{}, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
	defer cancel()
	return client.CreateLink(ctx, domain.NewLink{SourceID: sourceID, TargetID: targetID})
}

// PairNodes links two cards both ways so they answer each other. The briefing
// is what each card is told once, before its first message from the other.
func (a *App) PairNodes(firstID, secondID int64, mode string, rounds int, briefing string) ([]domain.CanvasLink, error) {
	client, err := a.client()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
	defer cancel()
	return client.PairLink(ctx, domain.NewLink{
		SourceID: firstID, TargetID: secondID, Mode: mode, MaxRounds: rounds, Briefing: briefing,
	})
}

// UpdateLink changes how an existing link works. Changing the briefing re-briefs
// both cards before their next relayed message.
func (a *App) UpdateLink(id int64, mode string, rounds int, untilDone bool, briefing string) error {
	client, err := a.client()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
	defer cancel()
	return client.UpdateLink(ctx, id, domain.LinkOptions{
		Mode: mode, MaxRounds: rounds, UntilDone: untilDone, Briefing: briefing,
	})
}

// SetRole says what a card is meant to do when it works with another card.
// Without it two paired cards both wait to be led.
func (a *App) SetRole(conversationID int64, role string) error {
	client, err := a.client()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
	defer cancel()
	return client.SetRole(ctx, conversationID, domain.RoleRequest{Role: role})
}

// SetModel picks the model one provider of a card runs on. An empty model hands
// the choice back to the provider's own default.
func (a *App) SetModel(conversationID int64, providerName, model string) error {
	client, err := a.client()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
	defer cancel()
	return client.SetModel(ctx, conversationID, domain.ModelRequest{Provider: providerName, Model: model})
}

// ProviderModels lists what a provider can be asked for. Most of them are asked
// directly, and one of those answers over the network, so this is slower than
// the rest of the canvas calls and is only made when a list is opened.
func (a *App) ProviderModels(providerName string) (domain.ProviderModels, error) {
	client, err := a.client()
	if err != nil {
		return domain.ProviderModels{}, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 12*time.Second)
	defer cancel()
	return client.ProviderModels(ctx, providerName)
}

// Branch forks an answer into a new card per provider, each starting from that
// answer, so one line of work can be carried in several directions at once.
func (a *App) Branch(conversationID int64, answer string, providers []string) ([]domain.Conversation, error) {
	client, err := a.client()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 15*time.Second)
	defer cancel()
	return client.Branch(ctx, conversationID, domain.BranchRequest{Answer: answer, Providers: providers})
}

// ResumeDialogue clears a parked exchange so the next message starts it again.
// CreatePipeline puts a new pipeline card on the board.
func (a *App) CreatePipeline(input domain.NewPipeline) (domain.Pipeline, error) {
	client, err := a.client()
	if err != nil {
		return domain.Pipeline{}, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
	defer cancel()
	return client.CreatePipeline(ctx, input)
}

// SetPipeline replaces a pipeline card's title, project and stage list.
func (a *App) SetPipeline(id int64, config domain.PipelineConfig) error {
	client, err := a.client()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
	defer cancel()
	return client.SetPipeline(ctx, id, config)
}

// StartPipeline queues a pipeline card's stages.
func (a *App) StartPipeline(id int64) (int64, error) {
	client, err := a.client()
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
	defer cancel()
	return client.StartPipeline(ctx, id)
}

// Search looks for text across every card and note on the board.
func (a *App) Search(query string, limit int) ([]domain.SearchHit, error) {
	client, err := a.client()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
	defer cancel()
	return client.Search(ctx, query, limit)
}

// CancelConversation stops a card's running or queued turns.
func (a *App) CancelConversation(conversationID int64) (domain.CancelResult, error) {
	client, err := a.client()
	if err != nil {
		return domain.CancelResult{}, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
	defer cancel()
	return client.CancelConversation(ctx, conversationID)
}

func (a *App) ResumeDialogue(conversationID int64) error {
	client, err := a.client()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
	defer cancel()
	return client.ResumeDialogue(ctx, conversationID)
}

// SetLoop replaces a card's step list and how its cycle repeats.
func (a *App) SetLoop(conversationID int64, config domain.LoopConfig) error {
	client, err := a.client()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
	defer cancel()
	return client.SetLoop(ctx, conversationID, config)
}

// SetLoopRunning arms or disarms a card's cycle.
func (a *App) SetLoopRunning(conversationID int64, running bool) error {
	client, err := a.client()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
	defer cancel()
	return client.SetLoopRunning(ctx, conversationID, running)
}

func (a *App) UnlinkNodes(id int64) error {
	client, err := a.client()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
	defer cancel()
	return client.DeleteLink(ctx, id)
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

// UpdateStatus reports what the daemon last learned about newer releases. It
// reads a cached answer, so the canvas can ask for it as often as it likes.
func (a *App) UpdateStatus() (update.Status, error) {
	client, err := a.client()
	if err != nil {
		return update.Status{}, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 3*time.Second)
	defer cancel()
	return client.UpdateStatus(ctx)
}

// CheckUpdate looks now instead of waiting for the daily check.
func (a *App) CheckUpdate() (update.Status, error) {
	client, err := a.client()
	if err != nil {
		return update.Status{}, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 20*time.Second)
	defer cancel()
	return client.CheckUpdate(ctx)
}

// OpenReleasePage shows the release notes in the user's browser, for someone
// who wants to read what changed before installing it.
func (a *App) OpenReleasePage(url string) error {
	if !strings.HasPrefix(url, "https://github.com/") {
		return errors.New("only a github release page can be opened")
	}
	runtime.BrowserOpenURL(a.ctx, url)
	return nil
}

// ApplyUpdate hands the installation over to the installer script and quits.
//
// A running program cannot replace its own files on Windows, so the script is
// detached deliberately: it outlives this process, waits for it to exit,
// swaps the binaries and starts the new build. Nothing is installed without
// the user asking for it here.
func (a *App) ApplyUpdate() error {
	if goruntime.GOOS != "windows" {
		return errors.New("one-click update is only available on windows")
	}
	script, err := installerScript()
	if err != nil {
		return err
	}
	command := exec.Command(
		"powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", script,
		"-WaitForPid", strconv.Itoa(os.Getpid()), "-Restart",
	)
	detachProcess(command)
	if err := command.Start(); err != nil {
		return fmt.Errorf("start installer: %w", err)
	}
	// Release the process handle: the installer has to survive this app.
	if err := command.Process.Release(); err != nil {
		return err
	}
	// Give the script a moment to reach its wait before the window disappears.
	go func() {
		time.Sleep(500 * time.Millisecond)
		runtime.Quit(a.ctx)
	}()
	return nil
}

// installerScript finds the install.ps1 that the installer left next to the
// application. Without it there is nothing to run: downloading and executing a
// script from the network on the user's behalf is not something this app does.
func installerScript() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	path := filepath.Join(filepath.Dir(executable), "install.ps1")
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		return "", errors.New("install.ps1 is not next to the application; install the release by hand")
	}
	return path, nil
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
