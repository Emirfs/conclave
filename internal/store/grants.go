package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Emirfs/conclave/internal/domain"
	"github.com/Emirfs/conclave/internal/grants"
)

var ErrGrantConflict = errors.New("başvuru başka bir işlem tarafından değiştirildi; güncel durumu yükleyin")

func (s *Store) LoadGrantCatalogue(ctx context.Context) (grants.Catalogue, error) {
	var payload string
	var result grants.Catalogue
	if err := s.db.QueryRowContext(ctx, "SELECT payload FROM grant_catalogue WHERE id = 1").Scan(&payload); err != nil {
		return result, err
	}
	if err := json.Unmarshal([]byte(payload), &result); err != nil {
		return result, err
	}
	return result, nil
}

func (s *Store) SaveGrantCatalogue(ctx context.Context, catalogue grants.Catalogue) error {
	for index := range catalogue.Programs {
		catalogue.Programs[index].SourceText = grants.ScrubCredentials(catalogue.Programs[index].SourceText)
	}
	payload, err := json.Marshal(catalogue)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "INSERT INTO grant_catalogue(id,payload,updated_at) VALUES(1,?,?) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload,updated_at=excluded.updated_at", string(payload), time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Store) CreateGrantProject(ctx context.Context, input grants.ProjectInput) (grants.Project, error) {
	grants.SanitizeInput(&input)
	project := grants.Project{Input: input, Version: 1, Stage: "research", Conversations: make(map[string]int64), UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if err := grants.ValidateInput(input); err != nil {
		return project, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return project, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, "INSERT INTO grant_projects(payload,version,updated_at) VALUES('{}',1,?)", project.UpdatedAt)
	if err != nil {
		return project, err
	}
	project.ID, err = result.LastInsertId()
	if err != nil {
		return project, err
	}
	for _, name := range grants.Providers {
		title := grants.MaskForSharing(input.Facts["title"]+" — "+name+" / "+grants.Roles[name], input.SensitiveTerms)
		conversation, err := tx.ExecContext(ctx, "INSERT INTO conversations(title,kind,created_at,access,role) VALUES(?,?,?,?,?)", title, domain.KindSolo, project.UpdatedAt, "read", "grant:"+grants.Roles[name])
		if err != nil {
			return project, err
		}
		id, err := conversation.LastInsertId()
		if err != nil {
			return project, err
		}
		project.Conversations[name] = id
		if _, err := tx.ExecContext(ctx, "INSERT INTO conversation_providers(conversation_id,provider,position) VALUES(?,?,0)", id, name); err != nil {
			return project, err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO grant_conversations(conversation_id,project_id,provider) VALUES(?,?,?)", id, project.ID, name); err != nil {
			return project, err
		}
	}
	if err := grants.PreparePending(&project); err != nil {
		return project, err
	}
	project.Normalize()
	payload, err := json.Marshal(project)
	if err != nil {
		return project, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE grant_projects SET payload=? WHERE id=?", string(payload), project.ID); err != nil {
		return project, err
	}
	return project, tx.Commit()
}

func loadGrantProject(ctx context.Context, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id int64) (grants.Project, error) {
	var project grants.Project
	var payload string
	var version int
	var updated string
	if err := query.QueryRowContext(ctx, "SELECT payload,version,updated_at FROM grant_projects WHERE id=?", id).Scan(&payload, &version, &updated); err != nil {
		return project, err
	}
	if err := json.Unmarshal([]byte(payload), &project); err != nil {
		return project, err
	}
	project.ID, project.Version, project.UpdatedAt = id, version, updated
	project.Normalize()
	return project, nil
}

func (s *Store) LoadGrantProject(ctx context.Context, id int64) (grants.Project, error) {
	return loadGrantProject(ctx, s.db, id)
}

func (s *Store) ListGrantProjects(ctx context.Context) ([]grants.ProjectSummary, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id,version,payload,updated_at FROM grant_projects ORDER BY id DESC LIMIT 50")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]grants.ProjectSummary, 0)
	for rows.Next() {
		var item grants.ProjectSummary
		var payload string
		if err := rows.Scan(&item.ID, &item.Version, &payload, &item.UpdatedAt); err != nil {
			return nil, err
		}
		var saved struct {
			Stage string `json:"stage"`
			Input struct {
				Facts   map[string]string `json:"facts"`
				Program struct {
					Name string `json:"name"`
				} `json:"program"`
			} `json:"input"`
		}
		if err := json.Unmarshal([]byte(payload), &saved); err != nil {
			return nil, err
		}
		item.Title, item.ProgramName, item.Stage = saved.Input.Facts["title"], saved.Input.Program.Name, saved.Stage
		result = append(result, item)
	}
	return result, rows.Err()
}

func updateGrantProject(ctx context.Context, query interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, project *grants.Project, expected int) error {
	project.Version = expected + 1
	project.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	project.Normalize()
	payload, err := json.Marshal(project)
	if err != nil {
		return err
	}
	result, err := query.ExecContext(ctx, "UPDATE grant_projects SET payload=?,version=?,updated_at=? WHERE id=? AND version=?", string(payload), project.Version, project.UpdatedAt, project.ID, expected)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return ErrGrantConflict
	}
	return nil
}

func (s *Store) UpdateGrantProject(ctx context.Context, project *grants.Project, expected int) error {
	return updateGrantProject(ctx, s.db, project, expected)
}

// Approval and enqueue commit together: duplicate/stale approval cannot send twice.
func (s *Store) ApproveGrantSend(ctx context.Context, id int64, approval grants.SendApproval) (grants.Project, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return grants.Project{}, err
	}
	defer tx.Rollback()
	project, err := loadGrantProject(ctx, tx, id)
	if err != nil {
		return project, err
	}
	index := -1
	for i, pending := range project.Pending {
		if pending.Key == approval.Key {
			index = i
			break
		}
	}
	if index < 0 {
		return project, ErrGrantConflict
	}
	pending := project.Pending[index]
	if pending.Stage != project.Stage || len(grants.StageProviders(project.Stage)) == 0 {
		return project, ErrGrantConflict
	}
	prompt := approval.Prompt
	if strings.TrimSpace(prompt) == "" || len(prompt) > grants.MaxPromptBytes || grants.ScrubCredentials(prompt) != prompt {
		return project, errors.New("gönderim metni boş, çok uzun veya kimlik bilgisi içeriyor")
	}
	if len(approval.SensitiveTerms) > 100 {
		return project, errors.New("en fazla 100 maskeleme ifadesi kullanılabilir")
	}
	for _, term := range approval.SensitiveTerms {
		term = grants.ScrubCredentials(strings.TrimSpace(term))
		found := false
		for _, existing := range project.Input.SensitiveTerms {
			found = found || existing == term
		}
		if term != "" && !found {
			project.Input.SensitiveTerms = append(project.Input.SensitiveTerms, term)
		}
	}
	if len(project.Input.SensitiveTerms) > 100 {
		return project, errors.New("en fazla 100 maskeleme ifadesi kullanılabilir")
	}
	if err := grants.ValidateInput(project.Input); err != nil {
		return project, err
	}
	conversationID := project.Conversations[pending.Provider]
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM conversation_providers WHERE conversation_id=? AND provider=?", conversationID, pending.Provider).Scan(&count); err != nil {
		return project, err
	}
	if count != 1 {
		return project, errors.New("başvuru sağlayıcısının konuşması değiştirildi")
	}
	var total int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM conversation_providers WHERE conversation_id=?", conversationID).Scan(&total); err != nil {
		return project, err
	}
	if total != 1 {
		return project, errors.New("başvuru alıcı kapsamı değiştirildi")
	}
	turnID, err := insertTurn(ctx, tx, conversationID, prompt, []string{pending.Provider}, domain.TurnUser)
	if err != nil {
		return project, err
	}
	project.Contributions = append(project.Contributions, grants.Contribution{Provider: pending.Provider, Role: pending.Role, Stage: project.Stage, Round: project.Round, ConversationID: conversationID, TurnID: turnID, Status: "queued", Prompt: prompt})
	project.Pending = append(project.Pending[:index], project.Pending[index+1:]...)
	// New masking terms apply to all not-yet-approved prompts as well.
	if err := grants.PreparePending(&project); err != nil {
		return project, err
	}
	project.ResearchMarkdown = grants.BuildResearchRecord(project)
	if err := updateGrantProject(ctx, tx, &project, project.Version); err != nil {
		return project, err
	}
	return project, tx.Commit()
}

// Refresh responses by exact turn id, never by canvas window/index or first completion.
func (s *Store) RefreshGrantResponses(ctx context.Context, project *grants.Project) (bool, error) {
	changed := false
	for index := range project.Contributions {
		item := &project.Contributions[index]
		if item.Status != "queued" && item.Status != "running" {
			continue
		}
		responses, err := s.chatResponses(ctx, item.TurnID)
		if err != nil {
			return false, err
		}
		if len(responses) != 1 || responses[0].Provider != item.Provider {
			return false, fmt.Errorf("%s için yanıt alıcıları değiştirildi", item.Provider)
		}
		response := responses[0]
		content, failure := grants.ScrubModelOutput(response.Content), grants.ScrubCredentials(response.Error)
		if item.Status != string(response.Status) || item.Content != content || item.Error != failure || item.Activity != response.Activity {
			item.Status, item.Content, item.Error, item.Activity = string(response.Status), content, failure, response.Activity
			changed = true
		}
	}
	return changed, nil
}

func (s *Store) requireOrdinaryConversation(ctx context.Context, id int64) error {
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM grant_conversations WHERE conversation_id=?", id).Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return errors.New("başvuru konuşması yalnızca başvuru ekranındaki onaylı akıştan yönetilebilir")
	}
	return nil
}
