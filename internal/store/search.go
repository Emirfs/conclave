package store

import (
	"context"
	"strings"
	"unicode"

	"github.com/Emirfs/conclave/internal/domain"
)

// searchScanLimit bounds how much of the board one query reads. A personal
// canvas is small, but an answer can be long, and a search box that types
// ahead must not turn every keystroke into an unbounded scan.
const searchScanLimit = 4000

// snippetContext is how many runes of surrounding text ride along with a match.
const snippetContext = 70

// Search finds text anywhere on the board: card titles and roles, the messages
// a person typed, the answers providers gave, and the notes. Matching happens
// in Go rather than in SQL because SQLite's LIKE folds ASCII only, and a board
// written in Turkish would then miss half of what is on it.
func (s *Store) Search(ctx context.Context, query string, limit int) ([]domain.SearchHit, error) {
	needle := fold(query)
	if needle == "" {
		return []domain.SearchHit{}, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	hits := []domain.SearchHit{}

	// A card's own words first: a title or a role is what someone remembers
	// about a card, and both are short enough to rank ahead of a transcript.
	cards, err := s.searchConversations(ctx, needle)
	if err != nil {
		return nil, err
	}
	hits = append(hits, cards...)

	// What a person typed, then what the providers answered. Newest first: a
	// search on a working board is nearly always about something recent.
	prompts, err := s.searchPrompts(ctx, needle)
	if err != nil {
		return nil, err
	}
	hits = append(hits, prompts...)

	answers, err := s.searchAnswers(ctx, needle)
	if err != nil {
		return nil, err
	}
	hits = append(hits, answers...)

	notes, err := s.searchNotes(ctx, needle)
	if err != nil {
		return nil, err
	}
	hits = append(hits, notes...)

	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

func (s *Store) searchConversations(ctx context.Context, needle string) ([]domain.SearchHit, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT c.id, COALESCE(n.id, 0), c.title, COALESCE(c.role, '')
FROM conversations c
LEFT JOIN canvas_nodes n ON n.conversation_id = c.id
ORDER BY c.id DESC LIMIT ?`, searchScanLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var hits []domain.SearchHit
	for rows.Next() {
		var conversationID, nodeID int64
		var title, role string
		if err := rows.Scan(&conversationID, &nodeID, &title, &role); err != nil {
			return nil, err
		}
		hit := domain.SearchHit{
			NodeID: nodeID, ConversationID: conversationID, Kind: "conversation", Title: title,
		}
		if snippet, found := match(title, needle); found {
			hit.Where, hit.Snippet = "title", snippet
		} else if snippet, found := match(role, needle); found {
			hit.Where, hit.Snippet = "role", snippet
		} else {
			continue
		}
		hits = append(hits, hit)
	}
	return hits, rows.Err()
}

func (s *Store) searchPrompts(ctx context.Context, needle string) ([]domain.SearchHit, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT t.id, t.conversation_id, COALESCE(n.id, 0), COALESCE(c.title, ''), t.prompt
FROM chat_turns t
JOIN conversations c ON c.id = t.conversation_id
LEFT JOIN canvas_nodes n ON n.conversation_id = c.id
ORDER BY t.id DESC LIMIT ?`, searchScanLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var hits []domain.SearchHit
	for rows.Next() {
		var turnID, conversationID, nodeID int64
		var title, prompt string
		if err := rows.Scan(&turnID, &conversationID, &nodeID, &title, &prompt); err != nil {
			return nil, err
		}
		snippet, found := match(prompt, needle)
		if !found {
			continue
		}
		hits = append(hits, domain.SearchHit{
			NodeID: nodeID, ConversationID: conversationID, TurnID: turnID,
			Kind: "conversation", Title: title, Where: "prompt", Snippet: snippet,
		})
	}
	return hits, rows.Err()
}

func (s *Store) searchAnswers(ctx context.Context, needle string) ([]domain.SearchHit, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT r.turn_id, t.conversation_id, COALESCE(n.id, 0), COALESCE(c.title, ''), r.provider, r.content
FROM chat_responses r
JOIN chat_turns t ON t.id = r.turn_id
JOIN conversations c ON c.id = t.conversation_id
LEFT JOIN canvas_nodes n ON n.conversation_id = c.id
WHERE r.content <> ''
ORDER BY r.id DESC LIMIT ?`, searchScanLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var hits []domain.SearchHit
	for rows.Next() {
		var turnID, conversationID, nodeID int64
		var title, providerName, content string
		if err := rows.Scan(&turnID, &conversationID, &nodeID, &title, &providerName, &content); err != nil {
			return nil, err
		}
		snippet, found := match(content, needle)
		if !found {
			continue
		}
		hits = append(hits, domain.SearchHit{
			NodeID: nodeID, ConversationID: conversationID, TurnID: turnID,
			Kind: "conversation", Title: title, Provider: providerName,
			Where: "answer", Snippet: snippet,
		})
	}
	return hits, rows.Err()
}

// searchNotes looks through the notes. A note has no conversation behind it;
// the node is the whole thing.
func (s *Store) searchNotes(ctx context.Context, needle string) ([]domain.SearchHit, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, body FROM canvas_nodes
WHERE kind = 'note' AND body <> '' ORDER BY id DESC LIMIT ?`, searchScanLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var hits []domain.SearchHit
	for rows.Next() {
		var nodeID int64
		var body string
		if err := rows.Scan(&nodeID, &body); err != nil {
			return nil, err
		}
		snippet, found := match(body, needle)
		if !found {
			continue
		}
		hits = append(hits, domain.SearchHit{
			NodeID: nodeID, Kind: "note", Title: "Not", Where: "note", Snippet: snippet,
		})
	}
	return hits, rows.Err()
}

// match reports whether text contains the folded needle, and returns the
// surrounding words so a result reads as a sentence rather than a coordinate.
func match(text, needle string) (string, bool) {
	folded := fold(text)
	index := strings.Index(folded, needle)
	if index < 0 {
		return "", false
	}
	// fold maps one rune to one rune, so a rune offset into the folded text is
	// the same offset into the original.
	runes := []rune(text)
	start := len([]rune(folded[:index]))
	end := start + len([]rune(needle))
	from := max(0, start-snippetContext)
	to := min(len(runes), end+snippetContext)
	snippet := strings.TrimSpace(collapse(string(runes[from:to])))
	if from > 0 {
		snippet = "…" + snippet
	}
	if to < len(runes) {
		snippet += "…"
	}
	return snippet, true
}

// foldedLetters maps each Turkish letter onto the plain one someone is likely
// to type instead. It is deliberately lossy in one direction only: typing
// "olcum" finds "ölçüm", and typing "ölçüm" still finds it too.
var foldedLetters = map[rune]rune{
	'ı': 'i', 'i': 'i',
	'ş': 's',
	'ğ': 'g',
	'ü': 'u',
	'ö': 'o',
	'ç': 'c',
	'â': 'a', 'î': 'i', 'û': 'u',
}

// fold lowercases text for comparison and flattens Turkish's letters onto their
// plain forms, so "İŞ" is found by typing "is". It maps one rune to one rune on
// purpose: the result stays position-compatible with its input, which is what
// lets a match be located in the original text.
func fold(text string) string {
	var builder strings.Builder
	builder.Grow(len(text))
	for _, symbol := range text {
		lowered := unicode.ToLower(symbol)
		// Uppercase İ lowercases to i plus a combining dot, which would cost a
		// rune; the table is consulted on the original letter as well.
		if plain, known := foldedLetters[lowered]; known {
			builder.WriteRune(plain)
			continue
		}
		if plain, known := foldedLetters[symbol]; known {
			builder.WriteRune(plain)
			continue
		}
		if symbol == 'İ' || symbol == 'I' {
			builder.WriteRune('i')
			continue
		}
		builder.WriteRune(lowered)
	}
	return builder.String()
}

// collapse turns the newlines and runs of spaces inside a snippet into single
// spaces. A snippet is one line in a result list, not a document.
func collapse(text string) string {
	return strings.Join(strings.Fields(text), " ")
}
