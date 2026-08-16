package repos

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/fullsend-ai/fullsend/internal/forge"
)

// RepoState holds the installation state of a single repo as read
// from forge variables and workflow files.
type RepoState struct {
	Installed       bool
	MintURL         string
	InferenceRegion string
	FullsendRef     string
}

// ProbeRepoState reads a repo's current per-repo installation state
// from forge variables and workflow files.
func ProbeRepoState(ctx context.Context, client forge.Client, owner, repo string, fc ForgeConfig) (RepoState, error) {
	vars, err := client.ListRepoVariables(ctx, owner, repo)
	if err != nil {
		return RepoState{}, fmt.Errorf("listing variables for %s/%s: %w", owner, repo, err)
	}

	if vars[forge.PerRepoGuardVar] != "true" {
		return RepoState{}, nil
	}

	state := RepoState{
		Installed:       true,
		MintURL:         vars["FULLSEND_MINT_URL"],
		InferenceRegion: vars["FULLSEND_GCP_REGION"],
	}

	ref, err := readWorkflowRef(ctx, client, owner, repo, fc)
	if err != nil {
		return state, fmt.Errorf("reading workflow for %s/%s: %w", owner, repo, err)
	}
	state.FullsendRef = ref

	return state, nil
}

// Drift describes a single field that differs between the manifest's
// desired state and the repo's actual state.
type Drift struct {
	Field    string `json:"field"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
}

// RepoStatus holds the status of a single repo as compared against
// the manifest's desired state.
type RepoStatus struct {
	Owner           string  `json:"owner"`
	Repo            string  `json:"repo"`
	Installed       bool    `json:"installed"`
	CurrentRef      string  `json:"current_ref,omitempty"`
	ExpectedRef     string  `json:"expected_ref,omitempty"`
	MintURL         string  `json:"mint_url,omitempty"`
	ExpectedMintURL string  `json:"expected_mint_url,omitempty"`
	Region          string  `json:"region,omitempty"`
	Drifts          []Drift `json:"drifts,omitempty"`
	Error           string  `json:"error,omitempty"`
}

// StatusSummary provides aggregate counts across all repos.
// Counts are not mutually exclusive: a repo can be both Installed and
// Errored (e.g. guard variable set but workflow read fails), so
// Installed + NotInstalled + Errored may exceed Total.
type StatusSummary struct {
	Total        int `json:"total"`
	Installed    int `json:"installed"`
	NotInstalled int `json:"not_installed"`
	Drifted      int `json:"drifted"`
	Errored      int `json:"errored"`
}

// StatusResult holds the full output of a status check.
type StatusResult struct {
	Repos    []RepoStatus  `json:"repos"`
	Summary  StatusSummary `json:"summary"`
	Warnings []string      `json:"warnings,omitempty"`
}

// Status compares the manifest's desired state against the actual forge
// state for each repo. It returns a StatusResult with per-repo status
// and aggregate counts. API calls are parallelised up to maxConcurrency.
func Status(ctx context.Context, manifest *Manifest, clients ForgeClientFactory, maxConcurrency int, repoFilter []string) (*StatusResult, error) {
	resolved, err := manifest.ExpandGlobs(ctx, clients)
	if err != nil {
		return nil, fmt.Errorf("resolving repos: %w", err)
	}

	var warnings []string
	if len(repoFilter) > 0 {
		var unmatched []string
		var filterErr error
		resolved, unmatched, filterErr = filterRepos(resolved, repoFilter)
		if filterErr != nil {
			return nil, filterErr
		}
		for _, p := range unmatched {
			warnings = append(warnings, fmt.Sprintf("--repo filter %q matched no manifest entries", p))
		}
	}

	if maxConcurrency < 1 {
		maxConcurrency = 8
	}

	// Create a ref resolver for SHA-based drift detection. The resolver
	// caches results so all repos sharing the same fullsend_ref only
	// trigger one API call.
	var refResolver *RefResolver
	if ghFC, ghErr := clients.ConfigFor(ForgeGitHub); ghErr == nil {
		refResolver = NewRefResolver(ghFC.Client)
	}

	results := make([]RepoStatus, len(resolved))
	sem := make(chan struct{}, maxConcurrency)
	var wg sync.WaitGroup

	for i, rr := range resolved {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return nil, ctx.Err()
		}
		wg.Add(1)
		go func(idx int, rr ResolvedRepo) {
			defer wg.Done()
			defer func() { <-sem }()

			cfg := manifest.ResolveConfigForEntry(rr.Owner, rr.Repo, rr.Entry)
			fc, fcErr := clients.ConfigFor(cfg.Forge)
			if fcErr != nil {
				results[idx] = RepoStatus{
					Owner: rr.Owner,
					Repo:  rr.Repo,
					Error: fcErr.Error(),
				}
				return
			}
			cfg.ForgeConfig = fc
			status := checkRepoStatus(ctx, cfg, refResolver)
			results[idx] = status
		}(i, rr)
	}
	wg.Wait()

	summary := StatusSummary{Total: len(results)}
	for _, s := range results {
		if s.Error != "" {
			summary.Errored++
		}
		if s.Installed {
			summary.Installed++
		} else {
			summary.NotInstalled++
		}
		if len(s.Drifts) > 0 {
			summary.Drifted++
		}
	}

	return &StatusResult{Repos: results, Summary: summary, Warnings: warnings}, nil
}

func checkRepoStatus(ctx context.Context, cfg ResolvedConfig, resolver *RefResolver) RepoStatus {
	owner := cfg.Owner
	repo := cfg.Repo
	client := cfg.ForgeConfig.Client
	fc := cfg.ForgeConfig

	status := RepoStatus{
		Owner:           owner,
		Repo:            repo,
		ExpectedRef:     cfg.FullsendRef,
		ExpectedMintURL: cfg.MintURL,
	}

	state, err := ProbeRepoState(ctx, client, owner, repo, fc)
	if err != nil {
		status.Error = err.Error()
	}

	if !state.Installed {
		return status
	}
	status.Installed = true
	status.MintURL = state.MintURL
	status.Region = state.InferenceRegion
	status.CurrentRef = state.FullsendRef

	if err != nil {
		return status
	}

	if cfg.MintURL != "" && status.MintURL != cfg.MintURL {
		status.Drifts = append(status.Drifts, Drift{
			Field:    "FULLSEND_MINT_URL",
			Expected: cfg.MintURL,
			Actual:   status.MintURL,
		})
	}

	// Inference secrets are always required.
	for _, secretName := range requiredSecretsForForge() {
		exists, secretErr := client.RepoSecretExists(ctx, owner, repo, secretName)
		if secretErr != nil {
			if status.Error == "" {
				status.Error = fmt.Sprintf("checking secret %s: %v", secretName, secretErr)
			}
			break
		}
		if !exists {
			status.Drifts = append(status.Drifts, Drift{
				Field:    secretName,
				Expected: "present",
				Actual:   "missing",
			})
		}
	}

	// Resolve the manifest's fullsend_ref to a commit SHA for
	// comparison. This handles floating refs like "main" — if the
	// branch has moved, the resolved SHA differs from the installed
	// SHA and drift is correctly reported.
	if cfg.FullsendRef != "" {
		expectedSHA := cfg.FullsendRef
		if resolver != nil {
			expectedSHA = resolver.Resolve(ctx, cfg.FullsendRef)
		}
		if status.CurrentRef != expectedSHA {
			status.Drifts = append(status.Drifts, Drift{
				Field:    "fullsend_ref",
				Expected: cfg.FullsendRef,
				Actual:   status.CurrentRef,
			})
		}
	}

	return status
}

func readWorkflowRef(ctx context.Context, client forge.Client, owner, repo string, fc ForgeConfig) (string, error) {
	for _, path := range fc.WorkflowPaths {
		content, err := client.GetFileContent(ctx, owner, repo, path)
		if err != nil {
			if forge.IsNotFound(err) {
				continue
			}
			return "", err
		}
		return extractWorkflowRef(content, fc), nil
	}
	return "", nil
}

// extractWorkflowRef extracts the @ref from a fullsend workflow file
// using the forge-specific ref pattern.
func extractWorkflowRef(content []byte, fc ForgeConfig) string {
	m := fc.WorkflowRefPattern.FindSubmatch(content)
	if m == nil {
		return ""
	}
	return string(m[1])
}

// filterRepos returns the subset of repos matching at least one filter
// pattern, plus any patterns that matched nothing. When every pattern
// is unmatched (the result is empty), an error is returned so callers
// can surface a non-zero exit code.
//
// Callers surface unmatched-pattern warnings through two mechanisms:
// Status, Diff, and Sync collect them into a result struct field;
// BatchInstall and Upgrade emit them via progress callbacks. This
// dual-surface design reflects each caller's existing output architecture.
func filterRepos(repos []ResolvedRepo, filter []string) ([]ResolvedRepo, []string, error) {
	matched := make(map[string]bool)
	var result []ResolvedRepo
	for _, rr := range repos {
		fullName := rr.Owner + "/" + rr.Repo
		added := false
		for _, pattern := range filter {
			ok, err := matchesPattern(pattern, fullName)
			if err != nil {
				return nil, nil, fmt.Errorf("invalid glob pattern %q: %w", pattern, err)
			}
			if ok {
				matched[pattern] = true
				if !added {
					result = append(result, rr)
					added = true
				}
			}
		}
	}

	var unmatched []string
	for _, pattern := range filter {
		if !matched[pattern] {
			unmatched = append(unmatched, pattern)
		}
	}

	if len(result) == 0 && len(unmatched) > 0 {
		return nil, unmatched, fmt.Errorf(
			"--repo filter matched no manifest entries: %s",
			strings.Join(unmatched, ", "),
		)
	}

	return result, unmatched, nil
}
