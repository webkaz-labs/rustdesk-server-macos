package release

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

const (
	bootstrapTestSHA  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	bootstrapTestTag  = "v0.1.0"
	bootstrapTestRepo = "webkaz-labs/rustdesk-server-macos"
)

type bootstrapTestCall struct {
	cwd  string
	name string
	args []string
}

type bootstrapTestReply struct {
	value any
	err   error
}

// A string reply is raw process output; other values are JSON-encoded. Every
// command is mocked, including git, so these tests never use credentials or the
// network and cannot create tags or dispatch a real workflow.
func bootstrapTestClient(t *testing.T, replies ...bootstrapTestReply) (bootstrapClient, *[]bootstrapTestCall, *bytes.Buffer) {
	t.Helper()
	calls := []bootstrapTestCall{}
	out := new(bytes.Buffer)
	env := map[string]string{
		"GITHUB_EVENT_NAME": "workflow_dispatch", "GITHUB_REF": "refs/heads/main",
		"GITHUB_SHA": bootstrapTestSHA, "GITHUB_REPOSITORY": bootstrapTestRepo,
	}
	c := bootstrapClient{
		getenv: func(key string) string { return env[key] },
		output: out,
		run: func(cwd, name string, args ...string) ([]byte, error) {
			calls = append(calls, bootstrapTestCall{cwd, name, append([]string(nil), args...)})
			if len(calls) > len(replies) {
				t.Fatalf("unexpected command %s %q", name, args)
			}
			reply := replies[len(calls)-1]
			if reply.err != nil {
				return nil, reply.err
			}
			if raw, ok := reply.value.(string); ok {
				return []byte(raw), nil
			}
			raw, err := json.Marshal(reply.value)
			if err != nil {
				t.Fatal(err)
			}
			return raw, nil
		},
	}
	return c, &calls, out
}

func bootstrapTestRef(kind, sha string) bootstrapGitRef {
	return bootstrapGitRef{Ref: "refs/tags/" + bootstrapTestTag, Object: bootstrapGitObject{Type: kind, SHA: sha}}
}

func bootstrapTestRuns() map[string]any {
	return map[string]any{"workflow_runs": []bootstrapWorkflowRun{{
		ID: 123, HeadSHA: bootstrapTestSHA, HeadBranch: "main",
		Event: "push", Status: "completed", Conclusion: "success",
	}}}
}

func bootstrapTestJobs() []bootstrapWorkflowJob {
	return []bootstrapWorkflowJob{
		{Name: "unit", Status: "completed", Conclusion: "success", HeadSHA: bootstrapTestSHA, Labels: []string{"ubuntu-24.04"}},
		{Name: "native (macos-26, arm64, 1)", Status: "completed", Conclusion: "success", HeadSHA: bootstrapTestSHA, Labels: []string{"macos-26"}},
	}
}

func bootstrapAssertReadOnly(t *testing.T, calls []bootstrapTestCall) {
	t.Helper()
	for _, call := range calls {
		if call.name == "gh" && (len(call.args) != 4 || call.args[2] != "--method" || call.args[3] != "GET") {
			t.Fatalf("unexpected mutation: %+v", call)
		}
	}
}

func TestValidateVersion(t *testing.T) {
	for _, version := range []string{"0.1.0", "1.1.16", "0.0.0-dev.123", "1.2.3-rc.1", "1.2.3+build.4", "1.2.3-beta-1", "1.2.3-0", "1.2.3-01a", "1.2.3+001"} {
		t.Run("valid/"+version, func(t *testing.T) {
			if err := ValidateVersion(version); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, version := range []string{"", "../oops", "1.2.3\n", "v1.2.3", "01.2.3", "1.02.3", "1.2.03", "$(whoami)", "1.2", "1.2.3/evil", "1.2.3-01", "1.2.3-rc.01", "1.2.3-rc..1", "1.2.3+", "1.2.3-é", "1.2.3\x00", " 1.2.3", "1.2.3 "} {
		t.Run("invalid/"+version, func(t *testing.T) {
			if err := ValidateVersion(version); err == nil {
				t.Fatalf("accepted unsafe version %q", version)
			}
		})
	}
}

func TestValidateBootstrapRequest(t *testing.T) {
	if err := ValidateBootstrapRequest(bootstrapTestTag, bootstrapTestSHA, "workflow_dispatch", "refs/heads/main", bootstrapTestSHA); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, tag, tested, event, ref, sha string
	}{
		{"push", bootstrapTestTag, bootstrapTestSHA, "push", "refs/heads/main", bootstrapTestSHA},
		{"feature", bootstrapTestTag, bootstrapTestSHA, "workflow_dispatch", "refs/heads/feature", bootstrapTestSHA},
		{"tag", bootstrapTestTag, bootstrapTestSHA, "workflow_dispatch", "refs/tags/v0.1.0", bootstrapTestSHA},
		{"different-sha", bootstrapTestTag, bootstrapTestSHA, "workflow_dispatch", "refs/heads/main", strings.Repeat("b", 40)},
		{"short-sha", bootstrapTestTag, "abcd", "workflow_dispatch", "refs/heads/main", "abcd"},
		{"upper-sha", bootstrapTestTag, strings.Repeat("A", 40), "workflow_dispatch", "refs/heads/main", strings.Repeat("A", 40)},
		{"no-v", "0.1.0", bootstrapTestSHA, "workflow_dispatch", "refs/heads/main", bootstrapTestSHA},
		{"unsafe-tag", "v../oops", bootstrapTestSHA, "workflow_dispatch", "refs/heads/main", bootstrapTestSHA},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateBootstrapRequest(tc.tag, tc.tested, tc.event, tc.ref, tc.sha); err == nil {
				t.Fatal("accepted invalid bootstrap request")
			}
		})
	}
}

func TestBootstrapAPIUsesExplicitMethodsAndAcceptsNoContent(t *testing.T) {
	c, calls, _ := bootstrapTestClient(t, bootstrapTestReply{value: `{ "answer": 42 }`}, bootstrapTestReply{value: "\n"})
	var response map[string]int
	if err := c.api("example/read", nil, &response); err != nil {
		t.Fatal(err)
	}
	if response["answer"] != 42 {
		t.Fatal(response)
	}
	if err := c.api("example/dispatches", map[string]string{"ref": bootstrapTestTag}, nil); err != nil {
		t.Fatal(err)
	}
	want := []bootstrapTestCall{
		{"", "gh", []string{"api", "example/read", "--method", "GET"}},
		{"", "gh", []string{"api", "example/dispatches", "--method", "POST", "-f", "ref=" + bootstrapTestTag}},
	}
	if !reflect.DeepEqual(*calls, want) {
		t.Fatalf("calls = %#v, want %#v", *calls, want)
	}
}

func TestBootstrapAPIRejectsMalformedResponsesAndCommandFailures(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reply bootstrapTestReply
	}{
		{"empty", bootstrapTestReply{value: ""}},
		{"null", bootstrapTestReply{value: "null"}},
		{"invalid-json", bootstrapTestReply{value: "{broken"}},
		{"wrong-shape", bootstrapTestReply{value: "[]"}},
		{"failed-command", bootstrapTestReply{err: errors.New("gh failed")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _, _ := bootstrapTestClient(t, tc.reply)
			var response bootstrapGitRef
			if err := c.api("example/read", nil, &response); err == nil {
				t.Fatal("accepted malformed response")
			}
		})
	}
}

func TestBootstrapCreateOnlyAbsentExactTag(t *testing.T) {
	for _, refs := range [][]bootstrapGitRef{
		{},
		{{Ref: "refs/tags/" + bootstrapTestTag + "-rc.1", Object: bootstrapGitObject{Type: "commit", SHA: strings.Repeat("b", 40)}}},
	} {
		c, calls, _ := bootstrapTestClient(t, bootstrapTestReply{value: refs}, bootstrapTestReply{value: bootstrapTestRef("commit", bootstrapTestSHA)})
		if err := c.ensureReleaseTag(bootstrapTestTag, bootstrapTestSHA, bootstrapTestRepo); err != nil {
			t.Fatal(err)
		}
		want := []string{"api", "repos/" + bootstrapTestRepo + "/git/refs", "--method", "POST", "-f", "ref=refs/tags/" + bootstrapTestTag, "-f", "sha=" + bootstrapTestSHA}
		if len(*calls) != 2 || !reflect.DeepEqual((*calls)[1].args, want) {
			t.Fatalf("unexpected creation commands: %+v", *calls)
		}
	}
}

func TestBootstrapExistingMatchingLightweightTagIsReadOnly(t *testing.T) {
	c, calls, _ := bootstrapTestClient(t, bootstrapTestReply{value: []bootstrapGitRef{bootstrapTestRef("commit", bootstrapTestSHA)}})
	if err := c.ensureReleaseTag(bootstrapTestTag, bootstrapTestSHA, bootstrapTestRepo); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 {
		t.Fatal(*calls)
	}
	bootstrapAssertReadOnly(t, *calls)
}

func TestBootstrapAnnotatedTagIsPeeledAndChecked(t *testing.T) {
	annotatedSHA := strings.Repeat("b", 40)
	c, calls, _ := bootstrapTestClient(t,
		bootstrapTestReply{value: []bootstrapGitRef{bootstrapTestRef("tag", annotatedSHA)}},
		bootstrapTestReply{value: bootstrapTestRef("commit", bootstrapTestSHA)},
	)
	if err := c.ensureReleaseTag(bootstrapTestTag, bootstrapTestSHA, bootstrapTestRepo); err != nil {
		t.Fatal(err)
	}
	if (*calls)[1].args[1] != "repos/"+bootstrapTestRepo+"/git/tags/"+annotatedSHA {
		t.Fatal(*calls)
	}
	bootstrapAssertReadOnly(t, *calls)
}

func TestBootstrapMismatchedAndNoncommitTagsAreNeverChanged(t *testing.T) {
	for _, obj := range []bootstrapGitRef{
		bootstrapTestRef("commit", strings.Repeat("b", 40)),
		bootstrapTestRef("tree", bootstrapTestSHA),
		bootstrapTestRef("blob", bootstrapTestSHA),
		bootstrapTestRef("commit", "short"),
		bootstrapTestRef("", bootstrapTestSHA),
	} {
		c, calls, _ := bootstrapTestClient(t, bootstrapTestReply{value: []bootstrapGitRef{obj}})
		if err := c.ensureReleaseTag(bootstrapTestTag, bootstrapTestSHA, bootstrapTestRepo); err == nil {
			t.Fatalf("accepted invalid tag: %+v", obj)
		}
		bootstrapAssertReadOnly(t, *calls)
	}
}

func TestBootstrapTagValidationPrecedesCommands(t *testing.T) {
	for _, tc := range [][3]string{
		{"../oops", bootstrapTestSHA, bootstrapTestRepo},
		{bootstrapTestTag, "short", bootstrapTestRepo},
		{bootstrapTestTag, bootstrapTestSHA, "owner/repo/extra"},
	} {
		c, calls, _ := bootstrapTestClient(t)
		if err := c.ensureReleaseTag(tc[0], tc[1], tc[2]); err == nil {
			t.Fatal("accepted invalid tag request")
		}
		if len(*calls) != 0 {
			t.Fatal(*calls)
		}
	}
}

func TestBootstrapMalformedMatchingRefsFailClosed(t *testing.T) {
	for _, response := range []any{
		"{}", "null", "", "[null]",
		[]bootstrapGitRef{bootstrapTestRef("commit", bootstrapTestSHA), bootstrapTestRef("commit", bootstrapTestSHA)},
	} {
		c, calls, _ := bootstrapTestClient(t, bootstrapTestReply{value: response})
		if err := c.ensureReleaseTag(bootstrapTestTag, bootstrapTestSHA, bootstrapTestRepo); err == nil {
			t.Fatalf("accepted invalid refs response: %#v", response)
		}
		bootstrapAssertReadOnly(t, *calls)
	}
}

func TestBootstrapCreationFailureNeverRetriesOrForces(t *testing.T) {
	for _, reply := range []bootstrapTestReply{
		{err: errors.New("HTTP 422: ref already exists")},
		{value: bootstrapGitRef{Ref: "refs/tags/v9.9.9", Object: bootstrapGitObject{Type: "commit", SHA: bootstrapTestSHA}}},
		{value: bootstrapTestRef("commit", strings.Repeat("b", 40))},
	} {
		c, calls, _ := bootstrapTestClient(t, bootstrapTestReply{value: "[]"}, reply)
		if err := c.ensureReleaseTag(bootstrapTestTag, bootstrapTestSHA, bootstrapTestRepo); err == nil {
			t.Fatal("accepted invalid creation result")
		}
		if len(*calls) != 2 || (*calls)[1].args[3] != "POST" {
			t.Fatal(*calls)
		}
	}
}

func TestBootstrapAnnotatedTagCyclesAndNestingAreBounded(t *testing.T) {
	t.Run("cycle", func(t *testing.T) {
		ref := bootstrapTestRef("tag", strings.Repeat("b", 40))
		c, calls, _ := bootstrapTestClient(t, bootstrapTestReply{value: []bootstrapGitRef{ref}}, bootstrapTestReply{value: ref})
		if err := c.ensureReleaseTag(bootstrapTestTag, bootstrapTestSHA, bootstrapTestRepo); err == nil || !strings.Contains(err.Error(), "safely") {
			t.Fatalf("expected cycle rejection, got %v", err)
		}
		if len(*calls) != 2 {
			t.Fatal(*calls)
		}
		bootstrapAssertReadOnly(t, *calls)
	})
	for _, depth := range []int{15, 16} {
		t.Run(fmt.Sprint(depth), func(t *testing.T) {
			replies := []bootstrapTestReply{{value: []bootstrapGitRef{bootstrapTestRef("tag", fmt.Sprintf("%040x", 1))}}}
			for i := 1; i <= depth; i++ {
				obj := bootstrapTestRef("tag", fmt.Sprintf("%040x", i+1))
				if i == depth {
					obj = bootstrapTestRef("commit", bootstrapTestSHA)
				}
				replies = append(replies, bootstrapTestReply{value: obj})
			}
			c, calls, _ := bootstrapTestClient(t, replies...)
			err := c.ensureReleaseTag(bootstrapTestTag, bootstrapTestSHA, bootstrapTestRepo)
			if (depth == 15 && err != nil) || (depth == 16 && (err == nil || !strings.Contains(err.Error(), "nesting"))) {
				t.Fatalf("depth %d: %v", depth, err)
			}
			if len(*calls) != depth+1 {
				t.Fatal(*calls)
			}
			bootstrapAssertReadOnly(t, *calls)
		})
	}
}

func TestBootstrapSuccessRequiresUnitAndNativeAppleSiliconRunner(t *testing.T) {
	for _, change := range []string{"none", "skipped", "wrong-commit", "wrong-label", "wrong-name", "unfinished", "no-unit", "failed-unit"} {
		t.Run(change, func(t *testing.T) {
			jobs := bootstrapTestJobs()
			switch change {
			case "skipped":
				jobs[1].Conclusion = "skipped"
			case "wrong-commit":
				jobs[1].HeadSHA = strings.Repeat("b", 40)
			case "wrong-label":
				jobs[1].Labels = []string{"macos-26-intel"}
			case "wrong-name":
				jobs[1].Name = "some-other-job"
			case "unfinished":
				jobs[1].Status = "in_progress"
			case "no-unit":
				jobs = jobs[1:]
			case "failed-unit":
				jobs[0].Conclusion = "failure"
			}
			c, calls, _ := bootstrapTestClient(t, bootstrapTestReply{value: bootstrapTestRuns()}, bootstrapTestReply{value: map[string]any{"jobs": jobs, "total_count": len(jobs)}})
			id, err := c.successfulNativeRun(bootstrapTestRepo, bootstrapTestSHA)
			if change == "none" {
				if err != nil || id != 123 {
					t.Fatalf("id=%d, err=%v", id, err)
				}
			} else if err == nil {
				t.Fatal("accepted missing successful job evidence")
			}
			bootstrapAssertReadOnly(t, *calls)
		})
	}
}

func TestBootstrapRunEvidenceMustMatchExactMainPush(t *testing.T) {
	for _, field := range []string{"sha", "branch", "event", "status", "conclusion", "id"} {
		t.Run(field, func(t *testing.T) {
			runs := bootstrapTestRuns()
			run := runs["workflow_runs"].([]bootstrapWorkflowRun)[0]
			switch field {
			case "sha":
				run.HeadSHA = strings.Repeat("b", 40)
			case "branch":
				run.HeadBranch = "feature"
			case "event":
				run.Event = "workflow_dispatch"
			case "status":
				run.Status = "in_progress"
			case "conclusion":
				run.Conclusion = "failure"
			case "id":
				run.ID = 0
			}
			runs["workflow_runs"] = []bootstrapWorkflowRun{run}
			c, calls, _ := bootstrapTestClient(t, bootstrapTestReply{value: runs})
			if _, err := c.successfulNativeRun(bootstrapTestRepo, bootstrapTestSHA); err == nil {
				t.Fatal("accepted invalid CI run")
			}
			bootstrapAssertReadOnly(t, *calls)
		})
	}
}

func TestBootstrapRejectsOversizedJobList(t *testing.T) {
	c, calls, _ := bootstrapTestClient(t, bootstrapTestReply{value: bootstrapTestRuns()}, bootstrapTestReply{value: map[string]any{"jobs": bootstrapTestJobs(), "total_count": 101}})
	if _, err := c.successfulNativeRun(bootstrapTestRepo, bootstrapTestSHA); err == nil || !strings.Contains(err.Error(), "large CI job list") {
		t.Fatalf("expected pagination rejection: %v", err)
	}
	bootstrapAssertReadOnly(t, *calls)
}

func TestBootstrapChecksCIBeforeTagAndDispatch(t *testing.T) {
	c, calls, out := bootstrapTestClient(t,
		bootstrapTestReply{value: bootstrapTestSHA + "\n"},
		bootstrapTestReply{value: bootstrapTestRuns()},
		bootstrapTestReply{value: map[string]any{"jobs": bootstrapTestJobs(), "total_count": 2}},
		bootstrapTestReply{value: "[]"},
		bootstrapTestReply{value: bootstrapTestRef("commit", bootstrapTestSHA)},
		bootstrapTestReply{value: ""},
	)
	root := t.TempDir()
	if err := c.bootstrap(root, bootstrapTestTag, bootstrapTestSHA); err != nil {
		t.Fatal(err)
	}
	want := []bootstrapTestCall{
		{root, "git", []string{"rev-parse", "HEAD"}},
		{"", "gh", []string{"api", "repos/" + bootstrapTestRepo + "/actions/workflows/ci-release.yml/runs?head_sha=" + bootstrapTestSHA + "&branch=main&event=push&status=success&per_page=100", "--method", "GET"}},
		{"", "gh", []string{"api", "repos/" + bootstrapTestRepo + "/actions/runs/123/jobs?filter=latest&per_page=100", "--method", "GET"}},
		{"", "gh", []string{"api", "repos/" + bootstrapTestRepo + "/git/matching-refs/tags/" + bootstrapTestTag, "--method", "GET"}},
		{"", "gh", []string{"api", "repos/" + bootstrapTestRepo + "/git/refs", "--method", "POST", "-f", "ref=refs/tags/" + bootstrapTestTag, "-f", "sha=" + bootstrapTestSHA}},
		{"", "gh", []string{"api", "repos/" + bootstrapTestRepo + "/actions/workflows/ci-release.yml/dispatches", "--method", "POST", "-f", "ref=" + bootstrapTestTag}},
	}
	if !reflect.DeepEqual(*calls, want) {
		t.Fatalf("calls = %#v, want %#v", *calls, want)
	}
	if !strings.Contains(out.String(), "Verified CI run 123; requested release workflow for "+bootstrapTestTag+" at "+bootstrapTestSHA) {
		t.Fatal(out.String())
	}
}

func TestBootstrapFailedCIBlocksAllMutations(t *testing.T) {
	c, calls, out := bootstrapTestClient(t, bootstrapTestReply{value: bootstrapTestSHA}, bootstrapTestReply{value: map[string]any{"workflow_runs": []any{}}})
	if err := c.bootstrap(t.TempDir(), bootstrapTestTag, bootstrapTestSHA); err == nil {
		t.Fatal("accepted missing CI evidence")
	}
	if len(*calls) != 2 || out.Len() != 0 {
		t.Fatalf("calls=%+v, output=%s", *calls, out)
	}
	bootstrapAssertReadOnly(t, *calls)
}

func TestBootstrapInvalidEnvironmentAndCheckoutBlockGitHub(t *testing.T) {
	for _, field := range []string{"GITHUB_EVENT_NAME", "GITHUB_REF", "GITHUB_SHA", "GITHUB_REPOSITORY", "checkout"} {
		t.Run(field, func(t *testing.T) {
			c, calls, _ := bootstrapTestClient(t, bootstrapTestReply{value: strings.Repeat("b", 40)})
			getenv := c.getenv
			c.getenv = func(key string) string {
				if key == field {
					return ""
				}
				return getenv(key)
			}
			if err := c.bootstrap(t.TempDir(), bootstrapTestTag, bootstrapTestSHA); err == nil {
				t.Fatal("accepted invalid environment or checkout")
			}
			for _, call := range *calls {
				if call.name != "git" {
					t.Fatalf("contacted GitHub before validation: %+v", call)
				}
			}
		})
	}
}

func TestBootstrapDispatchFailureIsReportedWithoutRetry(t *testing.T) {
	c, calls, out := bootstrapTestClient(t,
		bootstrapTestReply{value: bootstrapTestSHA},
		bootstrapTestReply{value: bootstrapTestRuns()},
		bootstrapTestReply{value: map[string]any{"jobs": bootstrapTestJobs()}},
		bootstrapTestReply{value: []bootstrapGitRef{bootstrapTestRef("commit", bootstrapTestSHA)}},
		bootstrapTestReply{err: errors.New("HTTP 403")},
	)
	if err := c.bootstrap(t.TempDir(), bootstrapTestTag, bootstrapTestSHA); err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("expected dispatch failure: %v", err)
	}
	if len(*calls) != 5 || out.Len() != 0 {
		t.Fatalf("calls=%+v, output=%s", *calls, out)
	}
	bootstrapAssertReadOnly(t, (*calls)[:4])
}
