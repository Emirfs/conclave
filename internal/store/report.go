package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Emirfs/conclave/internal/domain"
)

// reportAnswerLimit keeps a report readable. A report is a summary of what
// happened, not a second copy of every transcript; the cards still hold those.
const reportAnswerLimit = 1200

// runReportWidth and runReportHeight give a report card room to be read on the
// board without being opened, which is the only reason to put it there.
const (
	runReportWidth  = 420
	runReportHeight = 320
)

// RunReport writes up one run: what started it, which cards it touched in what
// order, and what each of them said. A routine fires while nobody is watching,
// so the result has to be something a person can read afterwards.
func (s *Store) RunReport(ctx context.Context, runID int64) (string, error) {
	detail, err := s.FlowRunDetail(ctx, runID)
	if err != nil {
		return "", err
	}
	return formatRunReport(detail), nil
}

func formatRunReport(detail domain.FlowRunDetail) string {
	run := detail.Run
	var report strings.Builder

	title := run.OriginLabel
	if strings.TrimSpace(title) == "" {
		title = fmt.Sprintf("Akış #%d", run.ID)
	}
	report.WriteString("## " + title + "\n\n")

	started := localTime(run.StartedAt)
	if run.OriginKind == domain.OriginTrigger {
		report.WriteString("Tetikleyici çalıştı · " + started)
	} else {
		report.WriteString("Başlangıç · " + started)
	}
	if took := duration(run.StartedAt, run.FinishedAt); took != "" {
		report.WriteString(" · " + took)
	}
	report.WriteString("\n\n")

	failed := 0
	for _, step := range detail.Steps {
		if step.Status == string(domain.StatusFailed) {
			failed++
		}
	}
	summary := fmt.Sprintf("%d kart · %d adım", run.Cards, len(detail.Steps))
	if failed > 0 {
		summary += fmt.Sprintf(" · %d başarısız", failed)
	}
	if run.Status == domain.RunRunning {
		summary += " · sürüyor"
	}
	report.WriteString(summary + "\n")

	if len(detail.Steps) == 0 {
		report.WriteString("\nHiçbir kart bu akışta konuşmadı.\n")
		return report.String()
	}

	for _, step := range detail.Steps {
		report.WriteString("\n---\n\n### " + step.Card + statusSuffix(step.Status) + "\n\n")
		answer := strings.TrimSpace(step.Answer)
		if answer == "" {
			// A card that produced nothing is still part of the story: knowing
			// it was asked and stayed silent is the point of reading a report.
			report.WriteString("_Cevap yok._\n")
			continue
		}
		report.WriteString(clip(answer, reportAnswerLimit) + "\n")
	}
	return report.String()
}

// statusSuffix names an outcome only when it is not the ordinary one. Writing
// "geçti" beside every card would bury the one that did not.
func statusSuffix(status string) string {
	switch status {
	case string(domain.StatusFailed):
		return " · başarısız"
	case string(domain.StatusCanceled):
		return " · durduruldu"
	case string(domain.StatusRunning), string(domain.StatusQueued):
		return " · sürüyor"
	}
	return ""
}

func clip(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "\n\n…(kısaltıldı; tamamı kartın dökümünde)"
}

// localTime prints a stored instant in the reader's own clock. A report is read
// by a person the next morning, not by a machine in UTC.
func localTime(stamp string) string {
	parsed, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil {
		return stamp
	}
	return parsed.Local().Format("02.01.2006 15:04")
}

func duration(from, to string) string {
	start, err := time.Parse(time.RFC3339Nano, from)
	if err != nil {
		return ""
	}
	end, err := time.Parse(time.RFC3339Nano, to)
	if err != nil {
		return ""
	}
	took := end.Sub(start)
	if took < time.Minute {
		return fmt.Sprintf("%d sn", int(took.Seconds()))
	}
	return fmt.Sprintf("%d dk", int(took.Minutes()))
}

// reportRun puts a run's write-up on the board as a note. Only a run nobody was
// watching earns one on its own; a person who sent a message was already there
// to read the answer.
func (s *Store) reportRun(ctx context.Context, runID int64) error {
	var kind string
	var reported int
	if err := s.db.QueryRowContext(ctx,
		"SELECT COALESCE(origin_kind, 'user'), COALESCE(reported, 0) FROM flow_runs WHERE id = ?",
		runID).Scan(&kind, &reported); err != nil {
		return err
	}
	if kind != domain.OriginTrigger || reported != 0 {
		return nil
	}
	// Marked first: a report that fails to be written is better than one
	// written twice, and the run is over either way.
	if _, err := s.db.ExecContext(ctx,
		"UPDATE flow_runs SET reported = 1 WHERE id = ?", runID); err != nil {
		return err
	}
	_, err := s.ReportRunToBoard(ctx, runID)
	return err
}

// ReportRunToBoard puts a run's write-up on the board as a note, whoever asked
// for it. Reading a run in a panel and keeping it are different things, and the
// board is where anything worth keeping lives.
func (s *Store) ReportRunToBoard(ctx context.Context, runID int64) (domain.CanvasNode, error) {
	body, err := s.RunReport(ctx, runID)
	if err != nil {
		return domain.CanvasNode{}, err
	}
	x, y, err := s.runNotePosition(ctx, runID)
	if err != nil {
		return domain.CanvasNode{}, err
	}
	return s.createNote(ctx, domain.NewNote{
		Body:  body,
		Color: "var(--accent)",
		X:     x,
		Y:     y,
	}, runReportWidth, runReportHeight)
}

// runNotePosition puts a report below the cards it is about, so it lands where
// the work was rather than at the origin of the canvas.
func (s *Store) runNotePosition(ctx context.Context, runID int64) (float64, float64, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT n.x, n.y, n.height FROM canvas_nodes n
WHERE n.conversation_id IN (SELECT DISTINCT conversation_id FROM chat_turns WHERE flow_run_id = ?)`,
		runID)
	if err != nil {
		return 0, 0, err
	}
	defer rows.Close()
	var left, bottom float64
	count := 0
	for rows.Next() {
		var x, y, height float64
		if err := rows.Scan(&x, &y, &height); err != nil {
			return 0, 0, err
		}
		if count == 0 || x < left {
			left = x
		}
		if y+height > bottom {
			bottom = y + height
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	if count == 0 {
		return 0, 0, nil
	}
	return left, bottom + 40, nil
}
