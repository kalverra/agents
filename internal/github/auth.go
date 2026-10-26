package github

import (
	"fmt"
	"net/http"

	ghapi "github.com/cli/go-gh/v2/pkg/api"
	ghauth "github.com/cli/go-gh/v2/pkg/auth"
	"github.com/shurcooL/githubv4"
	"golang.org/x/oauth2"

	"github.com/kalverra/agents/internal/mactls"
)

// ResolveToken finds a GitHub token using gh conventions: GH_TOKEN/GITHUB_TOKEN
// env vars, then the gh config file, then the system keyring via the gh CLI.
func ResolveToken() (string, error) {
	host, _ := ghauth.DefaultHost()
	if t, _ := ghauth.TokenForHost(host); t != "" {
		return t, nil
	}
	return "", fmt.Errorf("no GitHub token found; set GITHUB_TOKEN, GH_TOKEN, or run `gh auth login`")
}

// NewClient creates a GitHub GraphQL client authenticated with the given token.
func NewClient(token string) *githubv4.Client {
	src := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	oauthTransport := &oauth2.Transport{Source: src}
	if base := mactls.RoundTripper(); base != nil {
		oauthTransport = &oauth2.Transport{Source: src, Base: base}
	}
	return githubv4.NewClient(&http.Client{Transport: oauthTransport})
}

// NewAuthenticatedClient resolves a token and returns a ready-to-use GraphQL client.
func NewAuthenticatedClient() (*githubv4.Client, error) {
	token, err := ResolveToken()
	if err != nil {
		return nil, err
	}
	return NewClient(token), nil
}

// NewRESTClient creates a GitHub REST API client authenticated the same way as
// gh, with the mactls transport as its base so sandboxed environments work.
func NewRESTClient() (*ghapi.RESTClient, error) {
	return ghapi.NewRESTClient(ghapi.ClientOptions{Transport: mactls.RoundTripper()})
}
