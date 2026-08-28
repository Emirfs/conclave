package main

import (
	"context"
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

	"github.com/gofrs/flock"

	"github.com/Emirfs/conclave/internal/api"
	"github.com/Emirfs/conclave/internal/daemon"
	"github.com/Emirfs/conclave/internal/domain"
	"github.com/Emirfs/conclave/internal/statedir"
	"github.com/Emirfs/conclave/internal/store"
	"github.com/Emirfs/conclave/internal/update"
	"github.com/Emirfs/conclave/internal/version"
)

const defaultAddress = "127.0.0.1:7331"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "conclave:", err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	command := "status"
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
	case "chat":
		return submitChat(arguments)
	case "search":
		return searchBoard(arguments)
	case "export":
		return exportBoard(arguments)
	case "version", "-v", "--version":
		fmt.Println(version.Version)
		return nil
	case "update":
		return checkUpdate(arguments)
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
	chatWorkers := flags.Int("chat-workers", 4, "maximum concurrent provider chats")
	timeout := flags.Duration("stage-timeout", 20*time.Minute, "per-stage timeout")
	stateDirectory := flags.String("state-dir", statedir.Default(), "state directory")
	checkUpdates := flags.Bool("check-updates", true, "look for newer releases once a day")
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
	token, err := statedir.LoadOrCreateToken(filepath.Join(*stateDirectory, "token"))
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
	engine := daemon.New(database, *workers, *chatWorkers, *timeout)

	// A development build has no release number to compare against, so it does
	// not ask GitHub anything at all.
	var updates *update.Checker
	if version.IsRelease() && *checkUpdates {
		updates = update.NewChecker(version.Version, 24*time.Hour)
		go updates.Run(ctx)
	}
	engineDone := make(chan struct{})
	go func() {
		defer close(engineDone)
		engine.Run(ctx)
	}()

	server := &http.Server{
		Addr: *address, Handler: api.NewServer(database, token, updates).Handler(),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second,
	}
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	log.Printf("conclave daemon listening on %s", *address)
	err = server.ListenAndServe()
	stop()
	<-shutdownDone
	<-engineDone
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// exportBoard writes the board, or one card's transcript, to stdout. Redirecting
// it is the whole interface: this is a client, not a file manager.
func exportBoard(arguments []string) error {
	flags := flag.NewFlagSet("export", flag.ContinueOnError)
	conversation := flags.Int64("conversation", 0, "export one card's transcript as Markdown")
	address := flags.String("address", defaultAddress, "daemon address")
	tokenFile := flags.String("token-file", statedir.TokenPath(), "daemon token file")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	token, err := statedir.ReadToken(*tokenFile)
	if err != nil {
		return err
	}
	client := api.NewClient("http://"+*address, token)
	if *conversation > 0 {
		markdown, err := client.ExportConversation(context.Background(), *conversation)
		if err != nil {
			return err
		}
		_, err = os.Stdout.WriteString(markdown)
		return err
	}
	board, err := client.ExportBoard(context.Background())
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(board)
}

// searchBoard finds text on the canvas from a terminal. The daemon does the
// looking; this only prints what came back.
func searchBoard(arguments []string) error {
	flags := flag.NewFlagSet("search", flag.ContinueOnError)
	jsonOutput := flags.Bool("json", false, "emit JSON")
	limit := flags.Int("limit", 20, "maximum results")
	address := flags.String("address", defaultAddress, "daemon address")
	tokenFile := flags.String("token-file", statedir.TokenPath(), "daemon token file")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	query := strings.TrimSpace(strings.Join(flags.Args(), " "))
	if query == "" {
		return errors.New("search needs something to look for")
	}
	token, err := statedir.ReadToken(*tokenFile)
	if err != nil {
		return err
	}
	hits, err := api.NewClient("http://"+*address, token).Search(context.Background(), query, *limit)
	if err != nil {
		return err
	}
	if *jsonOutput {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(hits)
	}
	if len(hits) == 0 {
		fmt.Printf("no match for %q\n", query)
		return nil
	}
	for _, hit := range hits {
		fmt.Printf("%s [%s] %s\n", hit.Title, hit.Where, hit.Snippet)
	}
	return nil
}

func runStatus(arguments []string) error {
	flags := flag.NewFlagSet("status", flag.ContinueOnError)
	jsonOutput := flags.Bool("json", false, "emit JSON")
	address := flags.String("address", defaultAddress, "daemon address")
	tokenFile := flags.String("token-file", statedir.TokenPath(), "daemon token file")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	token, err := statedir.ReadToken(*tokenFile)
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

// checkUpdate asks the daemon what it knows about newer releases, forcing a
// fresh look rather than reporting a day-old answer.
func checkUpdate(arguments []string) error {
	flags := flag.NewFlagSet("update", flag.ContinueOnError)
	address := flags.String("address", defaultAddress, "daemon address")
	tokenFile := flags.String("token-file", statedir.TokenPath(), "daemon token file")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	token, err := statedir.ReadToken(*tokenFile)
	if err != nil {
		return err
	}
	status, err := api.NewClient("http://"+*address, token).CheckUpdate(context.Background())
	if err != nil {
		return err
	}
	switch {
	case status.Available:
		fmt.Printf("%s cikti (calisan surum %s)\n%s\n", status.Latest, status.Current, status.URL)
	case status.Error != "":
		fmt.Printf("%s calisiyor; kontrol basarisiz: %s\n", status.Current, status.Error)
	default:
		fmt.Printf("%s calisiyor, en guncel surum bu\n", status.Current)
	}
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
	tokenFile := flags.String("token-file", statedir.TokenPath(), "daemon token file")
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
	token, err := statedir.ReadToken(*tokenFile)
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

func submitChat(arguments []string) error {
	flags := flag.NewFlagSet("chat", flag.ContinueOnError)
	address := flags.String("address", defaultAddress, "daemon address")
	tokenFile := flags.String("token-file", statedir.TokenPath(), "daemon token file")
	conversation := flags.Int64("conversation", 0, "post into an existing conversation instead of opening one")
	var providers stageFlags
	flags.Var(&providers, "provider", "provider name (repeatable; default: all available)")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	prompt := strings.TrimSpace(strings.Join(flags.Args(), " "))
	if prompt == "" {
		return errors.New("chat message is required")
	}
	token, err := statedir.ReadToken(*tokenFile)
	if err != nil {
		return err
	}
	client := api.NewClient("http://"+*address, token)
	ctx := context.Background()

	conversationID := *conversation
	if conversationID == 0 {
		if len(providers) == 0 {
			snapshot, err := client.Snapshot(ctx)
			if err != nil {
				return err
			}
			for _, item := range snapshot.Providers {
				if item.Available && item.Kind != "memory" {
					providers = append(providers, item.Name)
				}
			}
		}
		kind := domain.KindGroup
		if len(providers) == 1 {
			kind = domain.KindSolo
		}
		// A conversation started from the CLI still belongs on the canvas, so
		// the desktop client shows it like any other.
		created, err := client.CreateConversation(ctx, domain.NewConversation{
			Title: conversationTitle(prompt), Kind: kind, Providers: providers,
		})
		if err != nil {
			return err
		}
		conversationID = created.ID
		fmt.Printf("conversation #%d opened for %s\n", created.ID, strings.Join(created.Providers, ", "))
	}
	id, err := client.CreateTurn(ctx, conversationID, prompt)
	if err != nil {
		return err
	}
	fmt.Printf("turn #%d queued in conversation #%d\n", id, conversationID)
	return nil
}

// conversationTitle keeps a readable label without carrying a whole prompt into
// the node header.
func conversationTitle(prompt string) string {
	fields := strings.Fields(prompt)
	if len(fields) > 6 {
		fields = fields[:6]
	}
	title := strings.Join(fields, " ")
	if runes := []rune(title); len(runes) > 60 {
		title = string(runes[:60])
	}
	return title
}

func isLoopbackAddress(address string) bool {
	host, _, found := strings.Cut(address, ":")
	return found && (host == "127.0.0.1" || host == "localhost")
}

func usage() {
	fmt.Println("conclave [status|daemon|run|chat|search|export|update|version] [options]")
}
