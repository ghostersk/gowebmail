package email

import (
	"context"
	"fmt"

	"github.com/ghostersk/gowebmail/internal/jmap"
	gomailModels "github.com/ghostersk/gowebmail/internal/models"
)

// SaveDraftJMAP saves req as a draft on the account's JMAP server, mirroring
// AppendToDrafts' replace-in-place behavior: if prevID is non-empty that earlier draft
// copy is deleted first (best-effort — a failure there shouldn't block saving the new
// one), then the new message is uploaded + imported into the Drafts mailbox and flagged
// $draft. Returns the new draft's email id.
func SaveDraftJMAP(ctx context.Context, account *gomailModels.EmailAccount, req *gomailModels.ComposeRequest, prevID string) (string, error) {
	rawMsg, err := BuildRawMessage(account, req, nil)
	if err != nil {
		return "", err
	}
	jc := jmap.New(account.IMAPHost, account.EmailAddress, account.AccessToken)
	draftsID, err := jc.FindMailboxByRole(ctx, "drafts")
	if err != nil {
		return "", fmt.Errorf("jmap find Drafts folder: %w", err)
	}
	if prevID != "" {
		_ = jc.DeleteEmail(ctx, prevID)
	}
	blobID, err := jc.UploadBlob(ctx, rawMsg)
	if err != nil {
		return "", fmt.Errorf("jmap upload draft: %w", err)
	}
	newID, err := jc.ImportEmail(ctx, blobID, draftsID)
	if err != nil {
		return "", fmt.Errorf("jmap import draft: %w", err)
	}
	_ = jc.SetKeyword(ctx, newID, "$draft", true)
	return newID, nil
}

// DeleteDraftJMAP deletes a previously-autosaved draft by id.
func DeleteDraftJMAP(ctx context.Context, account *gomailModels.EmailAccount, id string) error {
	if id == "" {
		return nil
	}
	jc := jmap.New(account.IMAPHost, account.EmailAddress, account.AccessToken)
	return jc.DeleteEmail(ctx, id)
}
