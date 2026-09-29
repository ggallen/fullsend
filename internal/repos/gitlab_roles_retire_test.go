package repos

import (
	"context"
	"testing"

	"github.com/fullsend-ai/fullsend/internal/forge"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCutoverGitLabRoleCredentialsRetiresSharedCredential(t *testing.T) {
	fc := provisionClient(t)
	seedCutoverState(t, fc)
	fc.Secrets["group/project/"+forge.SecretForgeToken] = true
	tokens := cutoverTokenInventory()

	result, err := CutoverGitLabRoleCredentials(context.Background(), GitLabRoleCutoverConfig{
		Owner: "group", Repo: "project", Client: fc, TokenInventory: tokens, DrainConfirmed: true,
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
		Owner: "group", Repo: "project", Client: fc, TokenInventory: cutoverTokenInventory(), DrainConfirmed: true,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrGitLabRoleCutoverNotReady)
	assert.False(t, result.SharedRetired)
	assert.True(t, fc.Secrets["group/project/"+forge.SecretForgeToken])
}

func TestCutoverGitLabRoleCredentialsRequiresInventory(t *testing.T) {
	fc := provisionClient(t)
	_, err := CutoverGitLabRoleCredentials(context.Background(), GitLabRoleCutoverConfig{
		Owner: "group", Repo: "project", Client: fc, DrainConfirmed: true,
	})
	require.Error(t, err)
}
