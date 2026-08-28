package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Emirfs/conclave/internal/domain"
)

// ExportConversation renders a card's whole transcript as Markdown. It reads
// every turn rather than the window the canvas carries: an export is the record
// of a conversation, not the part of it a card happened to be showing.
func (s *Store) ExportConversation(ctx context.Context, conversationID int64) (string, error) {
	var title, kind, project, role string
	var created string
	err := s.db.QueryRowContext(ctx, `
SELECT title, kind, COALESCE(project_path, ''), COALESCE(role, ''), created_at
FROM conversations WHERE id = ?`, conversationID).
		Scan(&title, &kind, &project, &role, &created)
	if err != nil {
		return "", err
	}
	turns, err := s.allTurns(ctx, conversationID)
	if err != nil {
		return "", err
	}

	var out strings.Builder
	fmt.Fprintf(&out, "# %s\n\n", title)
	createdAt, _ := time.Parse(time.RFC3339Nano, created)
	if !createdAt.IsZero() {
		fmt.Fprintf(&out, "- Başlangıç: %s\n", createdAt.Local().Format("2006-01-02 15:04"))
	}
	if project != "" {
		fmt.Fprintf(&out, "- Proje: `%s`\n", project)
	}
	if role != "" {
		fmt.Fprintf(&out, "- Rol: %s\n", role)
	}
	fmt.Fprintf(&out, "- Dışa aktarım: %s\n\n", time.Now().Local().Format("2006-01-02 15:04"))

	if len(turns) == 0 {
		out.WriteString("_Bu kartta henüz mesaj yok._\n")
		return out.String(), nil
	}
	for _, turn := range turns {
		fmt.Fprintf(&out, "## %s\n\n%s\n\n", promptHeading(turn.Kind), strings.TrimSpace(turn.Prompt))
		for _, response := range turn.Responses {
			fmt.Fprintf(&out, "### %s\n\n", response.Provider)
			switch {
			case response.Error != "":
				fmt.Fprintf(&out, "> Hata: %s\n\n", strings.TrimSpace(response.Error))
			case response.Status == domain.StatusCanceled:
				out.WriteString("> Durduruldu.\n\n")
				if content := strings.TrimSpace(response.Content); content != "" {
					out.WriteString(content + "\n\n")
				}
			default:
				content := strings.TrimSpace(response.Content)
				if content == "" {
					content = "_Boş cevap._"
				}
				out.WriteString(content + "\n\n")
			}
		}
	}
	return out.String(), nil
}

// promptHeading says who spoke, so an exported transcript does not read as
// though the user typed every message in it.
func promptHeading(kind string) string {
	switch kind {
	case domain.TurnRelay:
		return "Bağlı karttan"
	case domain.TurnNudge:
		return "Sistem"
	default:
		return "Kullanıcı"
	}
}

// allTurns reads a conversation's complete history, oldest first.
func (s *Store) allTurns(ctx context.Context, conversationID int64) ([]domain.ChatTurn, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, prompt, created_at, COALESCE(kind, 'user') FROM chat_turns
WHERE conversation_id = ? ORDER BY id`, conversationID)
	if err != nil {
		return nil, err
	}
	var turns []domain.ChatTurn
	for rows.Next() {
		var turn domain.ChatTurn
		var created string
		if err := rows.Scan(&turn.ID, &turn.Prompt, &created, &turn.Kind); err != nil {
			rows.Close()
			return nil, err
		}
		turn.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		turns = append(turns, turn)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for index := range turns {
		responses, err := s.turnResponses(ctx, turns[index].ID)
		if err != nil {
			return nil, err
		}
		turns[index].Responses = responses
	}
	return turns, nil
}

func (s *Store) turnResponses(ctx context.Context, turnID int64) ([]domain.ChatResponse, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, provider, status, content, error FROM chat_responses
WHERE turn_id = ? ORDER BY id`, turnID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	responses := []domain.ChatResponse{}
	for rows.Next() {
		var response domain.ChatResponse
		if err := rows.Scan(&response.ID, &response.Provider, &response.Status,
			&response.Content, &response.Error); err != nil {
			return nil, err
		}
		responses = append(responses, response)
	}
	return responses, rows.Err()
}

// ExportBoard writes the whole board out as one self-contained value: cards
// with their transcripts, notes, pipelines, and the links between them.
func (s *Store) ExportBoard(ctx context.Context) (domain.BoardExport, error) {
	export := domain.BoardExport{
		Version:    domain.BoardExportVersion,
		ExportedAt: time.Now().UTC(),
		Nodes:      []domain.ExportedNode{},
		Links:      []domain.ExportedLink{},
	}
	canvas, err := s.Canvas(ctx)
	if err != nil {
		return export, err
	}
	conversations := make(map[int64]domain.Conversation, len(canvas.Conversations))
	for _, item := range canvas.Conversations {
		conversations[item.ID] = item
	}
	pipelines := make(map[int64]domain.Pipeline, len(canvas.Pipelines))
	for _, item := range canvas.Pipelines {
		pipelines[item.ID] = item
	}
	triggers := make(map[int64]domain.Trigger, len(canvas.Triggers))
	for _, item := range canvas.Triggers {
		triggers[item.ID] = item
	}
	gates := make(map[int64]domain.Gate, len(canvas.Gates))
	for _, item := range canvas.Gates {
		gates[item.ID] = item
	}

	for _, node := range canvas.Nodes {
		exported := domain.ExportedNode{
			NodeID: node.ID, Kind: node.Kind,
			X: node.X, Y: node.Y, Width: node.Width, Height: node.Height,
			Color: node.Color, Body: node.Body,
		}
		switch {
		case node.Kind == domain.NodeConversation && node.ConversationID != nil:
			conversation, known := conversations[*node.ConversationID]
			if !known {
				continue
			}
			turns, err := s.allTurns(ctx, conversation.ID)
			if err != nil {
				return export, err
			}
			exported.Title = conversation.Title
			exported.ConversationKind = conversation.Kind
			exported.Providers = conversation.Providers
			exported.ProjectPath = conversation.ProjectPath
			exported.Access = conversation.Access
			exported.Role = conversation.Role
			exported.Models = conversation.Models
			exported.Turns = exportTurns(turns)
		case node.Kind == domain.NodeGate && node.GateID != nil:
			gate, known := gates[*node.GateID]
			if !known {
				continue
			}
			exported.Title = gate.Title
			exported.GateMode = gate.Mode
			exported.Pattern = gate.Pattern
			exported.CaseSensitive = gate.CaseSensitive
		case node.Kind == domain.NodeTrigger && node.TriggerID != nil:
			trigger, known := triggers[*node.TriggerID]
			if !known {
				continue
			}
			exported.Title = trigger.Title
			exported.Prompt = trigger.Prompt
			exported.TriggerMode = trigger.Mode
			exported.IntervalSeconds = trigger.IntervalSeconds
			exported.AtTime = trigger.AtTime
			exported.Enabled = trigger.Enabled
		case node.Kind == domain.NodePipeline && node.PipelineID != nil:
			pipeline, known := pipelines[*node.PipelineID]
			if !known {
				continue
			}
			exported.Title = pipeline.Title
			exported.ProjectPath = pipeline.ProjectPath
			exported.Stages = pipeline.Stages
		}
		export.Nodes = append(export.Nodes, exported)
	}

	for _, link := range canvas.Links {
		export.Links = append(export.Links, domain.ExportedLink{
			SourceNodeID: link.SourceID, TargetNodeID: link.TargetID,
			SourceHandle: link.SourceHandle,
			Mode:         link.Mode, MaxRounds: link.MaxRounds,
			UntilDone: link.UntilDone, Briefing: link.Briefing,
		})
	}
	return export, nil
}

func exportTurns(turns []domain.ChatTurn) []domain.ExportedTurn {
	exported := make([]domain.ExportedTurn, 0, len(turns))
	for _, turn := range turns {
		item := domain.ExportedTurn{
			Prompt: turn.Prompt, Kind: turn.Kind, CreatedAt: turn.CreatedAt,
			Responses: make([]domain.ExportedResponse, 0, len(turn.Responses)),
		}
		for _, response := range turn.Responses {
			item.Responses = append(item.Responses, domain.ExportedResponse{
				Provider: response.Provider, Status: response.Status,
				Content: response.Content, Error: response.Error,
			})
		}
		exported = append(exported, item)
	}
	return exported
}

// ImportBoard adds an exported board to the current one. It never replaces
// anything: the file's cards arrive alongside what is already there, offset so
// they do not land on top of it. Imported turns are written as history, not
// queued — an import is a record of work that already happened.
func (s *Store) ImportBoard(ctx context.Context, export domain.BoardExport, offsetX, offsetY float64) (domain.ImportResult, error) {
	var result domain.ImportResult
	if export.Version > domain.BoardExportVersion {
		return result, fmt.Errorf("this board was exported by a newer build (version %d, this build understands %d)",
			export.Version, domain.BoardExportVersion)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	// Old node id to new node id, which is how the links find their ends again.
	nodes := make(map[int64]int64, len(export.Nodes))

	for _, node := range export.Nodes {
		newNodeID, err := importNode(ctx, tx, node, offsetX, offsetY, now)
		if err != nil {
			return result, err
		}
		if newNodeID == 0 {
			continue
		}
		nodes[node.NodeID] = newNodeID
		result.Nodes++
	}

	for _, link := range export.Links {
		source, sourceKnown := nodes[link.SourceNodeID]
		target, targetKnown := nodes[link.TargetNodeID]
		// A link whose ends did not both come along has nothing to connect.
		if !sourceKnown || !targetKnown || source == target {
			continue
		}
		options := domain.LinkOptions{
			Mode: link.Mode, MaxRounds: link.MaxRounds,
			UntilDone: link.UntilDone, Briefing: link.Briefing,
			SourceHandle: link.SourceHandle,
		}.Normalised()
		if _, err := tx.ExecContext(ctx, `
INSERT INTO canvas_links(source_id, target_id, created_at, mode, max_rounds, until_done, briefing, source_handle)
VALUES(?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(source_id, target_id) DO NOTHING`,
			source, target, now, options.Mode, options.MaxRounds,
			options.UntilDone, options.Briefing, options.SourceHandle); err != nil {
			return result, err
		}
		result.Links++
	}
	return result, tx.Commit()
}

// importNode writes one exported node and whatever record stands behind it,
// returning the new canvas node id. A node of an unknown kind is skipped rather
// than failing the whole import.
func importNode(ctx context.Context, tx *sql.Tx, node domain.ExportedNode, offsetX, offsetY float64, now string) (int64, error) {
	x, y := node.X+offsetX, node.Y+offsetY
	width, height := node.Width, node.Height

	switch node.Kind {
	case domain.NodeNote:
		if width == 0 || height == 0 {
			width, height = noteWidth, noteHeight
		}
		result, err := tx.ExecContext(ctx, `
INSERT INTO canvas_nodes(kind, x, y, width, height, z, color, body, updated_at)
VALUES(?, ?, ?, ?, ?, (SELECT COALESCE(MAX(z), 0) + 1 FROM canvas_nodes), ?, ?, ?)`,
			domain.NodeNote, x, y, width, height, node.Color, node.Body, now)
		if err != nil {
			return 0, err
		}
		return result.LastInsertId()

	case domain.NodeJoin:
		if width == 0 || height == 0 {
			width, height = joinWidth, joinHeight
		}
		result, err := tx.ExecContext(ctx, `
INSERT INTO canvas_nodes(kind, x, y, width, height, z, color, body, updated_at)
VALUES(?, ?, ?, ?, ?, (SELECT COALESCE(MAX(z), 0) + 1 FROM canvas_nodes), '', ?, ?)`,
			domain.NodeJoin, x, y, width, height, node.Body, now)
		if err != nil {
			return 0, err
		}
		return result.LastInsertId()

	case domain.NodeGate:
		if width == 0 || height == 0 {
			width, height = gateWidth, gateHeight
		}
		result, err := tx.ExecContext(ctx, `
INSERT INTO gates(title, mode, pattern, case_sensitive, created_at)
VALUES(?, ?, ?, ?, ?)`,
			node.Title, gateMode(node.GateMode), node.Pattern,
			boolToInt(node.CaseSensitive), now)
		if err != nil {
			return 0, err
		}
		gateID, err := result.LastInsertId()
		if err != nil {
			return 0, err
		}
		result, err = tx.ExecContext(ctx, `
INSERT INTO canvas_nodes(kind, gate_id, x, y, width, height, z, updated_at)
VALUES(?, ?, ?, ?, ?, ?, (SELECT COALESCE(MAX(z), 0) + 1 FROM canvas_nodes), ?)`,
			domain.NodeGate, gateID, x, y, width, height, now)
		if err != nil {
			return 0, err
		}
		return result.LastInsertId()

	case domain.NodeTrigger:
		if width == 0 || height == 0 {
			width, height = triggerWidth, triggerHeight
		}
		// An imported trigger arrives disarmed whatever the file said. A board
		// someone is looking through should not start doing work on its own the
		// moment it lands.
		result, err := tx.ExecContext(ctx, `
INSERT INTO triggers(title, prompt, mode, interval_seconds, at_time, enabled, created_at)
VALUES(?, ?, ?, ?, ?, 0, ?)`,
			node.Title, node.Prompt, triggerMode(node.TriggerMode),
			triggerInterval(node.IntervalSeconds), node.AtTime, now)
		if err != nil {
			return 0, err
		}
		triggerID, err := result.LastInsertId()
		if err != nil {
			return 0, err
		}
		result, err = tx.ExecContext(ctx, `
INSERT INTO canvas_nodes(kind, trigger_id, x, y, width, height, z, updated_at)
VALUES(?, ?, ?, ?, ?, ?, (SELECT COALESCE(MAX(z), 0) + 1 FROM canvas_nodes), ?)`,
			domain.NodeTrigger, triggerID, x, y, width, height, now)
		if err != nil {
			return 0, err
		}
		return result.LastInsertId()

	case domain.NodePipeline:
		if width == 0 || height == 0 {
			width, height = pipelineWidth, pipelineHeight
		}
		result, err := tx.ExecContext(ctx,
			"INSERT INTO pipelines(title, project_path, created_at) VALUES(?, ?, ?)",
			node.Title, node.ProjectPath, now)
		if err != nil {
			return 0, err
		}
		pipelineID, err := result.LastInsertId()
		if err != nil {
			return 0, err
		}
		for position, stage := range node.Stages {
			if _, err := tx.ExecContext(ctx,
				"INSERT INTO pipeline_stages(pipeline_id, position, name, command) VALUES(?, ?, ?, ?)",
				pipelineID, position, stage.Name, stage.Command); err != nil {
				return 0, err
			}
		}
		result, err = tx.ExecContext(ctx, `
INSERT INTO canvas_nodes(kind, pipeline_id, x, y, width, height, z, updated_at)
VALUES(?, ?, ?, ?, ?, ?, (SELECT COALESCE(MAX(z), 0) + 1 FROM canvas_nodes), ?)`,
			domain.NodePipeline, pipelineID, x, y, width, height, now)
		if err != nil {
			return 0, err
		}
		return result.LastInsertId()

	case domain.NodeConversation:
		if width == 0 || height == 0 {
			width, height = conversationWidth, conversationHeight
		}
		access := node.Access
		if access == "" {
			access = "edit"
		}
		kind := node.ConversationKind
		if kind == "" {
			kind = domain.KindSolo
		}
		result, err := tx.ExecContext(ctx, `
INSERT INTO conversations(title, kind, created_at, project_path, access, role)
VALUES(?, ?, ?, ?, ?, ?)`,
			node.Title, kind, now, node.ProjectPath, access, node.Role)
		if err != nil {
			return 0, err
		}
		conversationID, err := result.LastInsertId()
		if err != nil {
			return 0, err
		}
		for position, name := range node.Providers {
			if _, err := tx.ExecContext(ctx,
				"INSERT INTO conversation_providers(conversation_id, provider, position) VALUES(?, ?, ?)",
				conversationID, name, position); err != nil {
				return 0, err
			}
		}
		for name, model := range node.Models {
			if model == "" {
				continue
			}
			if _, err := tx.ExecContext(ctx,
				"INSERT INTO conversation_models(conversation_id, provider, model) VALUES(?, ?, ?)",
				conversationID, name, model); err != nil {
				return 0, err
			}
		}
		if err := importTurns(ctx, tx, conversationID, node.Turns); err != nil {
			return 0, err
		}
		result, err = tx.ExecContext(ctx, `
INSERT INTO canvas_nodes(kind, conversation_id, x, y, width, height, z, updated_at)
VALUES(?, ?, ?, ?, ?, ?, (SELECT COALESCE(MAX(z), 0) + 1 FROM canvas_nodes), ?)`,
			domain.NodeConversation, conversationID, x, y, width, height, now)
		if err != nil {
			return 0, err
		}
		return result.LastInsertId()
	}
	return 0, nil
}

// importTurns writes an imported transcript as finished history. A response
// that was queued or running when the board was exported arrives as canceled:
// the run it belonged to is long over, and re-queueing it here would send
// somebody else's prompt to a provider.
func importTurns(ctx context.Context, tx *sql.Tx, conversationID int64, turns []domain.ExportedTurn) error {
	for _, turn := range turns {
		created := turn.CreatedAt.UTC().Format(time.RFC3339Nano)
		if turn.CreatedAt.IsZero() {
			created = time.Now().UTC().Format(time.RFC3339Nano)
		}
		kind := turn.Kind
		if kind == "" {
			kind = domain.TurnUser
		}
		result, err := tx.ExecContext(ctx,
			"INSERT INTO chat_turns(conversation_id, prompt, created_at, kind) VALUES(?, ?, ?, ?)",
			conversationID, turn.Prompt, created, kind)
		if err != nil {
			return err
		}
		turnID, err := result.LastInsertId()
		if err != nil {
			return err
		}
		for _, response := range turn.Responses {
			status := response.Status
			if status == domain.StatusQueued || status == domain.StatusRunning || status == "" {
				status = domain.StatusCanceled
			}
			if _, err := tx.ExecContext(ctx, `
INSERT INTO chat_responses(turn_id, provider, status, content, error, updated_at)
VALUES(?, ?, ?, ?, ?, ?)`,
				turnID, response.Provider, status, response.Content, response.Error, created); err != nil {
				return err
			}
		}
	}
	return nil
}

// triggerMode falls back to manual for a file written by a newer version, or a
// hand-edited one. An unrecognised schedule must not become a trigger that
// fires on terms nobody can see.
func triggerMode(mode string) string {
	switch mode {
	case domain.TriggerInterval, domain.TriggerDaily, domain.TriggerManual:
		return mode
	}
	return domain.TriggerManual
}

func triggerInterval(seconds int) int {
	if seconds < minInterval {
		return minInterval
	}
	return seconds
}

// gateMode falls back to the plainest condition for a file written by a newer
// version, or a hand-edited one. An unrecognised condition must not become a
// gate that decides on terms nobody can see.
func gateMode(mode string) string {
	switch mode {
	case domain.GateContains, domain.GateMissing, domain.GateMatches, domain.GateNotEmpty:
		return mode
	}
	return domain.GateNotEmpty
}
