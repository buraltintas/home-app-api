package email

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"
)

// SenderOptions carries the provider settings needed to build a Sender.
//
// The API process and the standalone worker both drain the same outbox, so they must
// agree on how mail leaves the system. Keeping the decision in one place is what stops
// the two entrypoints from drifting into different providers or different defaults.
type SenderOptions struct {
	Provider                string
	DevelopmentDir          string
	APIURL, APIKey          string
	GmailServiceAccountJSON string
	GmailServiceAccountFile string
	GmailImpersonatedUser   string
	GmailAPIURL             string
}

// NewSender builds the configured Sender. Anything other than a known remote provider
// writes to disk, which keeps local development from needing credentials at all.
func NewSender(ctx context.Context, options SenderOptions) (Sender, error) {
	switch options.Provider {
	case "resend":
		url := options.APIURL
		if url == "" {
			url = "https://api.resend.com/emails"
		}
		return &ResendSender{URL: url, APIKey: options.APIKey, Client: &http.Client{Timeout: 10 * time.Second}}, nil
	case "gmail":
		credentials := []byte(options.GmailServiceAccountJSON)
		if len(credentials) == 0 {
			if options.GmailServiceAccountFile == "" {
				return nil, errors.New("GMAIL_SERVICE_ACCOUNT_FILE or GMAIL_SERVICE_ACCOUNT_JSON is required")
			}
			read, err := os.ReadFile(options.GmailServiceAccountFile)
			if err != nil {
				return nil, fmt.Errorf("read gmail credentials: %w", err)
			}
			credentials = read
		}
		return NewGmailSender(ctx, credentials, options.GmailImpersonatedUser, options.GmailAPIURL)
	default:
		return FileSender{Dir: options.DevelopmentDir}, nil
	}
}
