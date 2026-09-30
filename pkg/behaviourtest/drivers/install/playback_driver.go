package install

import (
	"context"
	"fmt"
	"os"

	"github.com/fullsend-ai/fullsend/internal/forge"
	"github.com/fullsend-ai/fullsend/pkg/behaviourtest/drivers/install/common"
)

// PlaybackDriver decorates the standard behaviour repo-pool driver with the
// playback runtime's tracking-comment setup. Allocation, WIF resolution,
// installation, cleanup, and capacity all remain owned by the shared driver.
type PlaybackDriver struct {
	base   Driver
	org    string
	client forge.Client
	logf   func(string, ...any)
}

// NewPlaybackDriver wraps a standard behaviour Driver for playback tests.
func NewPlaybackDriver(base Driver, org string, client forge.Client, logf func(string, ...any)) *PlaybackDriver {
	return &PlaybackDriver{base: base, org: org, client: client, logf: logf}
}

// SetRepoHint is retained for the shared playback steps. Pool repositories
// have stable names, so the hint is no longer part of allocation.
func (d *PlaybackDriver) SetRepoHint(string) {}

func (d *PlaybackDriver) AllocateRepo(ctx context.Context) (string, error) {
	return d.base.AllocateRepo(ctx)
}

type playbackTrackingState struct {
	ref string
}

func playbackInstallHooks(opts common.GitHubSetupOpts, _ string, _ forge.Client, logf func(string, ...any)) InstallHooks {
	if opts.Runtime != "dummy-playback" {
		return InstallHooks{}
	}
	return InstallHooks{
		BeforeInstall: func(ctx context.Context, client forge.Client, org, repo string) (any, error) {
			target := org + "/" + repo
			logf("[playback] creating tracking issue on %s before install", target)
			issue, err := client.CreateIssue(ctx, org, repo, "Playback tracking", "Internal issue for playback counter tracking")
			if err != nil {
				return nil, err
			}
			comment, err := client.CreateIssueComment(ctx, org, repo, issue.Number, "playback-current: 1")
			if err != nil {
				return nil, err
			}
			return playbackTrackingState{ref: fmt.Sprintf("gh\n/repos/%s/issues/comments/%d", target, comment.ID)}, nil
		},
		AfterInstall: func(ctx context.Context, client forge.Client, org, repo string, state any) error {
			tracking, ok := state.(playbackTrackingState)
			if !ok || tracking.ref == "" {
				return fmt.Errorf("missing playback tracking reference")
			}
			logf("[playback] committing tracking comment path to %s/%s", org, repo)
			_, err := client.CommitFiles(ctx, org, repo,
				"chore: store playback tracking comment [skip ci]",
				[]forge.TreeFile{{Path: ".fullsend/playback-comment-url", Content: []byte(tracking.ref), Mode: "100644"}})
			return err
		},
	}
}

// InstalledBotToken is kept for the shared CI-step adapter. GitHub playback
// uses the existing suite CI driver and does not need to reconfigure it.
func (d *PlaybackDriver) InstalledBotToken(ctx context.Context, repoName string) (string, error) {
	val, ok, err := d.client.GetRepoVariable(ctx, d.org, repoName, "FULLSEND_FORGE_TOKEN")
	if err != nil {
		return "", fmt.Errorf("reading FULLSEND_FORGE_TOKEN: %w", err)
	}
	if !ok {
		return "", fmt.Errorf("FULLSEND_FORGE_TOKEN not found on %s/%s", d.org, repoName)
	}
	return val, nil
}

func (d *PlaybackDriver) DeallocateRepo(ctx context.Context, repoName string) error {
	return d.base.DeallocateRepo(ctx, repoName)
}

func (d *PlaybackDriver) Finalize(ctx context.Context) error {
	return d.base.Finalize(ctx)
}

func (d *PlaybackDriver) Capacity() int { return d.base.Capacity() }

func playbackSetupOpts() common.GitHubSetupOpts {
	opts := common.DefaultGitHubSetupOpts()
	if runtime := os.Getenv("PLAYBACK_RUNTIME"); runtime != "" {
		opts.Runtime = runtime
	}
	return opts
}

var _ Driver = (*PlaybackDriver)(nil)
