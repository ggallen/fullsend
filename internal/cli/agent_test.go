package cli

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fullsend-ai/fullsend/internal/config"
	"github.com/fullsend-ai/fullsend/internal/fetch"
	"github.com/fullsend-ai/fullsend/internal/forge"
	"github.com/fullsend-ai/fullsend/internal/ui"
)

const testCommitSHA = "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"

func newAgentTestServer(t *testing.T, contents map[string][]byte) (*httptest.Server, fetch.FetchPolicy) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if data, ok := contents[r.URL.Path]; ok {
			w.Write(data)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	hostPort := strings.TrimPrefix(srv.URL, "https://")
	hostname, port, _ := net.SplitHostPort(hostPort)

	tlsCfg := srv.TLS.Clone()
	tlsCfg.InsecureSkipVerify = true

	return srv, fetch.NewTestPolicy(tlsCfg, []string{hostname}, []string{port})
}

func writeOrgConfig(t *testing.T, dir string, extraYAML string) {
	t.Helper()
	cfg := `version: "1"
dispatch:
  platform: github-actions
defaults:
  roles:
    - fullsend
  max_implementation_retries: 2
repos: {}
`
	if extraYAML != "" {
		cfg += extraYAML
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(cfg), 0o644))
}

func writePerRepoConfig(t *testing.T, dir string, extraYAML string) {
	t.Helper()
	cfg := `version: "1"
roles:
  - triage
  - coder
`
	if extraYAML != "" {
		cfg += extraYAML
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(cfg), 0o644))
}

// --- loadAgentConfig tests ---

func TestLoadAgentConfig_OrgConfig(t *testing.T) {
	dir := t.TempDir()
	writeOrgConfig(t, dir, "")

	cfg, err := loadAgentConfig(filepath.Join(dir, "config.yaml"))
	require.NoError(t, err)
	assert.True(t, cfg.isOrg)
}

func TestLoadAgentConfig_PerRepoConfig(t *testing.T) {
	dir := t.TempDir()
	writePerRepoConfig(t, dir, "")

	cfg, err := loadAgentConfig(filepath.Join(dir, "config.yaml"))
	require.NoError(t, err)
	assert.False(t, cfg.isOrg)
}

func TestLoadAgentConfig_MissingFile(t *testing.T) {
	_, err := loadAgentConfig("/nonexistent/config.yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading config")
}

// --- agent add tests ---

func TestRunAgentAdd_LocalPath(t *testing.T) {
	dir := t.TempDir()
	writeOrgConfig(t, dir, "")

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "harness"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "harness", "lint.yaml"),
		[]byte("role: coder\n"),
		0o644,
	))

	printer := ui.New(os.Stdout)
	err := runAgentAdd(context.Background(), "harness/lint.yaml", "", dir, nil, printer)
	require.NoError(t, err)

	cfg, err := loadAgentConfig(filepath.Join(dir, "config.yaml"))
	require.NoError(t, err)
	agents := cfg.agents()
	require.Len(t, agents, 1)
	assert.Equal(t, "harness/lint.yaml", agents[0].Source)
	assert.Equal(t, "", agents[0].Name)
	assert.Equal(t, "lint", agents[0].DerivedName())
}

func TestRunAgentAdd_LocalPathWithName(t *testing.T) {
	dir := t.TempDir()
	writeOrgConfig(t, dir, "")

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "harness"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "harness", "lint.yaml"),
		[]byte("role: coder\n"),
		0o644,
	))

	printer := ui.New(os.Stdout)
	err := runAgentAdd(context.Background(), "harness/lint.yaml", "my-linter", dir, nil, printer)
	require.NoError(t, err)

	cfg, err := loadAgentConfig(filepath.Join(dir, "config.yaml"))
	require.NoError(t, err)
	agents := cfg.agents()
	require.Len(t, agents, 1)
	assert.Equal(t, "my-linter", agents[0].Name)
	assert.Equal(t, "my-linter", agents[0].DerivedName())
}

func TestRunAgentAdd_DuplicateNameRejected(t *testing.T) {
	dir := t.TempDir()
	writeOrgConfig(t, dir, `agents:
  - harness/lint.yaml
`)

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "harness"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "harness", "lint.yaml"),
		[]byte("role: coder\n"),
		0o644,
	))

	printer := ui.New(os.Stdout)
	err := runAgentAdd(context.Background(), "harness/lint.yaml", "", dir, nil, printer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func TestRunAgentAdd_DuplicateNameCaseInsensitive(t *testing.T) {
	dir := t.TempDir()
	writeOrgConfig(t, dir, `agents:
  - name: Lint
    source: harness/lint.yaml
`)

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "harness"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "harness", "lint.yaml"),
		[]byte("role: coder\n"),
		0o644,
	))

	printer := ui.New(os.Stdout)
	// "lint" collides with "Lint" case-insensitively
	err := runAgentAdd(context.Background(), "harness/lint.yaml", "", dir, nil, printer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func TestRunAgentAdd_PathTraversalRejected(t *testing.T) {
	dir := t.TempDir()
	writeOrgConfig(t, dir, "")

	printer := ui.New(os.Stdout)
	err := runAgentAdd(context.Background(), "../../../etc/passwd", "", dir, nil, printer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "path traversal")
}

func TestRunAgentAdd_LocalPathNotExist(t *testing.T) {
	dir := t.TempDir()
	writeOrgConfig(t, dir, "")

	printer := ui.New(os.Stdout)
	err := runAgentAdd(context.Background(), "harness/nonexistent.yaml", "", dir, nil, printer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not exist")
}

func TestRunAgentAdd_URLWithPinnedSHA(t *testing.T) {
	harnessContent := []byte("role: triage\nslug: my-triage\n")
	harnessHash := fetch.ComputeSHA256(harnessContent)

	srv, policy := newAgentTestServer(t, map[string][]byte{
		"/my-org/my-agents/" + testCommitSHA + "/harness/triage.yaml": harnessContent,
	})

	origPolicy := fetch.DefaultPolicy
	fetch.DefaultPolicy = policy
	defer func() { fetch.DefaultPolicy = origPolicy }()

	dir := t.TempDir()
	writeOrgConfig(t, dir, "")

	source := srv.URL + "/my-org/my-agents/" + testCommitSHA + "/harness/triage.yaml#sha256=" + harnessHash

	client := forge.NewFakeClient()
	printer := ui.New(os.Stdout)
	err := runAgentAdd(context.Background(), source, "", dir, client, printer)
	require.NoError(t, err)

	cfg, err := loadAgentConfig(filepath.Join(dir, "config.yaml"))
	require.NoError(t, err)
	agents := cfg.agents()
	require.Len(t, agents, 1)
	assert.Equal(t, "triage", agents[0].DerivedName())
	assert.Contains(t, agents[0].Source, "#sha256="+harnessHash)
}

func TestRunAgentAdd_URLHashMismatch(t *testing.T) {
	harnessContent := []byte("role: triage\n")

	srv, policy := newAgentTestServer(t, map[string][]byte{
		"/my-org/my-agents/" + testCommitSHA + "/harness/triage.yaml": harnessContent,
	})

	origPolicy := fetch.DefaultPolicy
	fetch.DefaultPolicy = policy
	defer func() { fetch.DefaultPolicy = origPolicy }()

	dir := t.TempDir()
	writeOrgConfig(t, dir, "")

	wrongHash := "0000000000000000000000000000000000000000000000000000000000000000"
	source := srv.URL + "/my-org/my-agents/" + testCommitSHA + "/harness/triage.yaml#sha256=" + wrongHash

	client := forge.NewFakeClient()
	printer := ui.New(os.Stdout)
	err := runAgentAdd(context.Background(), source, "", dir, client, printer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "integrity hash mismatch")
}

func TestRunAgentAdd_URLAddsAllowlistPrefix(t *testing.T) {
	harnessContent := []byte("role: triage\n")

	srv, policy := newAgentTestServer(t, map[string][]byte{
		"/my-org/my-agents/" + testCommitSHA + "/harness/triage.yaml": harnessContent,
	})

	origPolicy := fetch.DefaultPolicy
	fetch.DefaultPolicy = policy
	defer func() { fetch.DefaultPolicy = origPolicy }()

	dir := t.TempDir()
	writeOrgConfig(t, dir, "")

	source := srv.URL + "/my-org/my-agents/" + testCommitSHA + "/harness/triage.yaml"

	client := forge.NewFakeClient()
	printer := ui.New(os.Stdout)
	err := runAgentAdd(context.Background(), source, "", dir, client, printer)
	require.NoError(t, err)

	cfg, err := loadAgentConfig(filepath.Join(dir, "config.yaml"))
	require.NoError(t, err)
	resources := cfg.allowedRemoteResources()
	found := false
	for _, r := range resources {
		if strings.Contains(r, "/my-org/my-agents/") {
			found = true
			break
		}
	}
	assert.True(t, found, "expected allowed_remote_resources to contain the agent's repo prefix")
}

func TestRunAgentAdd_PerRepoConfig(t *testing.T) {
	dir := t.TempDir()
	writePerRepoConfig(t, dir, "")

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "harness"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "harness", "lint.yaml"),
		[]byte("role: coder\n"),
		0o644,
	))

	printer := ui.New(os.Stdout)
	err := runAgentAdd(context.Background(), "harness/lint.yaml", "", dir, nil, printer)
	require.NoError(t, err)

	cfg, err := loadAgentConfig(filepath.Join(dir, "config.yaml"))
	require.NoError(t, err)
	assert.False(t, cfg.isOrg)
	agents := cfg.agents()
	require.Len(t, agents, 1)
	assert.Equal(t, "lint", agents[0].DerivedName())
}

// --- agent list tests ---

func TestRunAgentList_Empty(t *testing.T) {
	dir := t.TempDir()
	writeOrgConfig(t, dir, "")

	var buf strings.Builder
	printer := ui.New(&buf)
	err := runAgentList(dir, printer)
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "No agents registered")
}

func TestRunAgentList_WithAgents(t *testing.T) {
	dir := t.TempDir()
	writeOrgConfig(t, dir, `agents:
  - harness/lint.yaml
  - name: custom
    source: harness/custom.yaml
allowed_remote_resources:
  - "https://raw.githubusercontent.com/fullsend-ai/fullsend/"
`)

	var buf strings.Builder
	printer := ui.New(&buf)
	err := runAgentList(dir, printer)
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "lint")
	assert.Contains(t, output, "custom")
	assert.Contains(t, output, "harness/lint.yaml")
	assert.Contains(t, output, "harness/custom.yaml")
}

func TestRunAgentList_StripsHashFromDisplay(t *testing.T) {
	dir := t.TempDir()
	hash := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	writeOrgConfig(t, dir, `agents:
  - "https://raw.githubusercontent.com/org/repo/`+testCommitSHA+`/harness/triage.yaml#sha256=`+hash+`"
allowed_remote_resources:
  - "https://raw.githubusercontent.com/org/repo/"
`)

	var buf strings.Builder
	printer := ui.New(&buf)
	err := runAgentList(dir, printer)
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "triage")
	assert.NotContains(t, output, "sha256=")
}

// --- agent update tests ---

func TestRunAgentUpdate_RepinsSHA(t *testing.T) {
	oldSHA := testCommitSHA
	newSHA := "b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3"
	oldHash := "1111111111111111111111111111111111111111111111111111111111111111"
	newContent := []byte("role: triage\nupdated: true\n")
	newHash := fetch.ComputeSHA256(newContent)

	srv, policy := newAgentTestServer(t, map[string][]byte{
		"/org/repo/" + newSHA + "/harness/triage.yaml": newContent,
	})

	origPolicy := fetch.DefaultPolicy
	fetch.DefaultPolicy = policy
	defer func() { fetch.DefaultPolicy = origPolicy }()

	dir := t.TempDir()
	writeOrgConfig(t, dir, `agents:
  - "`+srv.URL+`/org/repo/`+oldSHA+`/harness/triage.yaml#sha256=`+oldHash+`"
allowed_remote_resources:
  - "`+srv.URL+`/org/repo/"
`)

	client := forge.NewFakeClient()
	client.Repos = []forge.Repository{{
		FullName:      "org/repo",
		DefaultBranch: "main",
	}}
	client.BranchRefs["org/repo/main"] = newSHA

	printer := ui.New(os.Stdout)
	err := runAgentUpdate(context.Background(), "triage", "", dir, client, printer)
	require.NoError(t, err)

	cfg, err := loadAgentConfig(filepath.Join(dir, "config.yaml"))
	require.NoError(t, err)
	agents := cfg.agents()
	require.Len(t, agents, 1)
	assert.Contains(t, agents[0].Source, newSHA)
	assert.Contains(t, agents[0].Source, "#sha256="+newHash)
	assert.NotContains(t, agents[0].Source, oldSHA)
}

func TestRunAgentUpdate_ExplicitSHA(t *testing.T) {
	explicitSHA := "c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4"
	newContent := []byte("role: triage\n")
	newHash := fetch.ComputeSHA256(newContent)

	srv, policy := newAgentTestServer(t, map[string][]byte{
		"/org/repo/" + explicitSHA + "/harness/triage.yaml": newContent,
	})

	origPolicy := fetch.DefaultPolicy
	fetch.DefaultPolicy = policy
	defer func() { fetch.DefaultPolicy = origPolicy }()

	dir := t.TempDir()
	oldHash := "2222222222222222222222222222222222222222222222222222222222222222"
	writeOrgConfig(t, dir, `agents:
  - "`+srv.URL+`/org/repo/`+testCommitSHA+`/harness/triage.yaml#sha256=`+oldHash+`"
allowed_remote_resources:
  - "`+srv.URL+`/org/repo/"
`)

	printer := ui.New(os.Stdout)
	err := runAgentUpdate(context.Background(), "triage", explicitSHA, dir, nil, printer)
	require.NoError(t, err)

	cfg, err := loadAgentConfig(filepath.Join(dir, "config.yaml"))
	require.NoError(t, err)
	agents := cfg.agents()
	require.Len(t, agents, 1)
	assert.Contains(t, agents[0].Source, explicitSHA)
	assert.Contains(t, agents[0].Source, "#sha256="+newHash)
}

func TestRunAgentUpdate_LocalPathRejected(t *testing.T) {
	dir := t.TempDir()
	writeOrgConfig(t, dir, `agents:
  - harness/lint.yaml
`)

	printer := ui.New(os.Stdout)
	err := runAgentUpdate(context.Background(), "lint", "", dir, nil, printer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "local path")
}

func TestRunAgentUpdate_NotFound(t *testing.T) {
	dir := t.TempDir()
	writeOrgConfig(t, dir, "")

	printer := ui.New(os.Stdout)
	err := runAgentUpdate(context.Background(), "nonexistent", "", dir, nil, printer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestRunAgentUpdate_InvalidSHA(t *testing.T) {
	dir := t.TempDir()
	hash := "3333333333333333333333333333333333333333333333333333333333333333"
	writeOrgConfig(t, dir, `agents:
  - "https://raw.githubusercontent.com/org/repo/`+testCommitSHA+`/harness/triage.yaml#sha256=`+hash+`"
allowed_remote_resources:
  - "https://raw.githubusercontent.com/org/repo/"
`)

	printer := ui.New(os.Stdout)
	err := runAgentUpdate(context.Background(), "triage", "not-a-sha", dir, nil, printer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid commit SHA")
}

// --- agent remove tests ---

func TestRunAgentRemove_Success(t *testing.T) {
	dir := t.TempDir()
	writeOrgConfig(t, dir, `agents:
  - harness/lint.yaml
  - harness/review.yaml
`)

	printer := ui.New(os.Stdout)
	err := runAgentRemove(dir, "lint", printer)
	require.NoError(t, err)

	cfg, err := loadAgentConfig(filepath.Join(dir, "config.yaml"))
	require.NoError(t, err)
	agents := cfg.agents()
	require.Len(t, agents, 1)
	assert.Equal(t, "review", agents[0].DerivedName())
}

func TestRunAgentRemove_CleansUpAllowlist(t *testing.T) {
	dir := t.TempDir()
	hash := "4444444444444444444444444444444444444444444444444444444444444444"
	writeOrgConfig(t, dir, `agents:
  - "https://raw.githubusercontent.com/org/repo/`+testCommitSHA+`/harness/triage.yaml#sha256=`+hash+`"
allowed_remote_resources:
  - "https://raw.githubusercontent.com/org/repo/"
  - "https://raw.githubusercontent.com/fullsend-ai/fullsend/"
`)

	printer := ui.New(os.Stdout)
	err := runAgentRemove(dir, "triage", printer)
	require.NoError(t, err)

	cfg, err := loadAgentConfig(filepath.Join(dir, "config.yaml"))
	require.NoError(t, err)
	resources := cfg.allowedRemoteResources()
	for _, r := range resources {
		assert.NotContains(t, r, "/org/repo/", "should have removed the unused prefix")
	}
	// The fullsend prefix should still be there
	assert.Contains(t, resources, "https://raw.githubusercontent.com/fullsend-ai/fullsend/")
}

func TestRunAgentRemove_KeepsAllowlistWhenOtherAgentsUseIt(t *testing.T) {
	dir := t.TempDir()
	hash1 := "5555555555555555555555555555555555555555555555555555555555555555"
	hash2 := "6666666666666666666666666666666666666666666666666666666666666666"
	writeOrgConfig(t, dir, `agents:
  - "https://raw.githubusercontent.com/org/repo/`+testCommitSHA+`/harness/triage.yaml#sha256=`+hash1+`"
  - "https://raw.githubusercontent.com/org/repo/`+testCommitSHA+`/harness/code.yaml#sha256=`+hash2+`"
allowed_remote_resources:
  - "https://raw.githubusercontent.com/org/repo/"
`)

	printer := ui.New(os.Stdout)
	err := runAgentRemove(dir, "triage", printer)
	require.NoError(t, err)

	cfg, err := loadAgentConfig(filepath.Join(dir, "config.yaml"))
	require.NoError(t, err)
	resources := cfg.allowedRemoteResources()
	assert.Contains(t, resources, "https://raw.githubusercontent.com/org/repo/")
}

func TestRunAgentRemove_NotFound(t *testing.T) {
	dir := t.TempDir()
	writeOrgConfig(t, dir, "")

	printer := ui.New(os.Stdout)
	err := runAgentRemove(dir, "nonexistent", printer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// --- helper tests ---

func TestFindAgentByName(t *testing.T) {
	agents := []config.AgentEntry{
		{Source: "harness/triage.yaml"},
		{Name: "Custom", Source: "harness/custom.yaml"},
	}

	idx, found := findAgentByName(agents, "triage")
	assert.True(t, found)
	assert.Equal(t, 0, idx)

	idx, found = findAgentByName(agents, "custom")
	assert.True(t, found)
	assert.Equal(t, 1, idx)

	// Case-insensitive
	idx, found = findAgentByName(agents, "CUSTOM")
	assert.True(t, found)
	assert.Equal(t, 1, idx)

	_, found = findAgentByName(agents, "nonexistent")
	assert.False(t, found)
}

func TestAllowlistPrefixForURL(t *testing.T) {
	prefix := allowlistPrefixForURL("https://raw.githubusercontent.com/my-org/my-repo/abc123/path/to/file.yaml#sha256=deadbeef")
	assert.Equal(t, "https://raw.githubusercontent.com/my-org/my-repo/", prefix)

	prefix = allowlistPrefixForURL("harness/local.yaml")
	assert.Equal(t, "", prefix)
}

func TestBuildRawURL(t *testing.T) {
	url := buildRawURL("owner", "repo", testCommitSHA, "harness/triage.yaml")
	assert.Equal(t, "https://raw.githubusercontent.com/owner/repo/"+testCommitSHA+"/harness/triage.yaml", url)
}
