package discobox

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

var (
	repositoryName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}/[A-Za-z0-9_.-]{1,100}$`)
	repositoryRef  = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_./-]{0,127}$`)
	gitCommit      = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

func ValidRepository(name string) bool {
	_, repo, _ := strings.Cut(name, "/")
	return repositoryName.MatchString(name) && repo != "." && repo != ".." && !strings.HasSuffix(repo, ".git")
}

func ValidBranch(branch string) bool {
	if !repositoryRef.MatchString(branch) || strings.Contains(branch, "..") {
		return false
	}
	for part := range strings.SplitSeq(branch, "/") {
		if part == "" || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".") || strings.HasSuffix(part, ".lock") {
			return false
		}
	}
	return true
}

type RepositorySource struct {
	Repository string `json:"repository" jsonschema:"GitHub owner/repository, without a URL or credentials."`
	Branch     string `json:"branch" jsonschema:"Explicit branch, for example main."`
	Commit     string `json:"commit,omitempty" jsonschema:"Optional immutable 40-character SHA. Omit to resolve a public branch anonymously."`
}

func (r RepositorySource) Validate() error {
	if !ValidRepository(r.Repository) || !ValidBranch(r.Branch) || (r.Commit != "" && !gitCommit.MatchString(r.Commit)) {
		return errors.New("source requires GitHub owner/repository, a valid branch, and an optional lowercase 40-character commit")
	}
	return nil
}

func (r RepositorySource) config(ctx context.Context) (map[string]any, error) {
	remote := "https://github.com/" + r.Repository + ".git"
	ref := "refs/heads/" + r.Branch
	if r.Commit == "" {
		// This path deliberately uses anonymous HTTPS. A checkout pin supplied
		// by the caller can still be delivered by a credentialed host client.
		cmd := exec.CommandContext(ctx, "git", "-c", "credential.helper=", "-c", "core.askPass=", "-c", "http.followRedirects=false",
			"ls-remote", "--exit-code", "--refs", remote, ref)
		cmd.Dir = os.TempDir()
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_COUNT=0",
			"GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "GIT_CONFIG_PARAMETERS=", "GIT_DIR=", "GIT_WORK_TREE=")
		// Raw Git diagnostics may contain credential helper output or URLs.
		out, err := cmd.Output()
		fields := strings.Fields(string(out))
		if err != nil || len(fields) != 2 || fields[1] != ref || !gitCommit.MatchString(fields[0]) {
			return nil, errors.New("cannot resolve the public source branch anonymously. Supply its commit SHA for a pinned launch, or use the Discobox console on a host with repository access")
		}
		r.Commit = fields[0]
	}
	return map[string]any{
		"kind": "git", "url": remote, "slug": "primary",
		"checkout":    map[string]string{"commit": r.Commit, "refName": r.Branch, "refType": "branch"},
		"destination": map[string]string{"directory": "/workspace/source", "workingDirectory": "/workspace/source"},
	}, nil
}

func httpStatus(err error, codes ...int) bool {
	var status *HTTPError
	if errors.As(err, &status) {
		for _, code := range codes {
			if status.Status == code {
				return true
			}
		}
	}
	return false
}

func limitedRead(err error) bool {
	return httpStatus(err, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusNotImplemented)
}
