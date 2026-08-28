package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/Emirfs/conclave/internal/domain"
)

// CreateGate puts a decision point on the board. It starts as a plain "is there
// anything here" check, which is the one condition that is useful before anyone
// has written a pattern.
func (s *Store) CreateGate(ctx context.Context, request domain.NewGate) (domain.Gate, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Gate{}, err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	title := strings.TrimSpace(request.Title)
	if title == "" {
		title = "Kapı"
	}
	result, err := tx.ExecContext(ctx, `
INSERT INTO gates(title, mode, pattern, case_sensitive, created_at)
VALUES(?, ?, '', 0, ?)`, title, domain.GateNotEmpty, now)
	if err != nil {
		return domain.Gate{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return domain.Gate{}, err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO canvas_nodes(kind, gate_id, x, y, width, height, z, updated_at)
VALUES(?, ?, ?, ?, ?, ?, (SELECT COALESCE(MAX(z), 0) + 1 FROM canvas_nodes), ?)`,
		domain.NodeGate, id, request.X, request.Y, gateWidth, gateHeight, now); err != nil {
		return domain.Gate{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Gate{}, err
	}
	return domain.Gate{ID: id, Title: title, Mode: domain.GateNotEmpty}, nil
}

// SetGate replaces a gate's condition in one write. A pattern that will not
// compile is refused here rather than at the moment a run reaches the gate,
// where a broken condition would silently stop a flow.
func (s *Store) SetGate(ctx context.Context, id int64, config domain.GateConfig) error {
	title := strings.TrimSpace(config.Title)
	if title == "" {
		title = "Kapı"
	}
	switch config.Mode {
	case domain.GateContains, domain.GateMissing, domain.GateMatches, domain.GateNotEmpty:
	default:
		return fmt.Errorf("unknown gate mode %q", config.Mode)
	}
	pattern := config.Pattern
	if config.Mode == domain.GateMatches {
		if _, err := regexp.Compile(pattern); err != nil {
			return fmt.Errorf("the expression cannot be read: %w", err)
		}
	}
	if config.Mode != domain.GateNotEmpty && strings.TrimSpace(pattern) == "" {
		return errors.New("this condition needs something to look for")
	}

	result, err := s.db.ExecContext(ctx, `
UPDATE gates SET title = ?, mode = ?, pattern = ?, case_sensitive = ? WHERE id = ?`,
		title, config.Mode, pattern, boolToInt(config.CaseSensitive), id)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// gateOfNode reads the condition a gate node carries.
func (s *Store) gateOfNode(ctx context.Context, nodeID int64) (domain.Gate, error) {
	var gate domain.Gate
	var sensitive int
	err := s.db.QueryRowContext(ctx, `
SELECT g.id, g.title, g.mode, g.pattern, g.case_sensitive
FROM gates g JOIN canvas_nodes n ON n.gate_id = g.id
WHERE n.id = ?`, nodeID).Scan(&gate.ID, &gate.Title, &gate.Mode, &gate.Pattern, &sensitive)
	if err != nil {
		return domain.Gate{}, err
	}
	gate.NodeID = nodeID
	gate.CaseSensitive = sensitive != 0
	return gate, nil
}

// gateAllows reports which way a message leaves a gate. A condition that cannot
// be read sends everything down the else port: a broken pattern must not decide
// that a flow simply stops.
func gateAllows(gate domain.Gate, payload string) bool {
	text := payload
	pattern := gate.Pattern
	if !gate.CaseSensitive {
		text = strings.ToLower(text)
		pattern = strings.ToLower(pattern)
	}
	switch gate.Mode {
	case domain.GateContains:
		return strings.Contains(text, pattern)
	case domain.GateMissing:
		return !strings.Contains(text, pattern)
	case domain.GateMatches:
		expression := gate.Pattern
		if !gate.CaseSensitive {
			expression = "(?i)" + expression
		}
		matcher, err := regexp.Compile(expression)
		if err != nil {
			return false
		}
		return matcher.MatchString(payload)
	default:
		return strings.TrimSpace(payload) != ""
	}
}

// recordGateDecision remembers what a gate last saw, so a board can be read
// after the fact instead of only watched as it happens.
func (s *Store) recordGateDecision(ctx context.Context, gateID int64, passed bool) error {
	result := domain.GateElse
	if passed {
		result = domain.GatePass
	}
	_, err := s.db.ExecContext(ctx,
		"UPDATE gates SET last_result = ?, last_seen_at = ? WHERE id = ?",
		result, time.Now().UTC().Format(time.RFC3339Nano), gateID)
	return err
}

// listGates reports every gate with the node that shows it.
func (s *Store) listGates(ctx context.Context) ([]domain.Gate, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT g.id, COALESCE(n.id, 0), g.title, g.mode, g.pattern, g.case_sensitive,
       g.last_result, g.last_seen_at
FROM gates g LEFT JOIN canvas_nodes n ON n.gate_id = g.id
ORDER BY g.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	gates := []domain.Gate{}
	for rows.Next() {
		var gate domain.Gate
		var sensitive int
		if err := rows.Scan(&gate.ID, &gate.NodeID, &gate.Title, &gate.Mode, &gate.Pattern,
			&sensitive, &gate.LastResult, &gate.LastSeenAt); err != nil {
			return nil, err
		}
		gate.CaseSensitive = sensitive != 0
		gates = append(gates, gate)
	}
	return gates, rows.Err()
}
