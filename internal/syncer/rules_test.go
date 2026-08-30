package syncer

import (
	"testing"

	"github.com/ghostersk/gowebmail/internal/models"
)

// ---- matchRule ----

func TestMatchRule_NoActiveRules(t *testing.T) {
	msg := &models.Message{Subject: "hello"}
	if got := matchRule(msg, "me@example.com", nil); got != nil {
		t.Errorf("matchRule with no rules = %+v, want nil", got)
	}
}

func TestMatchRule_SubjectContains(t *testing.T) {
	msg := &models.Message{FromEmail: "boss@work.com", Subject: "Re: Invoice #42", BodyText: "please pay"}
	active := []models.Rule{
		{ID: 1, Priority: 1, MatchType: "all", Action: "move_to_folder", ActionValue: "Finance",
			Conditions: []models.RuleCondition{{Field: "subject", Op: "contains", Value: "invoice"}}},
	}
	got := matchRule(msg, "me@example.com", active)
	if got == nil {
		t.Fatalf("matchRule = nil, want rule 1 to match")
	}
	if got.ID != 1 || got.Action != "move_to_folder" || got.ActionValue != "Finance" {
		t.Errorf("matchRule = %+v", got)
	}
}

func TestMatchRule_ReturnsFirstMatchByPriority(t *testing.T) {
	msg := &models.Message{FromEmail: "newsletter@shop.com", Subject: "50% off everything"}
	active := []models.Rule{
		{ID: 2, Priority: 2, MatchType: "all", Action: "delete",
			Conditions: []models.RuleCondition{{Field: "subject", Op: "contains", Value: "off"}}},
		{ID: 1, Priority: 1, MatchType: "all", Action: "mark_as_spam",
			Conditions: []models.RuleCondition{{Field: "from", Op: "contains", Value: "shop.com"}}},
	}
	// Match() iterates in the slice's given order and returns the first hit — callers
	// (ListActiveRules) are documented to pre-sort by priority ascending, so put rule 1
	// (priority 1) first here to prove matchRule returns it, not rule 2.
	active[0], active[1] = active[1], active[0]
	got := matchRule(msg, "me@example.com", active)
	if got == nil || got.ID != 1 {
		t.Fatalf("matchRule = %+v, want rule with ID=1 (lower priority number, listed first)", got)
	}
}

func TestMatchRule_NoConditionsMatch(t *testing.T) {
	msg := &models.Message{FromEmail: "friend@example.com", Subject: "hi"}
	active := []models.Rule{
		{ID: 1, Priority: 1, MatchType: "all", Action: "delete",
			Conditions: []models.RuleCondition{{Field: "subject", Op: "contains", Value: "invoice"}}},
	}
	if got := matchRule(msg, "me@example.com", active); got != nil {
		t.Errorf("matchRule = %+v, want nil (no condition matches)", got)
	}
}

// ---- recipientType / containsAddress ----

func TestRecipientType(t *testing.T) {
	cases := []struct {
		name string
		msg  *models.Message
		want string
	}{
		{"plain to", &models.Message{ToList: "me@example.com"}, "to"},
		{"in cc", &models.Message{CCList: "me@example.com, other@example.com"}, "cc"},
		{"in bcc", &models.Message{BCCList: "me@example.com"}, "bcc"},
		{"cc checked before bcc", &models.Message{CCList: "me@example.com", BCCList: "me@example.com"}, "cc"},
		{"not in cc/bcc falls back to to", &models.Message{CCList: "someoneelse@example.com"}, "to"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := recipientType(tc.msg, "me@example.com")
			if got != tc.want {
				t.Errorf("recipientType = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestContainsAddress(t *testing.T) {
	cases := []struct {
		list, addr string
		want       bool
	}{
		{"a@x.com, b@x.com", "b@x.com", true},
		{"a@x.com,b@x.com", "b@x.com", true}, // no space after comma
		{"  a@x.com ,  b@x.com  ", "b@x.com", true},
		{"a@x.com, b@x.com", "c@x.com", false},
		{"", "a@x.com", false},
		{"User@Example.com", "user@example.com", true}, // case-insensitive match
	}
	for _, tc := range cases {
		if got := containsAddress(tc.list, tc.addr); got != tc.want {
			t.Errorf("containsAddress(%q, %q) = %v, want %v", tc.list, tc.addr, got, tc.want)
		}
	}
}

func TestSplitAndTrim(t *testing.T) {
	got := splitAndTrim(" A@x.com , B@x.com,C@x.com ")
	want := []string{"a@x.com", "b@x.com", "c@x.com"}
	if len(got) != len(want) {
		t.Fatalf("splitAndTrim = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("splitAndTrim[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestTrimLower(t *testing.T) {
	if got := trimLower("  MiXeD Case\t"); got != "mixed case" {
		t.Errorf("trimLower = %q", got)
	}
}

func TestParseUID(t *testing.T) {
	if got := parseUID("12345"); got != 12345 {
		t.Errorf("parseUID(\"12345\") = %d, want 12345", got)
	}
	if got := parseUID("not-a-number"); got != 0 {
		t.Errorf("parseUID(garbage) = %d, want 0", got)
	}
}
