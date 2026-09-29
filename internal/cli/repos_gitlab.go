package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/fullsend-ai/fullsend/internal/forge"
	"github.com/fullsend-ai/fullsend/internal/forge/gitlab"
	"github.com/fullsend-ai/fullsend/internal/gitlabroles"
	"github.com/fullsend-ai/fullsend/internal/poll"
	"github.com/fullsend-ai/fullsend/internal/repos"
	"github.com/fullsend-ai/fullsend/internal/ui"
)

const (
	gitlabBotTokenName         = "fullsend-bot"
	gitlabAccessLevelDeveloper = 30
)

// gitlabBotPATExpiresAt returns the YYYY-MM-DD expiry GitLab expects for a
// project access token. GitLab evaluates expires_at in UTC, so a local-time
// date can already be in the past when the token is created and the PAT is
// born active:false. Compute against UTC.
func gitlabBotPATExpiresAt(now time.Time) string {
	return now.UTC().AddDate(1, 0, 0).Format("2006-01-02")
}

// setupGitLabBotToken creates a project access token for the fullsend bot
// identity and stores it as a protected CI/CD variable (FULLSEND_FORGE_TOKEN).
//
// If project access tokens are not available (free tier), it falls back
// to the provided fallbackToken. Returns the
// token value.
func setupGitLabBotToken(ctx context.Context, client forge.Client, glClient *gitlab.LiveClient, printer *ui.Printer, owner, repo, fallbackToken string) (string, error) {
	unlock := repos.LockGitLabRoleOperation(owner, repo)
	defer unlock()
	if err := ensureGitLabSharedCredentialAllowed(ctx, client, owner, repo); err != nil {
		return "", err
	}
	printer.StepStart("Creating project access token")
	var botPAT string
	var createdTokenID int
	if glClient != nil {
		// Revoke any existing fullsend-bot tokens to avoid duplicates on re-install.
		existing, listErr := glClient.ListProjectAccessTokens(ctx, owner, repo)
		if listErr != nil {
			printer.StepWarn(fmt.Sprintf("Could not list existing tokens — duplicate cleanup skipped: %v", listErr))
		} else {
			for _, t := range existing {
				if t.Name == gitlabBotTokenName && t.Active {
					if err := glClient.RevokeProjectAccessToken(ctx, owner, repo, t.ID); err != nil {
						printer.StepWarn(fmt.Sprintf("Failed to revoke existing token %q (ID %d): %v", t.Name, t.ID, err))
					} else {
						printer.StepInfo(fmt.Sprintf("Revoked existing token %q (ID %d)", t.Name, t.ID))
					}
				}
			}
		}

		expiresAt := gitlabBotPATExpiresAt(time.Now())
		// "api" scope is required for REST and GraphQL (MR operations, poll-state
		// branch writes). Developer (30) is sufficient now that poller state lives
		// on unprotected branches rather than Maintainer-only CI/CD variables.
		//
		// Residual dependency: the poller also creates pipelines via
		// CreatePipeline on the protected default branch (ADR 0067), which
		// requires merge or push access. Developer (30) satisfies that under
		// GitLab's default "Protected" preset. When a repo restricts merge
		// and push to Maintainers, ensureGitLabPollerPipelineAccess grants
		// the poller identity merge access (not push) on that ref.
		token, err := glClient.CreateProjectAccessToken(ctx, owner, repo, gitlabBotTokenName,
			[]string{"api"}, gitlabAccessLevelDeveloper, expiresAt)
		if err != nil {
			printer.StepWarn(fmt.Sprintf("Project access token creation failed: %v", err))
			if fallbackToken != "" {
				printer.StepInfo("Using manually supplied fallback token")
				botPAT = fallbackToken
			} else {
				return "", fmt.Errorf("project access token creation failed (%v); on free-tier instances, provide a PAT with 'api' scope through role-token enrollment", err)
			}
		} else {
			botPAT = token.Token
			createdTokenID = token.ID
			printer.StepDone(fmt.Sprintf("Created project access token %q (ID: %d)", gitlabBotTokenName, token.ID))
		}
	} else if fallbackToken != "" {
		botPAT = fallbackToken
	} else {
		return "", fmt.Errorf("no GitLab client available and no fallback token provided")
	}

	if botPAT != "" {
		if err := ensureGitLabSharedCredentialAllowed(ctx, client, owner, repo); err != nil {
			if createdTokenID != 0 {
				if revokeErr := glClient.RevokeProjectAccessToken(ctx, owner, repo, createdTokenID); revokeErr != nil {
					return "", fmt.Errorf("%w; revoking newly created project access token %d also failed: %v", err, createdTokenID, revokeErr)
				}
			}
			return "", err
		}
		// Always store bot PAT as a protected CI/CD variable.
		printer.StepStart("Storing bot credentials")
		if err := client.CreateRepoSecret(ctx, owner, repo, forge.SecretForgeToken, botPAT); err != nil {
			printer.StepFail("Failed to store bot credentials")
			return "", fmt.Errorf("storing bot PAT: %w", err)
		}
		if err := ensureGitLabSharedCredentialAllowed(ctx, client, owner, repo); err != nil {
			cleanupErr := client.DeleteRepoSecret(ctx, owner, repo, forge.SecretForgeToken)
			if createdTokenID != 0 {
				if revokeErr := glClient.RevokeProjectAccessToken(ctx, owner, repo, createdTokenID); revokeErr != nil {
					if cleanupErr != nil {
						return "", fmt.Errorf("%w; deleting stored shared credential failed: %v; revoking newly created project access token %d also failed: %v", err, cleanupErr, createdTokenID, revokeErr)
					}
					return "", fmt.Errorf("%w; revoking newly created project access token %d also failed: %v", err, createdTokenID, revokeErr)
				}
			}
			if cleanupErr != nil && !forge.IsNotFound(cleanupErr) {
				return "", fmt.Errorf("%w; deleting stored shared credential failed: %v", err, cleanupErr)
			}
			return "", err
		}
		printer.StepDone("Bot credentials stored as protected CI/CD variable")
	}

	return botPAT, nil
}

func ensureGitLabSharedCredentialAllowed(ctx context.Context, client forge.Client, owner, repo string) error {
	modeRaw, exists, err := client.GetRepoVariable(ctx, owner, repo, forge.VarGitLabRoleMigration)
	if err != nil {
		return fmt.Errorf("reading %s: %w", forge.VarGitLabRoleMigration, err)
	}
	roleCredentialsPresent, err := gitLabBuiltinRoleCredentialsPresent(ctx, client, owner, repo)
	if err != nil {
		return err
	}
	if roleCredentialsPresent && (!exists || strings.TrimSpace(modeRaw) == "") {
		return fmt.Errorf("refusing to create shared GitLab credential: role credentials are enrolled but %s is missing", forge.VarGitLabRoleMigration)
	}
	if exists {
		mode, err := gitlabroles.ParseMode(modeRaw)
		if err != nil {
			return err
		}
		if mode == gitlabroles.ModeEnforced {
			return fmt.Errorf("refusing to create shared GitLab credential while role migration mode is enforced")
		}
	}
	return nil
}

func gitLabBuiltinRoleCredentialsPresent(ctx context.Context, client forge.Client, owner, repo string) (bool, error) {
	for _, name := range []string{
		forge.SecretGitLabPollerToken,
		forge.SecretGitLabAnalystToken,
		forge.SecretGitLabCoderToken,
	} {
		present, err := client.RepoSecretExists(ctx, owner, repo, name)
		if err != nil {
			return false, fmt.Errorf("checking GitLab role credential %s: %w", name, err)
		}
		if present {
			return true, nil
		}
	}
	return false, nil
}

// provisionGitLabPollState ensures FULLSEND_DISPATCH_SECRET exists so
// poll-state HMAC signing is on by default, then creates both poll-state
// branches with an initial signed document. Legacy CI/CD variables are
// folded in when present (*Fast → slash, *Full + LabelState → events);
// otherwise each branch is seeded with an empty signed baseline.
//
// Only called for already-enrolled (converged / already-current) repos —
// fresh installs are handled by repos.Install() itself. Safe to run on
// live repos: it never creates or revokes the bot PAT, so it does not
// disturb live pipelines. Returns an error if either step fails so the
// caller can count the repo as failed, matching how fresh installs treat
// the identical failure as fatal; failures are still logged via StepWarn
// for operator visibility.
func provisionGitLabPollState(ctx context.Context, client forge.Client, printer *ui.Printer, owner, repo string) error {
	repoFullName := owner + "/" + repo
	dispatchSecret, created, secretErr := poll.EnsureDispatchSecret(ctx, client, owner, repo)
	if secretErr != nil {
		printer.StepWarn(fmt.Sprintf("[%s] Could not provision dispatch secret: %v", repoFullName, secretErr))
		return secretErr
	}
	if created {
		printer.StepDone(fmt.Sprintf("[%s] Provisioned FULLSEND_DISPATCH_SECRET (protected, masked)", repoFullName))
	}
	seeded, seedErr := poll.SeedGitLabPollStateBranches(ctx, client, owner, repo, dispatchSecret)
	if seedErr != nil {
		printer.StepWarn(fmt.Sprintf("[%s] Could not seed poll-state branches: %v", repoFullName, seedErr))
		return seedErr
	}
	if seeded {
		printer.StepDone(fmt.Sprintf("[%s] Seeded poll-state branches from legacy vars (or empty baseline)", repoFullName))
	}
	return nil
}

// setupGitLabPipelineSchedules creates two independent pipeline schedules
// for polling: a fast slash-command poll (every 5 min) and an offset
// event-discovery poll (at minutes 2,17,32,47). Each schedule has its own
// resource group so they never cancel each other.
func setupGitLabPipelineSchedules(ctx context.Context, client forge.Client, printer *ui.Printer, owner, repo, defaultBranch string) error {
	// Delete existing fullsend schedules to avoid duplicates on re-install.
	existing, listErr := client.ListPipelineSchedules(ctx, owner, repo)
	if listErr != nil {
		printer.StepWarn(fmt.Sprintf("Could not list existing schedules — duplicate cleanup skipped: %v", listErr))
	} else {
		for _, s := range existing {
			if strings.HasPrefix(s.Description, "fullsend") {
				if err := client.DeletePipelineSchedule(ctx, owner, repo, s.ID); err != nil {
					printer.StepWarn(fmt.Sprintf("Failed to delete existing schedule %q (ID %d): %v", s.Description, s.ID, err))
				} else {
					printer.StepInfo(fmt.Sprintf("Removed existing schedule %q (ID %d)", s.Description, s.ID))
				}
			}
		}
	}

	printer.StepStart("Creating pipeline schedules")
	var createdIDs []int64
	for _, spec := range repos.PipelineScheduleSpecs() {
		id, err := client.CreatePipelineSchedule(ctx, owner, repo, defaultBranch,
			spec.Description, spec.Cron, spec.Variables)
		if err != nil {
			// Roll back any schedules created in this call.
			for _, prevID := range createdIDs {
				if delErr := client.DeletePipelineSchedule(ctx, owner, repo, prevID); delErr != nil {
					printer.StepWarn(fmt.Sprintf("Failed to clean up schedule (ID %d): %v", prevID, delErr))
				}
			}
			printer.StepFail(fmt.Sprintf("Failed to create %s schedule", spec.Description))
			return fmt.Errorf("creating %s schedule: %w", spec.Description, err)
		}
		createdIDs = append(createdIDs, id)
		printer.StepDone(fmt.Sprintf("Created %s schedule (ID %d)", spec.Description, id))
	}
	return nil
}

// cleanupGitLabPipelineSchedules removes all fullsend-prefixed pipeline
// schedules from a GitLab project.
func cleanupGitLabPipelineSchedules(ctx context.Context, client forge.Client, printer *ui.Printer, owner, repo string) error {
	printer.StepStart("Removing pipeline schedules")
	schedules, err := client.ListPipelineSchedules(ctx, owner, repo)
	if err != nil {
		printer.StepWarn(fmt.Sprintf("Could not list pipeline schedules: %v", err))
		return nil
	}
	var toDelete []int64
	for _, s := range schedules {
		if strings.HasPrefix(s.Description, "fullsend") {
			toDelete = append(toDelete, s.ID)
		}
	}
	var deleted int
	for _, id := range toDelete {
		if err := client.DeletePipelineSchedule(ctx, owner, repo, id); err != nil {
			printer.StepWarn(fmt.Sprintf("Failed to delete schedule ID %d: %v", id, err))
		} else {
			deleted++
		}
	}
	printer.StepDone(fmt.Sprintf("Removed %d pipeline schedule(s)", deleted))
	return nil
}

// healGitLabResourceGroups toggles process_mode on all fullsend-prefixed
// resource groups to break stale locks left by cancelled or deleted pipelines.
// This complements the per-job self-heal in the scaffold templates: the
// self-heal cannot fix stale locks on first run because the job is blocked
// before it starts. Running this during install breaks those locks.
//
// The toggle sequence (unordered → target mode) forces GitLab to
// re-evaluate the lock state and release stale locks.
func healGitLabResourceGroups(ctx context.Context, glClient *gitlab.LiveClient, printer *ui.Printer, owner, repo string) {
	printer.StepStart("Healing resource group locks")
	groups, err := glClient.ListResourceGroups(ctx, owner, repo)
	if err != nil {
		printer.StepWarn(fmt.Sprintf("Could not list resource groups: %v", err))
		return
	}

	var healed int
	for _, g := range groups {
		if !strings.HasPrefix(g.Key, "fullsend-") {
			continue
		}
		// Toggle to unordered first to break any stale lock, then set
		// the desired production mode per resource group type.
		targetMode := "newest_first"
		if g.Key == "fullsend-poll-events" {
			targetMode = "oldest_first"
		}
		if err := glClient.UpdateResourceGroupProcessMode(ctx, owner, repo, g.Key, "unordered"); err != nil {
			printer.StepWarn(fmt.Sprintf("Failed to toggle resource group %q to unordered: %v", g.Key, err))
			continue
		}
		if err := glClient.UpdateResourceGroupProcessMode(ctx, owner, repo, g.Key, targetMode); err != nil {
			printer.StepWarn(fmt.Sprintf("Failed to set resource group %q to %s: %v", g.Key, targetMode, err))
			continue
		}
		healed++
	}
	printer.StepDone(fmt.Sprintf("Healed %d resource group(s)", healed))
}

func annotateGitLabRoleLifecycle(ctx context.Context, clients repos.ForgeClientFactory, result *repos.StatusResult) {
	if result == nil || clients == nil {
		return
	}
	fc, err := clients.ConfigFor(repos.ForgeGitLab)
	if err != nil || fc.Client == nil {
		return
	}
	glClient, ok := fc.Client.(*gitlab.LiveClient)
	if !ok {
		return
	}
	adapter := gitlabTokenAdapter{c: glClient}
	now := time.Now()
	for i := range result.Repos {
		st := &result.Repos[i]
		// A manifest can mix GitHub and GitLab entries. RepoStatus.Forge is
		// populated by repos.Status() for every real status result; an
		// empty value only occurs in hand-built test fixtures that predate
		// this field, which are exercising GitLab-only scenarios. Skip
		// anything explicitly resolved to a non-GitLab forge so GitHub
		// owner/repo paths are never sent to the GitLab client (mirrors
		// the forge gate in repos install's equivalent path).
		if st.Forge != "" && st.Forge != repos.ForgeGitLab {
			continue
		}
		hasRoleStatus := st.GitLabRoleMode != "" || len(st.GitLabRoleDiagnostics) > 0
		toks, listErr := adapter.ListProjectAccessTokens(ctx, st.Owner, st.Repo)
		if listErr != nil {
			toks = nil
		}
		// repos.Status already counted this repo once in Summary.Drifted
		// if it had any drift. Only count the no-drift -> drift
		// transition here, or a repo with pre-existing drift that also
		// gains a GitLab-role lifecycle drift gets double-counted.
		wasDrifted := len(st.Drifts) > 0
		newlyDrifted := false
		if hasRoleStatus && listErr == nil {
			newlyDrifted = repos.EnrichGitLabRoleStatus(ctx, fc.Client, st.Owner, st.Repo, toks, now, st)
		}
		userIDs := repos.PollerPipelineUserIDs(toks)
		if repos.AppendGitLabPipelineRefStatus(ctx, fc.Client, st.Owner, st.Repo, userIDs, st) {
			newlyDrifted = true
		}
		if newlyDrifted && !wasDrifted {
			result.Summary.Drifted++
		}
	}
}

func gitLabTokenInventory(opts *reposInstallConfig, client forge.Client) repos.ProjectAccessTokenClient {
	if opts != nil && opts.testGitLabTokenInventory != nil {
		return opts.testGitLabTokenInventory
	}
	glClient, ok := client.(*gitlab.LiveClient)
	if !ok {
		return nil
	}
	return gitlabTokenAdapter{c: glClient}
}

func ensureGitLabPollerPipelineAccess(ctx context.Context, client forge.Client, tokens repos.ProjectAccessTokenClient, printer *ui.Printer, owner, repo string, dryRun bool) error {
	repoFullName := owner + "/" + repo
	var toks []repos.ProjectAccessToken
	var listErr error
	if tokens != nil {
		listed, err := tokens.ListProjectAccessTokens(ctx, owner, repo)
		if err != nil {
			listErr = fmt.Errorf("listing project access tokens for protected-ref pipeline access: %w", err)
		} else {
			toks = listed
		}
	}
	// Proceed even when the token list failed: EnsureGitLabPollerPipelineAccess
	// no-ops when the branch is unprotected or Developer-class merge/push is
	// already allowed, neither of which needs the token list. Only surface
	// listErr if a grant actually turns out to be required.
	res, err := repos.EnsureGitLabPollerPipelineAccess(ctx, client, owner, repo, repos.PollerPipelineUserIDs(toks), dryRun)
	if err != nil {
		if listErr != nil {
			err = fmt.Errorf("%w (also failed to list project access tokens: %v)", err, listErr)
		}
		printer.StepFail(fmt.Sprintf("[%s] GitLab poller protected-ref pipeline access: %v", repoFullName, err))
		return err
	}
	if res.Detail != "" {
		printer.StepDone(fmt.Sprintf("[%s] %s", repoFullName, res.Detail))
	}
	return nil
}

type gitlabTokenAdapter struct {
	c *gitlab.LiveClient
}

func (a gitlabTokenAdapter) CreateProjectAccessToken(ctx context.Context, owner, repo, name string, scopes []string, accessLevel int, expiresAt string) (*repos.ProjectAccessToken, error) {
	tok, err := a.c.CreateProjectAccessToken(ctx, owner, repo, name, scopes, accessLevel, expiresAt)
	if err != nil {
		return nil, err
	}
	return &repos.ProjectAccessToken{ID: tok.ID, Name: tok.Name, Token: tok.Token, UserID: tok.UserID}, nil
}

func (a gitlabTokenAdapter) RevokeProjectAccessToken(ctx context.Context, owner, repo string, tokenID int) error {
	return a.c.RevokeProjectAccessToken(ctx, owner, repo, tokenID)
}

func (a gitlabTokenAdapter) ListProjectAccessTokens(ctx context.Context, owner, repo string) ([]repos.ProjectAccessToken, error) {
	toks, err := a.c.ListProjectAccessTokens(ctx, owner, repo)
	if err != nil {
		return nil, err
	}
	out := make([]repos.ProjectAccessToken, len(toks))
	for i, t := range toks {
		out[i] = repos.ProjectAccessToken{
			ID: t.ID, Name: t.Name, Active: t.Active, ExpiresAt: t.ExpiresAt, Revoked: t.Revoked, UserID: t.UserID,
		}
	}
	return out, nil
}

func prepareGitLabRoleFlags(opts *reposInstallConfig) error {
	s := strings.ToLower(strings.TrimSpace(opts.gitlabRoleMigration))
	if s != "" {
		mode, err := gitlabroles.ParseMode(s)
		if err != nil {
			return fmt.Errorf("--gitlab-role-migration: %w", err)
		}
		if !mode.OperatorSettable() {
			return fmt.Errorf("--gitlab-role-migration %q is not operator-settable", s)
		}
		opts.gitlabRoleModeFlag = mode
	}
	if opts.gitlabRoleRegistry != "" {
		raw, err := os.ReadFile(opts.gitlabRoleRegistry)
		if err != nil {
			return fmt.Errorf("reading --gitlab-role-registry: %w", err)
		}
		if _, err := gitlabroles.ParseRegistry(string(raw)); err != nil {
			return fmt.Errorf("parsing --gitlab-role-registry: %w", err)
		}
		opts.gitlabRoleRegistryJSON = string(raw)
	}
	provided, err := parseGitLabRoleTokens(opts.gitlabRoleTokens)
	if err != nil {
		return err
	}
	opts.gitlabRoleProvided = provided
	for _, raw := range opts.rotateGitLabRoleNames {
		name := gitlabroles.Role(strings.ToLower(strings.TrimSpace(raw)))
		if name == "" {
			return fmt.Errorf("invalid --rotate-gitlab-role: empty name")
		}
		opts.rotateGitLabRoleFilter = append(opts.rotateGitLabRoleFilter, name)
	}
	return nil
}

func parseGitLabRoleTokens(flags []string) (map[gitlabroles.Role]string, error) {
	out := make(map[gitlabroles.Role]string, len(flags))
	for _, raw := range flags {
		role, tok, ok := strings.Cut(raw, "=")
		role = strings.ToLower(strings.TrimSpace(role))
		if !ok || role == "" || tok == "" {
			return nil, fmt.Errorf("invalid --gitlab-role-token: expected role=token")
		}
		if strings.HasPrefix(role, "glpat-") || strings.HasPrefix(role, "glptt-") || strings.HasPrefix(role, "gldt-") {
			return nil, fmt.Errorf("invalid --gitlab-role-token: role name is not a token value")
		}
		out[gitlabroles.Role(role)] = tok
	}
	return out, nil
}

func maybeProvisionGitLabRoles(ctx context.Context, opts *reposInstallConfig, client forge.Client, printer *ui.Printer, owner, repo string) error {
	needed, _, err := gitLabRoleWorkNeeded(ctx, client, opts, owner, repo)
	if err != nil || !needed {
		return err
	}
	return setupGitLabRoleCredentials(ctx, opts, client, printer, owner, repo)
}

func gitLabRoleWorkNeeded(context.Context, forge.Client, *reposInstallConfig, string, string) (bool, gitlabroles.Mode, error) {
	return true, gitlabroles.ModeEnforced, nil
}

// maybeCutoverGitLabRoles is retained as a compatibility shim for older
// in-package callers. The install path retires the legacy shared credential
// immediately after role provisioning instead of exposing migration flags.
func maybeCutoverGitLabRoles(ctx context.Context, opts *reposInstallConfig, client forge.Client, printer *ui.Printer, owner, repo string) error {
	if opts.gitlabRoleModeFlag == gitlabroles.ModeRollback {
		return nil
	}
	var tokens repos.ProjectAccessTokenClient = opts.testGitLabTokenInventory
	if tokens == nil {
		if glClient, ok := client.(*gitlab.LiveClient); ok {
			tokens = gitlabTokenAdapter{c: glClient}
		}
	}
	if tokens == nil {
		return nil
	}
	_, err := repos.CutoverGitLabRoleCredentials(ctx, repos.GitLabRoleCutoverConfig{
		Owner: owner, Repo: repo, Client: client, TokenInventory: tokens,
		DrainConfirmed: true, DryRun: opts.dryRun,
	})
	return err
}

func setupGitLabRoleCredentials(ctx context.Context, opts *reposInstallConfig, client forge.Client, printer *ui.Printer, owner, repo string, _ ...gitlabroles.Mode) error {
	repoFullName := owner + "/" + repo
	registryJSON := opts.gitlabRoleRegistryJSON
	if registryJSON == "" {
		raw, exists, err := client.GetRepoVariable(ctx, owner, repo, forge.VarGitLabRoleRegistry)
		if err != nil {
			return fmt.Errorf("reading %s: %w", forge.VarGitLabRoleRegistry, err)
		}
		if exists {
			registryJSON = raw
		}
	}
	reg, err := gitlabroles.ParseRegistry(registryJSON)
	if err != nil {
		return err
	}
	var tokens repos.ProjectAccessTokenClient
	if glClient, ok := client.(*gitlab.LiveClient); ok {
		tokens = gitlabTokenAdapter{c: glClient}
	}
	printer.StepStart(fmt.Sprintf("[%s] Provisioning GitLab role credentials", repoFullName))
	result, err := repos.ProvisionGitLabRoleCredentials(ctx, repos.RoleProvisionConfig{
		Owner:            owner,
		Repo:             repo,
		Client:           client,
		Tokens:           tokens,
		Registry:         reg,
		RegistryProvided: opts.gitlabRoleRegistryJSON != "",
		ProvidedTokens:   opts.gitlabRoleProvided,
		DryRun:           opts.dryRun,
	})
	if err != nil {
		printer.StepFail(fmt.Sprintf("[%s] GitLab role provisioning failed", repoFullName))
		return err
	}
	printGitLabRoleProvision(printer, repoFullName, result)
	if len(result.Failed) > 0 {
		return fmt.Errorf("GitLab role provisioning incomplete: %d role credential(s) pending", len(result.Failed))
	}
	return nil
}

func maybeRetireGitLabSharedCredential(ctx context.Context, opts *reposInstallConfig, client forge.Client, printer *ui.Printer, owner, repo string) error {
	exists, err := client.RepoSecretExists(ctx, owner, repo, forge.SecretForgeToken)
	if err != nil {
		return fmt.Errorf("checking legacy GitLab shared credential: %w", err)
	}
	if !exists {
		return nil
	}
	tokens := gitLabTokenInventory(opts, client)
	if tokens == nil {
		printer.StepInfo(fmt.Sprintf("[%s/%s] Legacy GitLab shared credential retirement deferred: token inventory unavailable", owner, repo))
		return nil
	}
	if _, inventoryErr := tokens.ListProjectAccessTokens(ctx, owner, repo); inventoryErr != nil {
		printer.StepInfo(fmt.Sprintf("[%s/%s] Legacy GitLab shared credential retirement deferred: project-token inventory unavailable", owner, repo))
		return nil
	}
	result, err := repos.CutoverGitLabRoleCredentials(ctx, repos.GitLabRoleCutoverConfig{
		Owner: owner, Repo: repo, Client: client, TokenInventory: tokens,
		DrainConfirmed: true, DryRun: opts.dryRun,
	})
	if err != nil {
		if repos.IsGitLabRoleCutoverDeferred(err) {
			printer.StepInfo(fmt.Sprintf("[%s/%s] Legacy GitLab shared credential retirement deferred: %v", owner, repo, err))
			return nil
		}
		return fmt.Errorf("retiring legacy GitLab shared credential: %w", err)
	}
	for _, diagnostic := range result.Diagnostics {
		printer.StepInfo(fmt.Sprintf("[%s/%s] %s", owner, repo, diagnostic))
	}
	return nil
}

func maybeRotateGitLabRoles(ctx context.Context, opts *reposInstallConfig, client forge.Client, printer *ui.Printer, owner, repo string) error {
	mode := gitlabroles.ModeEnforced
	registryJSON := opts.gitlabRoleRegistryJSON
	if registryJSON == "" {
		live, liveExists, liveErr := client.GetRepoVariable(ctx, owner, repo, forge.VarGitLabRoleRegistry)
		if liveErr != nil {
			return fmt.Errorf("reading %s: %w", forge.VarGitLabRoleRegistry, liveErr)
		}
		if liveExists {
			registryJSON = live
		}
	}
	reg, err := gitlabroles.ParseRegistry(registryJSON)
	if err != nil {
		return err
	}
	var tokens repos.ProjectAccessTokenClient
	if glClient, ok := client.(*gitlab.LiveClient); ok {
		tokens = gitlabTokenAdapter{c: glClient}
	}
	repoFullName := owner + "/" + repo
	if tokens != nil {
		if _, inventoryErr := tokens.ListProjectAccessTokens(ctx, owner, repo); inventoryErr != nil {
			if opts.rotateGitLabRoles || len(opts.rotateGitLabRoleFilter) > 0 {
				return fmt.Errorf("listing GitLab project access tokens for forced role rotation: %w", inventoryErr)
			}
			printer.StepInfo(fmt.Sprintf("[%s] GitLab role rotation deferred: project-token inventory unavailable", repoFullName))
			return nil
		}
	}
	printer.StepStart(fmt.Sprintf("[%s] Rotating GitLab role credentials", repoFullName))
	result, err := repos.RotateGitLabRoleCredentials(ctx, repos.RoleRotateConfig{
		Owner:          owner,
		Repo:           repo,
		Client:         client,
		Tokens:         tokens,
		Registry:       reg,
		Mode:           mode,
		Roles:          opts.rotateGitLabRoleFilter,
		Force:          opts.rotateGitLabRoles,
		ProvidedTokens: opts.gitlabRoleProvided,
		DryRun:         opts.dryRun,
	})
	if err != nil {
		printer.StepFail(fmt.Sprintf("[%s] GitLab role rotation failed", repoFullName))
		return err
	}
	printGitLabRoleRotate(printer, repoFullName, result)
	return nil
}

func printGitLabRoleRotate(printer *ui.Printer, repoFullName string, result repos.RoleRotateResult) {
	verb := "Rotated"
	if result.DryRun {
		verb = "Would rotate"
	}
	for _, role := range result.Rotated {
		printer.StepDone(fmt.Sprintf("[%s] %s %s role credential", repoFullName, verb, role))
	}
	for _, role := range result.Skipped {
		printer.StepInfo(fmt.Sprintf("[%s] %s role credential not due for rotation", repoFullName, role))
	}
	for _, role := range result.Reused {
		printer.StepInfo(fmt.Sprintf("[%s] %s reuses another registered role credential; rotation follows the target", repoFullName, role))
	}
	for _, role := range result.Overlapping {
		printer.StepInfo(fmt.Sprintf("[%s] %s previous credential remains usable for in-flight jobs", repoFullName, role))
	}
	for _, role := range result.Cleaned {
		printer.StepDone(fmt.Sprintf("[%s] Revoked previous %s role credential after grace period", repoFullName, role))
	}
	for _, role := range result.RolledBack {
		printer.StepWarn(fmt.Sprintf("[%s] %s rotation rolled back; previous credential left in place", repoFullName, role))
	}
	for _, role := range result.InProgress {
		printer.StepInfo(fmt.Sprintf("[%s] %s rotation already in progress", repoFullName, role))
	}
	for _, f := range result.Failed {
		printer.StepWarn(fmt.Sprintf("[%s] %s role rotation pending (%s): %s", repoFullName, f.Role, f.Secret, f.Reason))
	}
	for _, d := range result.Diagnostics {
		printer.StepInfo(fmt.Sprintf("[%s] %s", repoFullName, d))
	}
}

func printGitLabRoleProvision(printer *ui.Printer, repoFullName string, result repos.RoleProvisionResult) {
	createVerb := "Created"
	enrollVerb := "Enrolled"
	if result.DryRun {
		createVerb = "Would create"
		enrollVerb = "Would enroll"
	}
	for _, role := range result.Created {
		printer.StepDone(fmt.Sprintf("[%s] %s %s role credential", repoFullName, createVerb, role))
	}
	for _, role := range result.Enrolled {
		printer.StepDone(fmt.Sprintf("[%s] %s %s role credential", repoFullName, enrollVerb, role))
	}
	for _, role := range result.Skipped {
		printer.StepInfo(fmt.Sprintf("[%s] %s role credential already present", repoFullName, role))
	}
	for _, role := range result.Reused {
		printer.StepInfo(fmt.Sprintf("[%s] %s reuses another registered role credential", repoFullName, role))
	}
	for _, f := range result.Failed {
		printer.StepWarn(fmt.Sprintf("[%s] %s role credential pending (%s): %s", repoFullName, f.Role, f.Secret, f.Reason))
	}
	if result.GateWritten {
		if result.DryRun {
			printer.StepDone(fmt.Sprintf("[%s] Would set GitLab role migration gate=%s (shared credential preserved)", repoFullName, result.Mode))
		} else {
			printer.StepDone(fmt.Sprintf("[%s] GitLab role migration gate=%s (shared credential preserved)", repoFullName, result.Mode))
		}
	}
	for _, d := range result.Diagnostics {
		printer.StepInfo(fmt.Sprintf("[%s] %s", repoFullName, d))
	}
}

// gitLabUninstallTokens returns the project-token inventory used by
// repos.Uninstall to revoke GitLab identity PATs. Test hooks win; live
// GitLab clients are wrapped when at least one targeted repo is GitLab.
// An unobtainable live client for a GitLab-targeted manifest is a
// silent-success risk (PAT revocation is skipped entirely), so it is
// reported via printer.StepWarn rather than returned without comment.
func gitLabUninstallTokens(opts *reposUninstallConfig, clients repos.ForgeClientFactory, printer *ui.Printer, manifest *repos.Manifest, repoNames []string) repos.ProjectAccessTokenClient {
	if opts != nil && opts.testGitLabTokens != nil {
		return opts.testGitLabTokens
	}
	if clients == nil || manifest == nil {
		return nil
	}
	for _, fullName := range repoNames {
		owner, name, found := strings.Cut(fullName, "/")
		if !found {
			continue
		}
		rc, ok := manifest.ResolveConfigWithGlobs(owner, name)
		if !ok || rc.Forge != repos.ForgeGitLab {
			continue
		}
		fc, err := clients.ConfigFor(repos.ForgeGitLab)
		if err != nil {
			printer.StepWarn(fmt.Sprintf("Could not obtain a GitLab client for project access token revocation: %v — token revocation will be skipped for GitLab repos in this run", err))
			return nil
		}
		glClient, ok := fc.Client.(*gitlab.LiveClient)
		if !ok {
			printer.StepWarn("GitLab client is not a live API client — project access token revocation will be skipped for GitLab repos in this run")
			return nil
		}
		return gitlabTokenAdapter{c: glClient}
	}
	return nil
}
