// Package rules implements mail-filter matching: given a message and an account's
// active rules (already ordered by priority), find the first rule that matches.
package rules

import "strings"

// Condition is one field/op/value test. Mirrors models.RuleCondition but this package
// stays free of the models/db dependency so Match is trivially unit-testable.
type Condition struct {
	Field string
	Op    string
	Value string
}

// MessageFields is the subset of a message's data rules can match against.
type MessageFields struct {
	From          string
	To            string
	Subject       string
	Body          string
	HasAttachment bool
	RecipientType string // "to" | "cc" | "bcc"
}

// Rule is one filter: conditions (AND'd or OR'd per MatchType) plus an action.
type Rule struct {
	ID            int64
	Priority      int
	Conditions    []Condition
	MatchType     string // "all" (AND, default) | "any" (OR)
	Action        string
	ActionValue   string
	ActionOptions map[string]any
}

// Match returns the first rule (by priority, ascending) whose conditions match msg,
// or nil if none match. Callers must pass rules pre-filtered to is_active and pre-sorted
// by priority ascending (ListActiveRules already does this).
func Match(msg MessageFields, activeRules []Rule) *Rule {
	for i := range activeRules {
		if ruleMatches(&activeRules[i], msg) {
			return &activeRules[i]
		}
	}
	return nil
}

func ruleMatches(r *Rule, msg MessageFields) bool {
	if len(r.Conditions) == 0 {
		return false
	}
	if r.MatchType == "any" {
		for _, c := range r.Conditions {
			if conditionMatches(c, msg) {
				return true
			}
		}
		return false
	}
	// default "all" (AND)
	for _, c := range r.Conditions {
		if !conditionMatches(c, msg) {
			return false
		}
	}
	return true
}

func conditionMatches(c Condition, msg MessageFields) bool {
	var target string
	switch c.Field {
	case "from":
		target = msg.From
	case "to":
		target = msg.To
	case "subject":
		target = msg.Subject
	case "body":
		target = msg.Body
	case "has_attachment":
		if msg.HasAttachment {
			target = "yes"
		} else {
			target = "no"
		}
	case "recipient_type":
		target = msg.RecipientType
	default:
		return false
	}
	return matchOp(c.Op, c.Value, target)
}

func matchOp(op, value, target string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	target = strings.ToLower(target)
	switch op {
	case "contains":
		return value != "" && strings.Contains(target, value)
	case "equals":
		return target == value
	case "starts_with":
		return value != "" && strings.HasPrefix(target, value)
	default:
		return false
	}
}
