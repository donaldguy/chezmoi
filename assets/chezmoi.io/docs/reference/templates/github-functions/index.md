# GitHub functions

The `gitHub*` template functions return data from GitHub or GitHub Enterprise
using the GitHub API.

All functions take a *host-owner-repo* argument of the form:

    [host/]owner/repo

The optional `host` specifies the host and defaults to `github.com` if omitted.
`owner` and `repo` specify the repository owner and name respectively.

By default, chezmoi makes anonymous GitHub API requests, which are subject to
[GitHub's rate
limits](https://docs.github.com/en/rest/overview/resources-in-the-rest-api#rate-limiting)
(currently 60 requests per hour per source IP address). chezmoi caches results
from identical GitHub API requests for the period defined in
`gitHub.refreshPeriod` (default one minute).

For `github.com` repos, if any of the environment variables

 * `$CHEZMOI_GITHUB_ACCESS_TOKEN`
 * `$CHEZMOI_GITHUB_TOKEN`
 * `$GITHUB_ACCESS_TOKEN`
 * `$GITHUB_TOKEN`

are found, then the first one found will be used to
authenticate the GitHub API requests which have a higher rate limit (currently
5,000 requests per hour per user).

In practice, GitHub API rate limits are high enough chezmoi's caching of results
mean that you should rarely need to set a token, unless you are sharing a source
IP address with many other GitHub users or accessing a private repo. If needed,
the GitHub documentation describes how to [create a personal access
token](https://docs.github.com/en/github/authenticating-to-github/creating-a-personal-access-token).

For non-`github.com` repos, e.g. self-hosted GitHub Enterprise repos, if any of
the environment variables

 * `$CHEZMOI_HOST_ACCESS_TOKEN`
 * `$HOST_ACCESS_TOKEN`

are found then the first one will be used to authenticate requests, where `HOST`
is the host converted to uppercase and with all non-letter characters replaced
with underscores.

!!! example

    Given the host `git.example.com`, chezmoi will look for the
    `$CHEZMOI_GIT_EXAMPLE_COM_ACCESS_TOKEN` and `$GIT_EXAMPLE_COM_ACCESS_TOKEN`
    environment variables.
