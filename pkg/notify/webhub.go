package notify

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/saker-ai/assethub/pkg/config"
	sakernotify "github.com/saker-ai/saker-common/webhub/notify"
)

var (
	globalMu    sync.Mutex
	globalClient *sakernotify.Client
	globalErr   error
)

func newClient(cfg config.WebHubNotifyConfig) (*sakernotify.Client, error) {
	globalMu.Lock()
	defer globalMu.Unlock()
	if globalClient != nil || globalErr != nil {
		if globalErr != nil {
			return nil, globalErr
		}
		return globalClient, nil
	}
	scopes := []string{}
	if strings.TrimSpace(cfg.Scope) != "" {
		scopes = []string{cfg.Scope}
	}
	client, err := sakernotify.NewClient(sakernotify.Config{
		Enabled:     cfg.Enabled,
		WebHubURL:   cfg.WebHubURL,
		WardenURL:   cfg.WardenURL,
		APIKey:      cfg.APIKey,
		Audience:    cfg.Audience,
		Scopes:      scopes,
		HTTPTimeout: 5 * time.Second,
	})
	globalClient = client
	globalErr = err
	return client, err
}

// ResetForTesting clears the singleton client. Only intended for tests.
func ResetForTesting() {
	globalMu.Lock()
	defer globalMu.Unlock()
	globalClient = nil
	globalErr = nil
}

// ReviewCreatedEvent is the payload assethub emits when a human review task
// is created and needs reviewer attention.
type ReviewCreatedEvent struct {
	ReviewID    string
	TenantID    string
	Title       string
	Reviewer    string
	ReferenceID string
	AssetIDs    []string
}

// ReviewCreatedFunc is invoked after a review task is successfully created.
type ReviewCreatedFunc func(event ReviewCreatedEvent)

// NewReviewCreatedHook returns a ReviewCreatedFunc that emits a WebHub
// notification when an assethub review task is created. When WebHub notify is
// disabled, the hook is still non-nil but does nothing.
func NewReviewCreatedHook(cfg config.WebHubNotifyConfig, logger *slog.Logger) (ReviewCreatedFunc, error) {
	if !cfg.Enabled {
		return func(ReviewCreatedEvent) {}, nil
	}
	client, err := newClient(cfg)
	if err != nil {
		return nil, err
	}
	if logger != nil {
		logger.Info("assethub webhub notifier enabled", "webhub_url", cfg.WebHubURL)
	}
	return func(event ReviewCreatedEvent) {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			body := "Review task " + event.ReviewID + " needs attention."
			if len(event.AssetIDs) > 0 {
				body += " Assets: " + strings.Join(event.AssetIDs, ", ") + "."
			}
			if event.Reviewer != "" {
				body += " Reviewer: " + event.Reviewer + "."
			}
			href := "/assets"
			if event.ReferenceID != "" {
				href = "/assets/" + event.ReferenceID
			}
			notifyErr := client.Notify(ctx, sakernotify.Event{
				Type:      "assethub.review_required",
				Title:     event.Title,
				Body:      body,
				TenantID:  event.TenantID,
				SourceApp: "assethub",
				Severity:  "warning",
				Href:      href,
			})
			if notifyErr != nil && logger != nil {
				logger.Warn("assethub notify failed", "error", notifyErr, "type", "assethub.review_required")
			}
		}()
	}, nil
}
