package email

import (
	"context"
	"fmt"

	"github.com/ghostersk/gowebmail/internal/jmap"
	gomailModels "github.com/ghostersk/gowebmail/internal/models"
)

// SendMessageJMAP sends via the account's JMAP server instead of SMTP — used
// for ProviderJMAP accounts. Builds the same RFC822 body as SendMessageFull
// (optionally signed/encrypted via signer), then uploads + imports + submits
// it over JMAP; the import into Sent replaces SMTP's separate append-to-Sent
// step, since JMAP's Email/import already files the message.
func SendMessageJMAP(ctx context.Context, account *gomailModels.EmailAccount, req *gomailModels.ComposeRequest, signer Signer) error {
	rawMsg, err := BuildRawMessage(account, req, signer)
	if err != nil {
		return err
	}
	jc := jmap.New(account.IMAPHost, account.EmailAddress, account.AccessToken)
	sentID, err := jc.FindMailboxByRole(ctx, "sent")
	if err != nil {
		return fmt.Errorf("jmap find Sent folder: %w", err)
	}
	return jc.Send(ctx, sentID, rawMsg)
}
