package repos

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/fullsend-ai/fullsend/internal/forge"
	"github.com/fullsend-ai/fullsend/internal/gitlabroles"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCutoverGitLabRoleCredentialsRetiresSharedCredential(t *testing.T) {
	fc := provisionClient(t)
	seedCutoverState(t, fc)
	fc.Secrets["group/project/"+forge.SecretForgeToken] = true
	tokens := cutoverTokenInventory()

	result, err := CutoverGitLabRoleCredentials(context.Background(), GitLabRoleCutoverConfig{
		Owner: "group", Repo: "project", Client: fc, TokenInventory: tokens,
	})
	require.NoError(t, err)
	assert.True(t, result.SharedRetired)
	assert.False(t, fc.Secrets["group/project/"+forge.SecretForgeToken])
	assert.Contains(t, tokens.revoked, 4)
}

func TestCutoverGitLabRoleCredentialsDefersWhenRoleMissing(t *testing.T) {
	fc := provisionClient(t)
	fc.Secrets["group/project/"+forge.SecretForgeToken] = true
	result, err := CutoverGitLabRoleCredentials(context.Background(), GitLabRoleCutoverConfig{
		Owner: "group", Repo: "project", Client: fc, TokenInventory: cutoverTokenInventory(),
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrGitLabRoleCutoverNotReady)
	assert.False(t, result.SharedRetired)
	assert.True(t, fc.Secrets["group/project/"+forge.SecretForgeToken])
}

func TestCutoverGitLabRoleCredentialsRequiresInventory(t *testing.T) {
	fc := provisionClient(t)
	_, err := CutoverGitLabRoleCredentials(context.Background(), GitLabRoleCutoverConfig{
		Owner: "group", Repo: "project", Client: fc,
	})
	require.Error(t, err)
}

func TestCutoverGitLabRoleCredentialsRetiresWithoutDrainFlag(t *testing.T) {
	fc := provisionClient(t)
	seedCutoverState(t, fc)
	fc.Secrets["group/project/"+forge.SecretForgeToken] = true
	tokens := cutoverTokenInventory()
	result, err := CutoverGitLabRoleCredentials(context.Background(), GitLabRoleCutoverConfig{
		Owner: "group", Repo: "project", Client: fc, TokenInventory: tokens,
	})
	require.NoError(t, err)
	assert.True(t, result.SharedRetired)
	assert.False(t, fc.Secrets["group/project/"+forge.SecretForgeToken])
}

func TestCutoverGitLabRoleCredentialsDryRunDoesNotRetire(t *testing.T) {
	fc := provisionClient(t)
	seedCutoverState(t, fc)
	fc.Secrets["group/project/"+forge.SecretForgeToken] = true
	tokens := cutoverTokenInventory()

	result, err := CutoverGitLabRoleCredentials(context.Background(), GitLabRoleCutoverConfig{
		Owner: "group", Repo: "project", Client: fc, TokenInventory: tokens, DryRun: true,
	})
	require.NoError(t, err)
	assert.True(t, result.DryRun)
	assert.True(t, result.SharedRetired, "dry-run reports what would be retired without acting")
	assert.True(t, fc.Secrets["group/project/"+forge.SecretForgeToken], "dry-run must not delete the shared secret")
	assert.Empty(t, tokens.revoked, "dry-run must not revoke any token")
}

// TestCutoverGitLabRoleCredentialsRetryAfterRevokeFailureStillRevokes is a
// regression test for the retry hole in maybeRetireGitLabSharedCredential
// (internal/cli/repos_gitlab.go): if secret deletion succeeds but revoking
// the shared fullsend-bot project access token fails, a retry must not
// treat "secret already gone" as "nothing left to do" — the leftover
// Developer-scope PAT must still get revoked.
func TestCutoverGitLabRoleCredentialsRetryAfterRevokeFailureStillRevokes(t *testing.T) {
	fc := provisionClient(t)
	seedCutoverState(t, fc)
	fc.Secrets["group/project/"+forge.SecretForgeToken] = true
	tokens := cutoverTokenInventory()
	tokens.failRevoke = fmt.Errorf("revoke transiently failed")

	_, err := CutoverGitLabRoleCredentials(context.Background(), GitLabRoleCutoverConfig{
		Owner: "group", Repo: "project", Client: fc, TokenInventory: tokens,
	})
	require.Error(t, err, "revoke failure must surface as an error, not a silent partial success")
	assert.False(t, fc.Secrets["group/project/"+forge.SecretForgeToken], "the secret is already deleted before revoke runs")
	assert.Empty(t, tokens.revoked, "the shared token is still active after the failed revoke")

	// Retry: the secret is gone, but the shared token is still active in
	// the inventory. CutoverGitLabRoleCredentials must still be called (the
	// caller must not skip it merely because the secret is absent) so the
	// leftover token actually gets revoked this time.
	tokens.failRevoke = nil
	result, err := CutoverGitLabRoleCredentials(context.Background(), GitLabRoleCutoverConfig{
		Owner: "group", Repo: "project", Client: fc, TokenInventory: tokens,
	})
	require.NoError(t, err)
	assert.True(t, result.SharedRetired)
	assert.Contains(t, tokens.revoked, 4, "the leftover shared PAT must be revoked on retry")
}

// TestCutoverGitLabRoleCredentialsSupportsCustomOwnRole is a registered
// custom own-role with an agent mapping: it must be reported ready and must
// not block retirement of the shared credential.
func TestCutoverGitLabRoleCredentialsSupportsCustomOwnRole(t *testing.T) {
	fc := provisionClient(t)
	seedCutoverState(t, fc)
	fc.Secrets["group/project/"+forge.SecretForgeToken] = true
	fc.VariableValues["group/project/"+forge.VarGitLabRoleRegistry] = `{"roles":[{"name":"scanner","responsibility":"scan","credential":"own","capabilities":["read_issues"],"agents":["scanner"]}]}`
	fc.VariablesExist["group/project/"+forge.VarGitLabRoleRegistry] = true
	fc.Secrets["group/project/"+gitlabroles.CustomSecretName("scanner")] = true
	tokens := cutoverTokenInventory()
	tokens.seed(ProjectAccessToken{ID: 5, Name: gitlabroles.CustomTokenName("scanner"), Active: true, ExpiresAt: "2027-01-01"})

	result, err := CutoverGitLabRoleCredentials(context.Background(), GitLabRoleCutoverConfig{
		Owner: "group", Repo: "project", Client: fc, TokenInventory: tokens,
	})
	require.NoError(t, err)
	assert.True(t, result.Registered.Ready)
	assert.True(t, result.SharedRetired)
	assert.False(t, fc.Secrets["group/project/"+forge.SecretForgeToken])
}

// TestCutoverGitLabRoleCredentialsRejectsCustomRoleWithoutMapping is a
// registered custom role with no agent mapping: it is never cutover-ready
// regardless of built-in readiness, and the shared secret must stay in place.
func TestCutoverGitLabRoleCredentialsRejectsCustomRoleWithoutMapping(t *testing.T) {
	fc := provisionClient(t)
	seedCutoverState(t, fc)
	fc.Secrets["group/project/"+forge.SecretForgeToken] = true
	fc.VariableValues["group/project/"+forge.VarGitLabRoleRegistry] = `{"roles":[{"name":"scanner","responsibility":"scan","credential":"own","capabilities":["read_issues"],"agents":[]}]}`
	fc.VariablesExist["group/project/"+forge.VarGitLabRoleRegistry] = true
	fc.Secrets["group/project/"+gitlabroles.CustomSecretName("scanner")] = true
	tokens := cutoverTokenInventory()
	tokens.seed(ProjectAccessToken{ID: 5, Name: gitlabroles.CustomTokenName("scanner"), Active: true, ExpiresAt: "2027-01-01"})

	result, err := CutoverGitLabRoleCredentials(context.Background(), GitLabRoleCutoverConfig{
		Owner: "group", Repo: "project", Client: fc, TokenInventory: tokens,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrGitLabRoleCutoverNotReady)
	assert.False(t, result.Registered.Ready)
	require.Greater(t, len(result.Registered.Diagnostics), 3)
	assert.Contains(t, result.Registered.Diagnostics[3], "no agent mapping")
	assert.True(t, fc.Secrets["group/project/"+forge.SecretForgeToken])
}

// TestCutoverGitLabRoleCredentialsRejectsUnverifiedWithoutEnrollmentProof is a
// present role secret with no matching project access token and no
// administrator enrollment proof in rotation state: the role is unverified
// and cutover must refuse to retire the shared credential.
func TestCutoverGitLabRoleCredentialsRejectsUnverifiedWithoutEnrollmentProof(t *testing.T) {
	fc := provisionClient(t)
	seedCutoverState(t, fc)
	fc.Secrets["group/project/"+forge.SecretForgeToken] = true

	_, err := CutoverGitLabRoleCredentials(context.Background(), GitLabRoleCutoverConfig{
		Owner: "group", Repo: "project", Client: fc, TokenInventory: &fakeTokens{},
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrGitLabRoleCutoverNotReady)
	assert.True(t, fc.Secrets["group/project/"+forge.SecretForgeToken])
}

// TestCutoverGitLabRoleCredentialsAcceptsAdministratorEnrollment is the
// GitLab Free/CE path: the project-token list has no matching PAT for any
// built-in role (list access is restricted), but administrator-recorded
// idle+DistributedAt rotation state proves the credential was distributed,
// so cutover must still retire the shared credential.
func TestCutoverGitLabRoleCredentialsAcceptsAdministratorEnrollment(t *testing.T) {
	fc := provisionClient(t)
	seedCutoverState(t, fc)
	fc.Secrets["group/project/"+forge.SecretForgeToken] = true
	fc.VariableValues["group/project/"+forge.VarGitLabRoleRotation] = `{"roles":{
"poller":{"phase":"idle","distributed_at":"2026-09-20T00:00:00Z"},
"analyst":{"phase":"idle","distributed_at":"2026-09-20T00:00:00Z"},
"coder":{"phase":"idle","distributed_at":"2026-09-20T00:00:00Z"}
}}`
	fc.VariablesExist["group/project/"+forge.VarGitLabRoleRotation] = true
	tokens := &fakeTokens{}

	result, err := CutoverGitLabRoleCredentials(context.Background(), GitLabRoleCutoverConfig{
		Owner: "group", Repo: "project", Client: fc, TokenInventory: tokens,
	})
	require.NoError(t, err)
	assert.True(t, result.SharedRetired)
	assert.Empty(t, tokens.revoked)
	assert.Contains(t, strings.Join(result.Diagnostics, "\n"), "must be revoked manually")
}

// TestCutoverGitLabRoleCredentialsRejectsUnhealthyLifecycle is a built-in
// role whose current project access token has expired: cutover must refuse
// to retire the shared credential even though the secret itself is present.
func TestCutoverGitLabRoleCredentialsRejectsUnhealthyLifecycle(t *testing.T) {
	fc := provisionClient(t)
	seedCutoverState(t, fc)
	fc.Secrets["group/project/"+forge.SecretForgeToken] = true
	tokens := cutoverTokenInventory()
	for i := range tokens.listed {
		if tokens.listed[i].Name == gitlabroles.PollerTokenName {
			tokens.listed[i].ExpiresAt = "2020-01-01"
		}
	}

	result, err := CutoverGitLabRoleCredentials(context.Background(), GitLabRoleCutoverConfig{
		Owner: "group", Repo: "project", Client: fc, TokenInventory: tokens,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrGitLabRoleCutoverNotReady)
	require.NotEmpty(t, result.Readiness.Roles)
	assert.Contains(t, result.Readiness.Roles[0].Reasons, "credential lifecycle is expired")
	assert.True(t, fc.Secrets["group/project/"+forge.SecretForgeToken])
}

// cutoverRevalidationClient changes the role registry between the initial
// readiness check and the pre-cutover revalidation read, simulating an
// administrator editing FULLSEND_GITLAB_ROLE_REGISTRY mid-verification.
type cutoverRevalidationClient struct {
	*forge.FakeClient
	registryReads int
}

func (c *cutoverRevalidationClient) GetRepoVariable(ctx context.Context, owner, repo, name string) (string, bool, error) {
	if name == forge.VarGitLabRoleRegistry {
		c.registryReads++
		if c.registryReads == 2 {
			c.VariableValues[owner+"/"+repo+"/"+name] = `{"roles":[{"name":"scanner","responsibility":"scan","credential":"own","capabilities":["read_issues"],"agents":["scanner"]}]}`
			c.VariablesExist[owner+"/"+repo+"/"+name] = true
		}
	}
	return c.FakeClient.GetRepoVariable(ctx, owner, repo, name)
}

// TestCutoverGitLabRoleCredentialsRevalidatesRegistryChange is the
// check-then-cutover race: the role registry changes after the initial
// readiness check passes but before the irreversible retirement, so cutover
// must fail closed with ErrGitLabRoleCutoverStateChanged instead of acting
// on stale readiness.
func TestCutoverGitLabRoleCredentialsRevalidatesRegistryChange(t *testing.T) {
	fc := provisionClient(t)
	seedCutoverState(t, fc)
	fc.Secrets["group/project/"+forge.SecretForgeToken] = true
	client := &cutoverRevalidationClient{FakeClient: fc}
	tokens := cutoverTokenInventory()

	result, err := CutoverGitLabRoleCredentials(context.Background(), GitLabRoleCutoverConfig{
		Owner: "group", Repo: "project", Client: client, TokenInventory: tokens,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrGitLabRoleCutoverStateChanged)
	assert.True(t, IsGitLabRoleCutoverDeferred(err))
	assert.False(t, result.SharedRetired)
	assert.True(t, fc.Secrets["group/project/"+forge.SecretForgeToken])
}
