package db

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/ghostersk/gowebmail/internal/models"
)

// ---- Rules (filters) ----

func scanRule(rowConditions, rowActionOptions string, r *models.Rule) {
	_ = json.Unmarshal([]byte(rowConditions), &r.Conditions)
	_ = json.Unmarshal([]byte(rowActionOptions), &r.ActionOptions)
}

// ListRules returns all rules for an account, ordered by priority (lowest first, then id).
func (d *DB) ListRules(accountID int64) ([]models.Rule, error) {
	rows, err := d.sql.Query(
		`SELECT id, account_id, name, priority, conditions, match_type, action, action_value,
		        action_options, is_active, created_at
		 FROM rules WHERE account_id=? ORDER BY priority ASC, id ASC`, accountID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.Rule
	for rows.Next() {
		var r models.Rule
		var conditionsJSON, optionsJSON string
		var isActive int
		if err := rows.Scan(&r.ID, &r.AccountID, &r.Name, &r.Priority, &conditionsJSON, &r.MatchType,
			&r.Action, &r.ActionValue, &optionsJSON, &isActive, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.IsActive = isActive == 1
		scanRule(conditionsJSON, optionsJSON, &r)
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListActiveRules returns only is_active rules for an account, same ordering as ListRules.
func (d *DB) ListActiveRules(accountID int64) ([]models.Rule, error) {
	all, err := d.ListRules(accountID)
	if err != nil {
		return nil, err
	}
	var active []models.Rule
	for _, r := range all {
		if r.IsActive {
			active = append(active, r)
		}
	}
	return active, nil
}

// GetRule fetches a single rule scoped to an account (so one user can't touch another's rule by id).
func (d *DB) GetRule(accountID, id int64) (*models.Rule, error) {
	r := &models.Rule{}
	var conditionsJSON, optionsJSON string
	var isActive int
	err := d.sql.QueryRow(
		`SELECT id, account_id, name, priority, conditions, match_type, action, action_value,
		        action_options, is_active, created_at
		 FROM rules WHERE account_id=? AND id=?`, accountID, id,
	).Scan(&r.ID, &r.AccountID, &r.Name, &r.Priority, &conditionsJSON, &r.MatchType,
		&r.Action, &r.ActionValue, &optionsJSON, &isActive, &r.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.IsActive = isActive == 1
	scanRule(conditionsJSON, optionsJSON, r)
	return r, nil
}

// CreateRule inserts a new rule and returns its id.
func (d *DB) CreateRule(r *models.Rule) (int64, error) {
	conditionsJSON, err := json.Marshal(r.Conditions)
	if err != nil {
		return 0, fmt.Errorf("marshal conditions: %w", err)
	}
	optionsJSON, err := json.Marshal(r.ActionOptions)
	if err != nil {
		return 0, fmt.Errorf("marshal action_options: %w", err)
	}
	if r.MatchType == "" {
		r.MatchType = "all"
	}
	res, err := d.sql.Exec(
		`INSERT INTO rules (account_id, name, priority, conditions, match_type, action, action_value, action_options, is_active)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		r.AccountID, r.Name, r.Priority, string(conditionsJSON), r.MatchType, r.Action, r.ActionValue, string(optionsJSON), boolToInt(r.IsActive),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateRule replaces an existing rule's fields (scoped to account_id).
func (d *DB) UpdateRule(r *models.Rule) error {
	conditionsJSON, err := json.Marshal(r.Conditions)
	if err != nil {
		return fmt.Errorf("marshal conditions: %w", err)
	}
	optionsJSON, err := json.Marshal(r.ActionOptions)
	if err != nil {
		return fmt.Errorf("marshal action_options: %w", err)
	}
	_, err = d.sql.Exec(
		`UPDATE rules SET name=?, priority=?, conditions=?, match_type=?, action=?, action_value=?, action_options=?, is_active=?
		 WHERE id=? AND account_id=?`,
		r.Name, r.Priority, string(conditionsJSON), r.MatchType, r.Action, r.ActionValue, string(optionsJSON), boolToInt(r.IsActive),
		r.ID, r.AccountID,
	)
	return err
}

// DeleteRule removes a rule (scoped to account_id).
func (d *DB) DeleteRule(accountID, id int64) error {
	_, err := d.sql.Exec(`DELETE FROM rules WHERE id=? AND account_id=?`, id, accountID)
	return err
}

// HasRecentAutoReply reports whether an auto-reply was already sent to recipientEmail
// for this rule within the last 24h, to prevent auto-reply loops.
func (d *DB) HasRecentAutoReply(accountID, ruleID int64, recipientEmail string) (bool, error) {
	var n int
	err := d.sql.QueryRow(
		`SELECT COUNT(*) FROM auto_reply_log
		 WHERE account_id=? AND rule_id=? AND recipient_email=? COLLATE NOCASE
		   AND sent_at > datetime('now', '-1 day')`,
		accountID, ruleID, recipientEmail,
	).Scan(&n)
	return n > 0, err
}

// LogAutoReply records that an auto-reply was just sent, for HasRecentAutoReply's window check.
func (d *DB) LogAutoReply(accountID, ruleID int64, recipientEmail string) error {
	_, err := d.sql.Exec(
		`INSERT INTO auto_reply_log (account_id, rule_id, recipient_email) VALUES (?,?,?)`,
		accountID, ruleID, recipientEmail,
	)
	return err
}
