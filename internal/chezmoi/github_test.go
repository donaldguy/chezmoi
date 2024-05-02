package chezmoi

import (
	"testing"

	"github.com/alecthomas/assert/v2"
)

func TestAccessTokenEnvKeys(t *testing.T) {
	for _, tc := range []struct {
		host     string
		expected []string
	}{
		{
			host: "github.com",
			expected: []string{
				"CHEZMOI_GITHUB_ACCESS_TOKEN",
				"CHEZMOI_GITHUB_TOKEN",
				"GITHUB_ACCESS_TOKEN",
				"GITHUB_TOKEN",
			},
		},
		{
			host: "git.example.com",
			expected: []string{
				"CHEZMOI_GIT_EXAMPLE_COM_ACCESS_TOKEN",
				"GIT_EXAMPLE_COM_ACCESS_TOKEN",
			},
		},
	} {
		t.Run(tc.host, func(t *testing.T) {
			assert.Equal(t, tc.expected, accessTokenEnvKeys(tc.host))
		})
	}
}
