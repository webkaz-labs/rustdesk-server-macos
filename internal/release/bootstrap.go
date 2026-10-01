package release

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
)

var (
	bootstrapVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)
	bootstrapCommitPattern  = regexp.MustCompile(`^[0-9a-f]{40}$`)
	bootstrapRepoPattern    = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
)

// ValidateVersion accepts a path-safe semantic version without a leading v.
func ValidateVersion(version string) error {
	if !bootstrapVersionPattern.MatchString(version) {
		return errors.New("expected a safe semver version without a leading v")
	}
	beforeBuild, _, _ := strings.Cut(version, "+")
	_, prerelease, present := strings.Cut(beforeBuild, "-")
	if present {
		for _, identifier := range strings.Split(prerelease, ".") {
			numeric := true
			for _, c := range identifier {
				if c < '0' || c > '9' {
					numeric = false
					break
				}
			}
			if numeric && len(identifier) > 1 && identifier[0] == '0' {
				return errors.New("numeric semver prerelease identifiers cannot have leading zeros")
			}
		}
	}
	return nil
}

// ValidateTag accepts a v-prefixed, path-safe semantic version.
func ValidateTag(tag string) error {
	if !strings.HasPrefix(tag, "v") {
		return errors.New("release tag must start with v")
	}
	return ValidateVersion(strings.TrimPrefix(tag, "v"))
}

// ValidateBootstrapRequest restricts bootstrap to a manually dispatched main run
// and its exact workflow commit, before any release-side mutation is possible.
func ValidateBootstrapRequest(tag, tested, event, ref, workflowSHA string) error {
	if err := ValidateTag(tag); err != nil {
		return err
	}
	if event != "workflow_dispatch" || ref != "refs/heads/main" {
		return errors.New("release bootstrap must be dispatched on main")
	}
	if !bootstrapCommitPattern.MatchString(tested) || tested != workflowSHA {
		return errors.New("tested_commit must equal this workflow's exact 40-character github.sha")
	}
	return nil
}

// Bootstrap verifies the requested source commit's native CI, creates a missing
// release tag without ever replacing an existing ref, then explicitly dispatches
// ci-release.yml on that tag. It uses the authenticated gh CLI already in CI.
func Bootstrap(root, tag, tested string) error {
	client := bootstrapClient{run: bootstrapCommand, getenv: os.Getenv, output: os.Stdout}
	return client.bootstrap(root, tag, tested)
}

// Inject process execution rather than an HTTP client so tests also verify the
// exact gh methods and arguments, without executing git or mutating GitHub.
type bootstrapClient struct {
	run    func(cwd, name string, args ...string) ([]byte, error)
	getenv func(string) string
	output io.Writer
}

func bootstrapCommand(cwd, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = cwd
	result, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && len(exit.Stderr) > 0 {
			return nil, fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(exit.Stderr)))
		}
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return result, nil
}

func (c bootstrapClient) api(path string, fields map[string]string, response any) error {
	method := "GET"
	if fields != nil {
		method = "POST"
	}
	args := []string{"api", path, "--method", method}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		args = append(args, "-f", key+"="+fields[key])
	}
	raw, err := c.run("", "gh", args...)
	if err != nil {
		return fmt.Errorf("GitHub %s %s: %w", method, path, err)
	}
	raw = []byte(strings.TrimSpace(string(raw)))
	// Dispatch returns HTTP 204 and no JSON body. Read operations and tag
	// creation must still return usable JSON instead of silently succeeding.
	if len(raw) == 0 {
		if response == nil {
			return nil
		}
		return fmt.Errorf("GitHub %s %s returned no JSON", method, path)
	}
	if response == nil {
		if !json.Valid(raw) {
			return fmt.Errorf("GitHub %s %s returned invalid JSON", method, path)
		}
		return nil
	}
	if string(raw) == "null" {
		return fmt.Errorf("GitHub %s %s returned null", method, path)
	}
	if err := json.Unmarshal(raw, response); err != nil {
		return fmt.Errorf("decode GitHub %s %s: %w", method, path, err)
	}
	return nil
}

type bootstrapGitObject struct {
	Type string `json:"type"`
	SHA  string `json:"sha"`
}

type bootstrapGitRef struct {
	Ref    string             `json:"ref"`
	Object bootstrapGitObject `json:"object"`
}

func (c bootstrapClient) ensureReleaseTag(tag, commit, repository string) error {
	if err := ValidateTag(tag); err != nil {
		return err
	}
	if !bootstrapCommitPattern.MatchString(commit) {
		return errors.New("invalid exact source commit")
	}
	if !bootstrapRepoPattern.MatchString(repository) {
		return errors.New("invalid GitHub repository")
	}
	endpoint := "repos/" + repository + "/git"
	ref := "refs/tags/" + tag
	var refs []bootstrapGitRef
	if err := c.api(endpoint+"/matching-refs/tags/"+tag, nil, &refs); err != nil {
		return err
	}
	var exact *bootstrapGitRef
	for i := range refs {
		if !strings.HasPrefix(refs[i].Ref, "refs/tags/") {
			return errors.New("unexpected GitHub matching refs response")
		}
		if refs[i].Ref == ref {
			if exact != nil {
				return errors.New("ambiguous tag response")
			}
			exact = &refs[i]
		}
	}
	if exact == nil {
		// A concurrent creation fails safely. Deliberately no PATCH, force,
		// update-ref, deletion, or automatic creation retry is available.
		var created bootstrapGitRef
		if err := c.api(endpoint+"/refs", map[string]string{"ref": ref, "sha": commit}, &created); err != nil {
			return err
		}
		if created.Ref != ref {
			return errors.New("GitHub returned a different created ref")
		}
		exact = &created
	}
	obj := exact.Object
	visited := make(map[string]bool)
	for range 16 {
		if !bootstrapCommitPattern.MatchString(obj.SHA) {
			return errors.New("malformed tag object SHA")
		}
		if obj.Type == "commit" {
			if obj.SHA != commit {
				return fmt.Errorf("existing release tag targets %s, not workflow commit %s", obj.SHA, commit)
			}
			return nil
		}
		if obj.Type != "tag" || visited[obj.SHA] {
			return errors.New("release tag does not resolve safely to a commit")
		}
		visited[obj.SHA] = true
		var peeled bootstrapGitRef
		if err := c.api(endpoint+"/tags/"+obj.SHA, nil, &peeled); err != nil {
			return err
		}
		obj = peeled.Object
	}
	return errors.New("release tag has excessive annotated-tag nesting")
}

type bootstrapWorkflowRun struct {
	ID         int64  `json:"id"`
	HeadSHA    string `json:"head_sha"`
	HeadBranch string `json:"head_branch"`
	Event      string `json:"event"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}

type bootstrapWorkflowJob struct {
	Name       string   `json:"name"`
	Status     string   `json:"status"`
	Conclusion string   `json:"conclusion"`
	HeadSHA    string   `json:"head_sha"`
	Labels     []string `json:"labels"`
}

func (c bootstrapClient) successfulNativeRun(repository, commit string) (int64, error) {
	if !bootstrapRepoPattern.MatchString(repository) || !bootstrapCommitPattern.MatchString(commit) {
		return 0, errors.New("invalid GitHub repository or exact source commit")
	}
	endpoint := "repos/" + repository + "/actions"
	var runs struct {
		WorkflowRuns []bootstrapWorkflowRun `json:"workflow_runs"`
	}
	if err := c.api(endpoint+"/workflows/ci-release.yml/runs?head_sha="+commit+"&branch=main&event=push&status=success&per_page=100", nil, &runs); err != nil {
		return 0, err
	}
	for _, run := range runs.WorkflowRuns {
		if run.HeadSHA != commit || run.HeadBranch != "main" || run.Event != "push" || run.Status != "completed" || run.Conclusion != "success" {
			continue
		}
		if run.ID <= 0 {
			return 0, errors.New("malformed workflow run ID")
		}
		var response struct {
			TotalCount *int                   `json:"total_count"`
			Jobs       []bootstrapWorkflowJob `json:"jobs"`
		}
		if err := c.api(fmt.Sprintf("%s/runs/%d/jobs?filter=latest&per_page=100", endpoint, run.ID), nil, &response); err != nil {
			return 0, err
		}
		if len(response.Jobs) > 100 || (response.TotalCount != nil && *response.TotalCount > 100) {
			return 0, errors.New("unexpectedly large CI job list; inspect before releasing")
		}
		unitPassed := false
		nativeLabels := make(map[string]bool)
		for _, job := range response.Jobs {
			if job.Status != "completed" || job.Conclusion != "success" || job.HeadSHA != commit {
				continue
			}
			if job.Name == "unit" {
				unitPassed = true
			}
			if strings.HasPrefix(job.Name, "native (") {
				for _, label := range job.Labels {
					nativeLabels[label] = true
				}
			}
		}
		if unitPassed && nativeLabels["macos-26"] {
			return run.ID, nil
		}
	}
	return 0, errors.New("no successful main/push CI run with passing unit and native Apple Silicon macOS 26 jobs exists for tested_commit")
}

func (c bootstrapClient) bootstrap(root, tag, tested string) error {
	if err := ValidateBootstrapRequest(tag, tested, c.getenv("GITHUB_EVENT_NAME"), c.getenv("GITHUB_REF"), c.getenv("GITHUB_SHA")); err != nil {
		return err
	}
	repository := c.getenv("GITHUB_REPOSITORY")
	if !bootstrapRepoPattern.MatchString(repository) {
		return errors.New("invalid GitHub repository")
	}
	checkout, err := c.run(root, "git", "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(checkout)) != tested {
		return errors.New("bootstrap checkout does not equal tested_commit")
	}
	// Every local check and successful-CI proof precedes the first mutation.
	runID, err := c.successfulNativeRun(repository, tested)
	if err != nil {
		return err
	}
	if err := c.ensureReleaseTag(tag, tested, repository); err != nil {
		return err
	}
	// GITHUB_TOKEN-created tags do not trigger push workflows. GitHub does
	// permit workflow_dispatch with that token, so explicitly request the tag.
	if err := c.api("repos/"+repository+"/actions/workflows/ci-release.yml/dispatches", map[string]string{"ref": tag}, nil); err != nil {
		return err
	}
	if c.output != nil {
		_, err = fmt.Fprintf(c.output, "Verified CI run %d; requested release workflow for %s at %s\n", runID, tag, tested)
	}
	return err
}
