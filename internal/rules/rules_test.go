package rules

import "testing"

func TestMatch(t *testing.T) {
	msg := MessageFields{
		From:          "boss@work.com",
		To:            "me@example.com",
		Subject:       "Weekly Report Due",
		Body:          "please see attached",
		HasAttachment: true,
		RecipientType: "to",
	}

	cases := []struct {
		name  string
		rules []Rule
		want  string // expected matched rule action value marker, "" for no match
	}{
		{
			name: "single contains condition matches",
			rules: []Rule{
				{ID: 1, Priority: 0, MatchType: "all", ActionValue: "hit",
					Conditions: []Condition{{Field: "from", Op: "contains", Value: "work.com"}}},
			},
			want: "hit",
		},
		{
			name: "equals is case-insensitive and exact",
			rules: []Rule{
				{ID: 1, Priority: 0, MatchType: "all", ActionValue: "hit",
					Conditions: []Condition{{Field: "to", Op: "equals", Value: "ME@EXAMPLE.COM"}}},
			},
			want: "hit",
		},
		{
			name: "starts_with no match",
			rules: []Rule{
				{ID: 1, Priority: 0, MatchType: "all", ActionValue: "hit",
					Conditions: []Condition{{Field: "subject", Op: "starts_with", Value: "URGENT"}}},
			},
			want: "",
		},
		{
			name: "match_type all requires every condition",
			rules: []Rule{
				{ID: 1, Priority: 0, MatchType: "all", ActionValue: "hit", Conditions: []Condition{
					{Field: "from", Op: "contains", Value: "work.com"},
					{Field: "subject", Op: "contains", Value: "NOPE"},
				}},
			},
			want: "",
		},
		{
			name: "match_type any needs only one condition",
			rules: []Rule{
				{ID: 1, Priority: 0, MatchType: "any", ActionValue: "hit", Conditions: []Condition{
					{Field: "from", Op: "contains", Value: "NOPE"},
					{Field: "subject", Op: "contains", Value: "Report"},
				}},
			},
			want: "hit",
		},
		{
			name: "has_attachment field",
			rules: []Rule{
				{ID: 1, Priority: 0, MatchType: "all", ActionValue: "hit",
					Conditions: []Condition{{Field: "has_attachment", Op: "equals", Value: "yes"}}},
			},
			want: "hit",
		},
		{
			name: "first matching rule in list order wins, later matching rules ignored",
			rules: []Rule{
				{ID: 1, Priority: 5, MatchType: "all", ActionValue: "nope",
					Conditions: []Condition{{Field: "from", Op: "contains", Value: "does-not-appear"}}},
				{ID: 2, Priority: 0, MatchType: "all", ActionValue: "first",
					Conditions: []Condition{{Field: "to", Op: "contains", Value: "example"}}},
				{ID: 3, Priority: 10, MatchType: "all", ActionValue: "second",
					Conditions: []Condition{{Field: "subject", Op: "contains", Value: "Report"}}},
			},
			want: "first", // Match trusts caller ordering (ListActiveRules sorts by priority ASC before calling)
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Match(msg, tc.rules)
			gotVal := ""
			if got != nil {
				gotVal = got.ActionValue
			}
			if gotVal != tc.want {
				t.Errorf("Match() = %q, want %q", gotVal, tc.want)
			}
		})
	}
}
