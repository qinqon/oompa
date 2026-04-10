package agent

import (
	"context"
	"fmt"
	"net/http"

	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/google/go-github/v84/github"
)

// GitHubAppAuth holds the GitHub App authentication state.
type GitHubAppAuth struct {
	Client    *GoGitHubClient
	TokenFunc func(context.Context) (string, error)
	Login     string
	Name      string
	Email     string
}

// NewGitHubAppAuth creates a GitHub client and token provider from GitHub App credentials.
// It uses the App's private key to generate JWTs and exchange them for installation tokens.
// The returned TokenFunc provides short-lived installation tokens (valid ~1 hour, auto-refreshed).
func NewGitHubAppAuth(appID, installationID int64, privateKeyPath string) (*GitHubAppAuth, error) {
	// Create app-level transport (JWT) to fetch app metadata
	appTransport, err := ghinstallation.NewAppsTransportKeyFromFile(http.DefaultTransport, appID, privateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("creating app transport: %w", err)
	}

	appClient := github.NewClient(&http.Client{Transport: appTransport})
	app, _, err := appClient.Apps.Get(context.Background(), "")
	if err != nil {
		return nil, fmt.Errorf("getting app info: %w", err)
	}

	// Create installation-level transport (auto-refreshing installation tokens)
	itr, err := ghinstallation.NewKeyFromFile(http.DefaultTransport, appID, installationID, privateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("creating installation transport: %w", err)
	}

	client := NewGoGitHubClientFromHTTPClient(&http.Client{Transport: itr})

	slug := app.GetSlug()
	login := fmt.Sprintf("%s[bot]", slug)
	name := app.GetName()
	email := fmt.Sprintf("%d+%s@users.noreply.github.com", appID, slug)

	return &GitHubAppAuth{
		Client:    client,
		TokenFunc: func(ctx context.Context) (string, error) { return itr.Token(ctx) },
		Login:     login,
		Name:      name,
		Email:     email,
	}, nil
}
