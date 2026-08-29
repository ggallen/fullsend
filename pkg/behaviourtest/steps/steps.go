package steps

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cucumber/godog"
	"gopkg.in/yaml.v3"

	"github.com/fullsend-ai/fullsend/internal/runtime"
	"github.com/fullsend-ai/fullsend/pkg/behaviourtest/world"
)

func registerE2ESteps(sc *godog.ScenarioContext) {
	sc.Step(`^the installed agents are "([^"]+)"$`, func(ctx context.Context, csv string) (context.Context, error) {
		return ctx, givenInstalledAgents(world.FromContext(ctx), csv)
	})
	sc.Step(`^an issue "([^"]+)" is created$`, func(ctx context.Context, name string) (context.Context, error) {
		return ctx, givenIssueFromFixture(world.FromContext(ctx), name)
	})
	sc.Step(`^the issue is not labeled with "([^"]+)"$`, func(ctx context.Context, label string) (context.Context, error) {
		return ctx, thenIssueNotLabeledWith(world.FromContext(ctx), label)
	})
	sc.Step(`^the issue is labeled with "([^"]+)"$`, func(ctx context.Context, label string) (context.Context, error) {
		return ctx, thenIssueIsLabeledWith(world.FromContext(ctx), label)
	})
	sc.Step(`^a (\w+) agent that returns "([^"]+)"$`, func(ctx context.Context, agent, result string) (context.Context, error) {
		w := world.FromContext(ctx)
		if !w.IsPlaybackMode() {
			return ctx, nil
		}
		w.PlaybackEntries = append(w.PlaybackEntries, runtime.PlaybackEntry{Result: result})
		return ctx, nil
	})
	sc.Step(`^the (\w+) agent completes successfully$`, func(ctx context.Context, agent string) (context.Context, error) {
		return ctx, thenAgentCompletes(world.FromContext(ctx), agent)
	})
	sc.Step(`^a pull request exists$`, func(ctx context.Context) (context.Context, error) {
		return ctx, thenPullRequestExists(world.FromContext(ctx))
	})
	sc.Step(`^the issue has a (successful|failed) status comment$`, func(ctx context.Context, status string) (context.Context, error) {
		return ctx, thenIssueHasStatusComment(world.FromContext(ctx), status)
	})
	sc.Step(`^the (\w+) agent submitted "([^"]+)"$`, func(ctx context.Context, agent, state string) (context.Context, error) {
		return ctx, thenAgentSubmittedReview(world.FromContext(ctx), state)
	})
}

func givenInstalledAgents(w *world.World, csv string) error {
	var roles []string
	for _, r := range strings.Split(csv, ",") {
		r = strings.TrimSpace(r)
		if r != "" {
			roles = append(roles, r)
		}
	}
	if len(roles) == 0 {
		return fmt.Errorf("no agent roles specified")
	}
	if w.RepoOwner == "" || w.RepoName == "" {
		return fmt.Errorf("no repo configured; call 'Given an installed test repository' first")
	}

	content, err := w.SCM.GetFileContent(context.Background(), w.RepoOwner, w.RepoName, ".fullsend/config.yaml")
	if err != nil {
		return fmt.Errorf("reading config.yaml: %w", err)
	}

	var rolesYAML string
	for _, r := range roles {
		rolesYAML += fmt.Sprintf("\n    - %s", r)
	}
	updated := string(content) + fmt.Sprintf("\ndefaults:\n  roles:%s\n", rolesYAML)

	if err := w.SCM.CommitFile(context.Background(), w.RepoOwner, w.RepoName,
		".fullsend/config.yaml", "chore: restrict defaults.roles for test",
		[]byte(updated)); err != nil {
		return fmt.Errorf("updating config.yaml: %w", err)
	}
	return nil
}

type issueFixture struct {
	Title string `yaml:"title"`
	Body  string `yaml:"body"`
}

func givenIssueFromFixture(w *world.World, name string) error {
	if w.RepoOwner == "" || w.RepoName == "" {
		return fmt.Errorf("no repo configured; call 'Given an installed test repository' first")
	}

	fixturesRoot, err := findModuleSubdir(w.FixturesRoot)
	if err != nil {
		return fmt.Errorf("finding fixtures root: %w", err)
	}
	fixtureDir := filepath.Join(fixturesRoot, "fixtures", "issues", name)

	data, err := os.ReadFile(filepath.Join(fixtureDir, "issue.yaml"))
	if err != nil {
		return fmt.Errorf("reading issue fixture %q: %w", name, err)
	}
	var fix issueFixture
	if err := yaml.Unmarshal(data, &fix); err != nil {
		return fmt.Errorf("parsing issue fixture %q: %w", name, err)
	}

	repoDir := filepath.Join(fixtureDir, "repo")
	if info, err := os.Stat(repoDir); err == nil && info.IsDir() {
		if err := commitFixtureRepo(w, repoDir); err != nil {
			return fmt.Errorf("committing fixture repo files for %q: %w", name, err)
		}
	}

	if w.IsPlaybackMode() && len(w.PlaybackEntries) > 0 && !w.PlaybackCommitted {
		if err := commitPlaylist(w); err != nil {
			return fmt.Errorf("committing playlist: %w", err)
		}
	}

	trigger := time.Now()
	issue, err := w.SCM.CreateIssue(context.Background(), w.RepoOwner, w.RepoName, fix.Title, fix.Body)
	if err != nil {
		return err
	}
	w.IssueNumber = issue.Number
	w.IssueTitle = fix.Title
	w.ScenarioStart = trigger
	return nil
}

func commitFixtureRepo(w *world.World, repoDir string) error {
	ctx := context.Background()
	return filepath.WalkDir(repoDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(repoDir, path)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return w.SCM.CommitFile(ctx, w.RepoOwner, w.RepoName, rel,
			fmt.Sprintf("fixture: add %s", rel), content)
	})
}

func thenAgentCompletes(w *world.World, agent string) error {
	if w.ScenarioStart.IsZero() {
		return fmt.Errorf("no workflow trigger time recorded")
	}
	ctx := context.Background()
	run, err := w.CI.WaitForHarnessAgent(ctx, w.Org, w.RepoName, agent, w.ScenarioStart)
	if err != nil {
		return err
	}
	w.WorkflowRun = run

	if playbackRecord() {
		if err := recordAgentArtifact(w, agent); err != nil {
			w.Logf("recording artifact for %s: %v", agent, err)
		}
	}
	return nil
}

func recordAgentArtifact(w *world.World, agent string) error {
	resultsRoot, err := findModuleSubdir(w.FixturesRoot)
	if err != nil {
		return fmt.Errorf("finding fixtures root: %w", err)
	}
	scenarioDir := filepath.Join(resultsRoot, "recordings", w.ScenarioName, agent)
	if err := os.MkdirAll(scenarioDir, 0o755); err != nil {
		return err
	}
	artifactName := "fullsend-" + agent
	w.Logf("recording %s artifact from run %d to %s", artifactName, w.WorkflowRun.ID, scenarioDir)
	return w.CI.DownloadNamedArtifactFromRun(
		context.Background(), w.Org, w.RepoName,
		w.WorkflowRun.ID, artifactName, scenarioDir,
	)
}

func playbackRecord() bool {
	v, _ := strconv.ParseBool(os.Getenv("PLAYBACK_RECORD"))
	return v
}

func thenPullRequestExists(w *world.World) error {
	if w.RepoOwner == "" || w.RepoName == "" {
		return fmt.Errorf("no repo configured")
	}
	ctx := context.Background()
	prs, err := w.SCM.ListOpenChangeProposals(ctx, w.RepoOwner, w.RepoName)
	if err != nil {
		return fmt.Errorf("listing open PRs: %w", err)
	}
	if len(prs) == 0 {
		return fmt.Errorf("no open pull requests found on %s/%s", w.RepoOwner, w.RepoName)
	}
	w.PRNumber = prs[0].Number
	return nil
}

func thenIssueIsLabeledWith(w *world.World, label string) error {
	issue, err := w.SCM.GetIssue(context.Background(), w.RepoOwner, w.RepoName, w.IssueNumber)
	if err != nil {
		return err
	}
	for _, name := range issue.Labels {
		if name == label {
			return nil
		}
	}
	return fmt.Errorf("issue #%d labels %v do not include %q", w.IssueNumber, issue.Labels, label)
}

var statusExpectations = map[string]struct {
	emoji string
	label string
}{
	"successful": {emoji: "✅", label: "Success"},
	"failed":     {emoji: "❌", label: "Failure"},
}

func thenIssueHasStatusComment(w *world.World, status string) error {
	expect, ok := statusExpectations[status]
	if !ok {
		return fmt.Errorf("unknown status %q: use 'successful' or 'failed'", status)
	}

	if w.WorkflowRun == nil {
		return fmt.Errorf("no workflow run recorded; complete an agent step first")
	}
	if w.IssueNumber == 0 {
		return fmt.Errorf("no issue created")
	}

	comments, err := w.SCM.ListComments(context.Background(), w.RepoOwner, w.RepoName, w.IssueNumber)
	if err != nil {
		return fmt.Errorf("listing issue comments: %w", err)
	}

	for _, c := range comments {
		if !strings.Contains(c.Body, "fullsend:agent-status:") {
			continue
		}
		if w.WorkflowRun.HTMLURL != "" && !strings.Contains(c.Body, w.WorkflowRun.HTMLURL) {
			continue
		}
		if !strings.Contains(c.Body, "fullsend:status:terminal") {
			return fmt.Errorf("found status comment but it is not terminal (still in Started state)")
		}
		if !strings.Contains(c.Body, expect.emoji) {
			return fmt.Errorf("status comment does not indicate %s (expected %s): %s", status, expect.emoji, c.Body)
		}
		return nil
	}

	return fmt.Errorf("no status comment found on issue #%d for workflow run %s", w.IssueNumber, w.WorkflowRun.HTMLURL)
}

func thenAgentSubmittedReview(w *world.World, expectedState string) error {
	if w.PRNumber == 0 {
		return fmt.Errorf("no pull request recorded; call 'a pull request exists' first")
	}
	reviews, err := w.SCM.ListPullRequestReviews(context.Background(), w.RepoOwner, w.RepoName, w.PRNumber)
	if err != nil {
		return fmt.Errorf("listing PR reviews: %w", err)
	}
	if len(reviews) == 0 {
		return fmt.Errorf("no reviews found on PR #%d", w.PRNumber)
	}
	latest := reviews[len(reviews)-1]
	got := strings.ToLower(latest.State)
	if got != expectedState {
		return fmt.Errorf("expected review state %q but got %q on PR #%d", expectedState, got, w.PRNumber)
	}
	return nil
}

func thenIssueNotLabeledWith(w *world.World, label string) error {
	issue, err := w.SCM.GetIssue(context.Background(), w.RepoOwner, w.RepoName, w.IssueNumber)
	if err != nil {
		return err
	}
	for _, name := range issue.Labels {
		if name == label {
			return fmt.Errorf("issue #%d unexpectedly has label %q (labels: %v)", w.IssueNumber, label, issue.Labels)
		}
	}
	return nil
}
