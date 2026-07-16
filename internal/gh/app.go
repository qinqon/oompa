package gh

import (
	"context"
	"fmt"
	"net/http"

	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/google/go-github/v88/github"
)

// AppAuth holds the GitHub App authentication state.
type AppAuth struct {
	Client    *RESTClient
	TokenFunc func(context.Context) (string, error)
	Login     string
	Name      string
	Email     string
}

// NewAppAuth creates a GitHub client and token provider from GitHub App credentials.
// It uses the App's private key to generate JWTs and exchange them for installation tokens.
// The returned TokenFunc provides short-lived installation tokens (valid ~1 hour, auto-refreshed).
func NewAppAuth(appID, installationID int64, privateKey []byte) (*AppAuth, error) {
	// Create app-level transport (JWT) to fetch app metadata
	appTransport, err := ghinstallation.NewAppsTransport(http.DefaultTransport, appID, privateKey)
	if err != nil {
		return nil, fmt.Errorf("creating app transport: %w", err)
	}

	appClient, err := github.NewClient(github.WithTransport(appTransport))
	if err != nil {
		return nil, fmt.Errorf("creating app client: %w", err)
	}
	app, _, err := appClient.Apps.Get(context.Background(), "")
	if err != nil {
		return nil, fmt.Errorf("getting app info: %w", err)
	}

	// Create installation-level transport (auto-refreshing installation tokens)
	itr, err := ghinstallation.New(http.DefaultTransport, appID, installationID, privateKey)
	if err != nil {
		return nil, fmt.Errorf("creating installation transport: %w", err)
	}

	client, err := NewRESTClientFromHTTPClient(&http.Client{Transport: itr})
	if err != nil {
		return nil, fmt.Errorf("creating installation client: %w", err)
	}

	slug := app.GetSlug()
	login := fmt.Sprintf("%s[bot]", slug)
	name := app.GetName()

	// Look up the bot's actual user ID for the noreply email.
	// The App ID != the bot user ID; using the App ID would attribute
	// commits to the wrong GitHub user.
	installClient, err := github.NewClient(github.WithTransport(itr))
	if err != nil {
		return nil, fmt.Errorf("creating bot lookup client: %w", err)
	}
	botUser, _, err := installClient.Users.Get(context.Background(), login)
	if err != nil {
		return nil, fmt.Errorf("getting bot user info for %s: %w", login, err)
	}
	email := fmt.Sprintf("%d+%s@users.noreply.github.com", botUser.GetID(), slug)

	return &AppAuth{
		Client:    client,
		TokenFunc: func(ctx context.Context) (string, error) { return itr.Token(ctx) },
		Login:     login,
		Name:      name,
		Email:     email,
	}, nil
}
