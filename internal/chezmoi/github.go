package chezmoi

import (
	"context"
	"net/http"
	"os"

	"github.com/google/go-github/v61/github"
	"golang.org/x/oauth2"
)

// NewGitHubClient returns a new github.Client configured with an access token
// and a http client, if available.
func NewGitHubClient(ctx context.Context, httpClient *http.Client, host string) (*github.Client, error) {
	for _, key := range accessTokenEnvKeys(host) {
		if accessToken := os.Getenv(key); accessToken != "" {
			httpClient = oauth2.NewClient(
				context.WithValue(ctx, oauth2.HTTPClient, httpClient),
				oauth2.StaticTokenSource(&oauth2.Token{
					AccessToken: accessToken,
				}))
			break
		}
	}
	gitHubClient := github.NewClient(httpClient)
	if host == "github.com" {
		return gitHubClient, nil
	}
	return gitHubClient.WithEnterpriseURLs(
		"https://"+host+"/api/v3/",
		"https://"+host+"/api/uploads/",
	)
}

func accessTokenEnvKeys(host string) []string {
	if host == "github.com" {
		return []string{
			"CHEZMOI_GITHUB_ACCESS_TOKEN",
			"CHEZMOI_GITHUB_TOKEN",
			"GITHUB_ACCESS_TOKEN",
			"GITHUB_TOKEN",
		}
	}
	hostKey := makeHostKey(host)
	return []string{
		"CHEZMOI_" + hostKey + "_ACCESS_TOKEN",
		hostKey + "_ACCESS_TOKEN",
	}
}

func makeHostKey(host string) string {
	hostKey := make([]byte, 0, len(host))
	for _, b := range []byte(host) {
		switch {
		case 'A' <= b && b <= 'Z':
			hostKey = append(hostKey, b)
		case 'a' <= b && b <= 'z':
			hostKey = append(hostKey, b-'a'+'A')
		default:
			hostKey = append(hostKey, '_')
		}
	}
	return string(hostKey)
}
