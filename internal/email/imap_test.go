package email

import "testing"

// InferFolderType drives how the sync engine classifies each IMAP folder (inbox/sent/drafts/
// trash/spam/archive/custom) — used for default folder discovery, DeleteByUID's trash-move
// target lookup, and rule actions like "mark_as_spam". Covers both the IMAP SPECIAL-USE
// attribute path (authoritative when the server sends it) and the name-guessing fallback.
func TestInferFolderType(t *testing.T) {
	cases := []struct {
		name       string
		folderName string
		attrs      []string
		want       string
	}{
		{"special-use inbox", "Whatever", []string{`\Inbox`}, "inbox"},
		{"special-use sent", "Whatever", []string{`\Sent`}, "sent"},
		{"special-use drafts", "Whatever", []string{`\Drafts`}, "drafts"},
		{"special-use trash", "Whatever", []string{`\Trash`}, "trash"},
		{"special-use deleted alias", "Whatever", []string{`\Deleted`}, "trash"},
		{"special-use junk", "Whatever", []string{`\Junk`}, "spam"},
		{"special-use spam alias", "Whatever", []string{`\Spam`}, "spam"},
		{"special-use archive", "Whatever", []string{`\Archive`}, "archive"},
		{"special-use case-insensitive", "Whatever", []string{`\SENT`}, "sent"},
		{"special-use wins over misleading name", "Trash Talk", []string{`\Sent`}, "sent"},

		{"name INBOX exact", "INBOX", nil, "inbox"},
		{"name lowercase inbox", "inbox", nil, "inbox"},
		{"name contains sent", "Sent Items", nil, "sent"},
		{"name contains draft", "Drafts", nil, "drafts"},
		{"name contains trash", "Trash", nil, "trash"},
		{"name contains deleted", "Deleted Items", nil, "trash"},
		{"name contains spam", "Spam", nil, "spam"},
		{"name contains junk", "Junk E-mail", nil, "spam"},
		{"name contains archive", "Archive", nil, "archive"},
		{"unrecognized name is custom", "Projects", nil, "custom"},
		{"gmail-style path", "[Gmail]/Sent Mail", nil, "sent"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := InferFolderType(tc.folderName, tc.attrs)
			if got != tc.want {
				t.Errorf("InferFolderType(%q, %v) = %q, want %q", tc.folderName, tc.attrs, got, tc.want)
			}
		})
	}
}
