package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gofrs/flock"

	"github.com/Emirfs/conclave/internal/api"
	"github.com/Emirfs/conclave/internal/daemon"
	"github.com/Emirfs/conclave/internal/domain"
	"github.com/Emirfs/conclave/internal/store"
	"github.com/Emirfs/conclave/internal/tui"
)

const defaultAddress = "127.0.0.1:7331"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "conclave:", err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	command := "tui"
	if len(arguments) > 0 {
		command, arguments = arguments[0], arguments[1:]
	}
	switch command {
	case "daemon":
		return runDaemon(arguments)
	case "status":
		return runStatus(arguments)
	case "run":
		return submitRun(arguments)
	case "tui":
		return runTUI(arguments)
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		return fmt.Errorf("unknown command %q", command)
	}
}

func runDaemon(arguments []string) error {
	flags := flag.NewFlagSet("daemon", flag.ContinueOnError)
	address := flags.String("listen", defaultAddress, "local listen address")
	workers := flags.Int("workers", 2, "maximum concurrent pipelines")
	timeout := flags.Duration("stage-timeout", 20*time.Minute, "per-stage timeout")
	stateDirectory := flags.String("state-dir", defaultStateDirectory(), "state directory")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *timeout <= 0 {
		return errors.New("stage-timeout must be positive")
	}
	if !isLoopbackAddress(*address) {
		return errors.New("listen address must use 127.0.0.1 or localhost")
	}
	if err := os.MkdirAll(*stateDirectory, 0o700); err != nil {
		return err
	}
	stateLock := flock.New(filepath.Join(*stateDirectory, "daemon.lock"))
	locked, err := stateLock.TryLock()
	if err != nil {
		return err
	}
	if !locked {
		return errors.New("another daemon owns this state directory")
	}
	defer stateLock.Unlock()
	token, err := loadOrCreateToken(filepath.Join(*stateDirectory, "token"))
	if err != nil {
		return err
	}
	database, err := store.Open(filepath.Join(*stateDirectory, "state.sqlite"))
	if err != nil {
		return err
	}
	defer database.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	engine := daemon.New(database, *workers, *timeout)
	engineDone := make(chan struct{})
	go func() {
		defer close(engineDone)
		engine.Run(ctx)
	}()

	server := &http.Server{
		Addr: *address, Handler: api.NewServer(database, token).Handler(),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	log.Printf("conclave daemon listening on %s", *address)
	err = server.ListenAndServe()
	stop()
	<-engineDone
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func runStatus(arguments []string) error {
	flags := flag.NewFlagSet("status", flag.ContinueOnError)
	jsonOutput := flags.Bool("json", false, "emit JSON")
	address := flags.String("address", defaultAddress, "daemon address")
	tokenFile := flags.String("token-file", defaultTokenFile(), "daemon token file")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	token, err := readToken(*tokenFile)
	if err != nil {
		return err
	}
	snapshot, err := api.NewClient("http://"+*address, token).Snapshot(context.Background())
	if err != nil {
		return err
	}
	if *jsonOutput {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(snapshot)
	}
	fmt.Printf("daemon: healthy, version %s\nproviders: %d, pipelines: %d\n", snapshot.Version, len(snapshot.Providers), len(snapshot.Runs))
	return nil
}

type stageFlags []string

func (s *stageFlags) String() string { return strings.Join(*s, ";") }
func (s *stageFlags) Set(value string) error {
	*s = append(*s, value)
	return nil
}

func submitRun(arguments []string) error {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	project := flags.String("project", ".", "project directory")
	address := flags.String("address", defaultAddress, "daemon address")
	tokenFile := flags.String("token-file", defaultTokenFile(), "daemon token file")
	var stages stageFlags
	flags.Var(&stages, "stage", "name=executable,arg,... (repeatable)")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	absoluteProject, err := filepath.Abs(*project)
	if err != nil {
		return err
	}
	request := domain.RunRequest{Project: absoluteProject}
	for _, raw := range stages {
		name, command, found := strings.Cut(raw, "=")
		if !found || name == "" || command == "" {
			return fmt.Errorf("invalid stage %q", raw)
		}
		parts := strings.Split(command, ",")
		for _, part := range parts {
			if part == "" {
				return fmt.Errorf("invalid empty argument in stage %q", raw)
			}
		}
		request.Stages = append(request.Stages, domain.StageSpec{Name: name, Command: parts})
	}
	token, err := readToken(*tokenFile)
	if err != nil {
		return err
	}
	id, err := api.NewClient("http://"+*address, token).CreateRun(context.Background(), request)
	if err != nil {
		return err
	}
	fmt.Printf("pipeline #%d queued\n", id)
	return nil
}

func runTUI(arguments []string) error {
	flags := flag.NewFlagSet("tui", flag.ContinueOnError)
	address := flags.String("address", defaultAddress, "daemon address")
	tokenFile := flags.String("token-file", defaultTokenFile(), "daemon token file")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	token, err := readToken(*tokenFile)
	if err != nil {
		return err
	}
	program := tea.NewProgram(tui.New(api.NewClient("http://"+*address, token)), tea.WithAltScreen())
	_, err = program.Run()
	return err
}

func defaultStateDirectory() string {
	if directory := os.Getenv("LOCALAPPDATA"); directory != "" {
		return filepath.Join(directory, "conclave")
	}
	directory, err := os.UserConfigDir()
	if err != nil {
		return ".conclave"
	}
	return filepath.Join(directory, "conclave")
}

func defaultTokenFile() string { return filepath.Join(defaultStateDirectory(), "token") }

func loadOrCreateToken(path string) (string, error) {
	token, err := readToken(path)
	if err == nil {
		return token, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	token = hex.EncodeToString(random)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return readToken(path)
		}
		return "", err
	}
	if _, err := file.WriteString(token); err != nil {
		file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	return token, nil
}

func readToken(path string) (string, error) {
	value, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(value))
	if len(token) != 64 {
		return "", errors.New("daemon token is invalid")
	}
	return token, nil
}

func isLoopbackAddress(address string) bool {
	host, _, found := strings.Cut(address, ":")
	return found && (host == "127.0.0.1" || host == "localhost")
}

func usage() {
	fmt.Println("conclave [tui|daemon|status|run] [options]")
}
