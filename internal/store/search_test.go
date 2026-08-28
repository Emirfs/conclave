package store

import (
	"context"
	"strings"
	"testing"

	"github.com/Emirfs/conclave/internal/domain"
)

func TestSearchFindsPromptsAnswersAndNotes(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	conversation, err := store.CreateConversation(ctx, domain.NewConversation{
		Title: "Claude", Kind: domain.KindSolo, Providers: []string{"claude"},
	})
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if _, err := store.CreateConversationTurn(ctx, conversation.ID, "linker betigini gozden gecir"); err != nil {
		t.Fatalf("turn: %v", err)
	}
	job, err := store.ClaimChatResponse(ctx)
	if err != nil || job == nil {
		t.Fatalf("claim: %v", err)
	}
	if err := store.FinishChatResponse(ctx, job.ResponseID, domain.StatusPassed,
		"linker script Flash bolgesini yanlis hizaliyor", ""); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if _, err := store.CreateNote(ctx, domain.NewNote{Body: "linker notu: 0x8000000"}); err != nil {
		t.Fatalf("note: %v", err)
	}

	hits, err := store.Search(ctx, "linker", 50)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	found := map[string]bool{}
	for _, hit := range hits {
		found[hit.Where] = true
	}
	for _, where := range []string{"prompt", "answer", "note"} {
		if !found[where] {
			t.Fatalf("search missed the %s; got %+v", where, hits)
		}
	}
}

// A card is found by what it is called and by what it was told to be, not only
// by what was said inside it.
func TestSearchFindsTitleAndRole(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	conversation, err := store.CreateConversation(ctx, domain.NewConversation{
		Title: "Bootloader kurulu", Kind: domain.KindSolo, Providers: []string{"claude"},
	})
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	hits, err := store.Search(ctx, "bootloader", 50)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 1 || hits[0].Where != "title" {
		t.Fatalf("title search returned %+v", hits)
	}
	if hits[0].NodeID == 0 {
		t.Fatal("a hit with no node cannot be jumped to")
	}

	if err := store.SetConversationRole(ctx, conversation.ID, "elestirel gozden gecirici"); err != nil {
		t.Fatalf("set role: %v", err)
	}
	hits, err = store.Search(ctx, "gozden", 50)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 1 || hits[0].Where != "role" {
		t.Fatalf("role search returned %+v", hits)
	}
}

// SQLite folds ASCII only, which is why matching is done in Go: a board written
// in Turkish has to be searchable with the letters on the keyboard.
func TestSearchFoldsTurkishCase(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	if _, err := store.CreateNote(ctx, domain.NewNote{Body: "İŞ BİTTİ, ŞİMDİ ÖLÇÜM"}); err != nil {
		t.Fatalf("note: %v", err)
	}
	for _, query := range []string{"iş bitti", "ölçüm", "ÖLÇÜM", "is bitti"} {
		hits, err := store.Search(ctx, query, 50)
		if err != nil {
			t.Fatalf("search %q: %v", query, err)
		}
		if len(hits) != 1 {
			t.Fatalf("query %q returned %d hits, want 1", query, len(hits))
		}
	}
}

// A snippet is what makes a result readable: it must carry the match and its
// surroundings, on one line.
func TestSearchSnippetCarriesContext(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	body := strings.Repeat("dolgu ", 60) + "aranan kelime" + strings.Repeat(" dolgu", 60)
	if _, err := store.CreateNote(ctx, domain.NewNote{Body: body}); err != nil {
		t.Fatalf("note: %v", err)
	}
	hits, err := store.Search(ctx, "aranan kelime", 50)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("got %d hits, want 1", len(hits))
	}
	snippet := hits[0].Snippet
	if !strings.Contains(snippet, "aranan kelime") {
		t.Fatalf("snippet lost the match: %q", snippet)
	}
	if strings.Contains(snippet, "\n") {
		t.Fatalf("snippet is not one line: %q", snippet)
	}
	if !strings.HasPrefix(snippet, "…") || !strings.HasSuffix(snippet, "…") {
		t.Fatalf("a snippet cut from a longer text must say so: %q", snippet)
	}
	if len([]rune(snippet)) > len([]rune(body)) {
		t.Fatalf("snippet is longer than the text it came from")
	}
}

func TestSearchIgnoresAnEmptyQuery(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	if _, err := store.CreateNote(ctx, domain.NewNote{Body: "bir sey"}); err != nil {
		t.Fatalf("note: %v", err)
	}
	hits, err := store.Search(ctx, "   ", 50)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("an empty query matched %d things", len(hits))
	}
}
