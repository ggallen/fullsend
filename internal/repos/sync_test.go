package repos

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/fullsend-ai/fullsend/internal/forge"
)

func TestDiff_NoDrift(t *testing.T) {
	fc := forge.NewFakeClient()
	m := newTestManifest()

	populateInstalledRepo(fc, "acme-corp", "api-server", "v2.3.0",
		"https://mint.example.com", "us-central1")
	populateInstalledRepo(fc, "acme-corp", "web-frontend", "v2.3.0",
		"https://mint.example.com", "us-central1")

	fc.Secrets["acme-corp/api-server/FULLSEND_GCP_PROJECT_ID"] = true
	fc.Secrets["acme-corp/web-frontend/FULLSEND_GCP_PROJECT_ID"] = true

	result, err := Diff(context.Background(), m, newTestClientFactory(fc), 4, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Changes) != 0 {
		t.Errorf("expected 0 changes for no-drift repos, got %d: %+v", len(result.Changes), result.Changes)
	}
}

func TestDiff_VariableDrift_MintURL(t *testing.T) {
	fc := forge.NewFakeClient()
	m := newTestManifest()

	populateInstalledRepo(fc, "acme-corp", "api-server", "v2.3.0",
		"https://old-mint.example.com", "us-central1")

	result, err := Diff(context.Background(), m, newTestClientFactory(fc), 4, []string{"acme-corp/api-server"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	found := false
	for _, c := range result.Changes {
		if c.Field == "FULLSEND_MINT_URL" && c.Type == "variable" {
			found = true
			if c.Action != "update" {
				t.Errorf("action = %q, want update", c.Action)
			}
			if c.OldValue != "https://old-mint.example.com" {
				t.Errorf("old value = %q", c.OldValue)
			}
			if c.NewValue != "https://mint.example.com" {
				t.Errorf("new value = %q", c.NewValue)
			}
		}
	}
	if !found {
		t.Error("expected FULLSEND_MINT_URL change")
	}
}

func TestDiff_VariableDrift_Region_NoLongerManaged(t *testing.T) {
	// FULLSEND_GCP_REGION is no longer managed by sync — it is an
	// install-time-only value. Diff should not report region changes.
	fc := forge.NewFakeClient()
	m := newTestManifest()

	populateInstalledRepo(fc, "acme-corp", "api-server", "v2.3.0",
		"https://mint.example.com", "us-west1")

	result, err := Diff(context.Background(), m, newTestClientFactory(fc), 4, []string{"acme-corp/api-server"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, c := range result.Changes {
		if c.Field == "FULLSEND_GCP_REGION" {
			t.Errorf("diff should not report FULLSEND_GCP_REGION changes: %+v", c)
		}
	}
}

func TestDiff_MissingGuardVariable(t *testing.T) {
	fc := forge.NewFakeClient()
	m := newTestManifest()

	fc.VariableValues["acme-corp/api-server/FULLSEND_MINT_URL"] = "https://mint.example.com"
	fc.VariableValues["acme-corp/api-server/FULLSEND_GCP_REGION"] = "us-central1"

	result, err := Diff(context.Background(), m, newTestClientFactory(fc), 4, []string{"acme-corp/api-server"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Changes) != 0 {
		t.Errorf("expected no changes for uninstalled repo, got %d", len(result.Changes))
	}

	found := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "not installed") {
			found = true
		}
	}
	if !found {
		t.Error("expected warning about uninstalled repo")
	}
}

func TestDiff_SecretNoLongerManaged(t *testing.T) {
	// Secrets are no longer managed by sync — they are written once at
	// install time. Verify that diff does not report secret changes.
	fc := forge.NewFakeClient()
	m := newTestManifest()

	populateInstalledRepo(fc, "acme-corp", "api-server", "v2.3.0",
		"https://mint.example.com", "us-central1")

	result, err := Diff(context.Background(), m, newTestClientFactory(fc), 4, []string{"acme-corp/api-server"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, c := range result.Changes {
		if c.Type == "secret" {
			t.Errorf("sync should not report secret changes, got: %+v", c)
		}
	}
}

func TestDiff_SecretExists_NotManaged(t *testing.T) {
	// Secrets are no longer managed by sync — existence is irrelevant.
	fc := forge.NewFakeClient()
	m := newTestManifest()

	populateInstalledRepo(fc, "acme-corp", "api-server", "v2.3.0",
		"https://mint.example.com", "us-central1")
	fc.Secrets["acme-corp/api-server/FULLSEND_GCP_PROJECT_ID"] = true

	result, err := Diff(context.Background(), m, newTestClientFactory(fc), 4, []string{"acme-corp/api-server"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, c := range result.Changes {
		if c.Type == "secret" {
			t.Error("sync should not report secret changes")
		}
	}
}

func TestDiff_RepoNotInstalled_Warning(t *testing.T) {
	fc := forge.NewFakeClient()
	m := newTestManifest()

	result, err := Diff(context.Background(), m, newTestClientFactory(fc), 4, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Changes) != 0 {
		t.Errorf("expected no changes for uninstalled repos, got %d", len(result.Changes))
	}

	installWarnings := 0
	for _, w := range result.Warnings {
		if strings.Contains(w, "not installed") {
			installWarnings++
		}
	}
	if installWarnings != 2 {
		t.Errorf("expected 2 not-installed warnings, got %d", installWarnings)
	}
}

func TestDiff_APIError_Warning(t *testing.T) {
	fc := forge.NewFakeClient()
	m := &Manifest{
		Version: 1,
		Forge: ForgeSection{GitHub: GitHubForgeInfra{
			MintURL: "https://mint.example.com",
		}},
		Repos: []RepoEntry{{Repo: "org/repo"}},
	}

	fc.Errors["ListRepoVariables"] = fmt.Errorf("rate limit exceeded")

	result, err := Diff(context.Background(), m, newTestClientFactory(fc), 4, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Warnings) == 0 {
		t.Error("expected warning from API error")
	}
	if !strings.Contains(result.Warnings[0], "rate limit") {
		t.Errorf("warning = %q, want to contain 'rate limit'", result.Warnings[0])
	}
}

func TestDiff_MultipleRepos(t *testing.T) {
	fc := forge.NewFakeClient()
	m := newTestManifest()

	populateInstalledRepo(fc, "acme-corp", "api-server", "v2.3.0",
		"https://old-mint.example.com", "us-central1")
	populateInstalledRepo(fc, "acme-corp", "web-frontend", "v2.3.0",
		"https://old-mint.example.com", "us-central1")

	result, err := Diff(context.Background(), m, newTestClientFactory(fc), 4, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	repoChanges := map[string]int{}
	for _, c := range result.Changes {
		if c.Type == "variable" {
			repoChanges[c.Owner+"/"+c.Repo]++
		}
	}
	if repoChanges["acme-corp/api-server"] < 1 {
		t.Error("expected at least 1 variable change for api-server")
	}
	if repoChanges["acme-corp/web-frontend"] < 1 {
		t.Error("expected at least 1 variable change for web-frontend")
	}
}

func TestDiff_RepoFilter(t *testing.T) {
	fc := forge.NewFakeClient()
	m := newTestManifest()

	populateInstalledRepo(fc, "acme-corp", "api-server", "v2.3.0",
		"https://old-mint.example.com", "us-central1")
	populateInstalledRepo(fc, "acme-corp", "web-frontend", "v2.3.0",
		"https://old-mint.example.com", "us-central1")

	result, err := Diff(context.Background(), m, newTestClientFactory(fc), 4, []string{"acme-corp/api-server"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, c := range result.Changes {
		if c.Repo == "web-frontend" {
			t.Error("web-frontend should be filtered out")
		}
	}
}

func TestDiff_GlobExpansion(t *testing.T) {
	fc := forge.NewFakeClient()
	fc.OrgRepos = map[string][]forge.Repository{
		"acme-corp": {
			{Name: "api-server", FullName: "acme-corp/api-server"},
			{Name: "web-app", FullName: "acme-corp/web-app"},
		},
	}

	m := &Manifest{
		Version: 1,
		Forge: ForgeSection{GitHub: GitHubForgeInfra{
			MintURL: "https://mint.example.com",
		}},
		Defaults: DefaultsConfig{
			Forge: "github",
		},
		Repos: []RepoEntry{{Repo: "acme-corp/*"}},
	}

	populateInstalledRepo(fc, "acme-corp", "api-server", "v2.3.0",
		"https://old.example.com", "us-central1")

	result, err := Diff(context.Background(), m, newTestClientFactory(fc), 4, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	found := false
	for _, c := range result.Changes {
		if c.Repo == "api-server" && c.Field == "FULLSEND_MINT_URL" {
			found = true
		}
	}
	if !found {
		t.Error("expected mint URL change for glob-expanded api-server")
	}
}

func TestDiff_EmptyManifest(t *testing.T) {
	fc := forge.NewFakeClient()
	m := &Manifest{
		Version: 1,
		Forge: ForgeSection{GitHub: GitHubForgeInfra{
			MintURL: "https://mint.example.com",
		}},
	}

	result, err := Diff(context.Background(), m, newTestClientFactory(fc), 4, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Changes) != 0 {
		t.Errorf("expected 0 changes, got %d", len(result.Changes))
	}
}

func TestDiff_DefaultMintURL_NoDrift(t *testing.T) {
	fc := forge.NewFakeClient()
	m := &Manifest{
		Version: 1,
		Forge:   ForgeSection{GitHub: GitHubForgeInfra{}},
		Defaults: DefaultsConfig{
			Forge: "github",
		},
		Repos: []RepoEntry{{Repo: "org/repo"}},
	}

	populateInstalledRepo(fc, "org", "repo", "v2.3.0",
		DefaultPublicMintURL, "us-west1")

	result, err := Diff(context.Background(), m, newTestClientFactory(fc), 4, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, c := range result.Changes {
		if c.Type == "variable" && c.Field == "FULLSEND_MINT_URL" {
			t.Errorf("should not report mint URL change when using default: %+v", c)
		}
	}
}

func TestDiff_EmptyDesiredRegion_Skips(t *testing.T) {
	fc := forge.NewFakeClient()
	m := &Manifest{
		Version: 1,
		Forge: ForgeSection{GitHub: GitHubForgeInfra{
			MintURL: DefaultPublicMintURL,
		}},
		Defaults: DefaultsConfig{
			Forge: "github",
		},
		Repos: []RepoEntry{{Repo: "org/repo"}},
	}

	populateInstalledRepo(fc, "org", "repo", "v2.3.0",
		DefaultPublicMintURL, "us-west1")

	result, err := Diff(context.Background(), m, newTestClientFactory(fc), 4, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, c := range result.Changes {
		if c.Type == "variable" && c.Field == "FULLSEND_GCP_REGION" {
			t.Errorf("should not report change when desired is empty: %+v", c)
		}
	}
}

func TestDiff_ConcurrencyValidation(t *testing.T) {
	fc := forge.NewFakeClient()
	m := newTestManifest()

	tests := []struct {
		name        string
		concurrency int
	}{
		{"zero", 0},
		{"negative", -1},
		{"too_high", 33},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Diff(context.Background(), m, newTestClientFactory(fc), tt.concurrency, nil)
			if err == nil {
				t.Error("expected error for invalid concurrency")
			}
			if !strings.Contains(err.Error(), "concurrency must be between 1 and 32") {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestDiff_SecretCheckError_NoLongerManaged(t *testing.T) {
	// Secrets are no longer managed by sync, so secret API errors
	// should not produce warnings or changes.
	fc := forge.NewFakeClient()
	m := &Manifest{
		Version: 1,
		Forge: ForgeSection{GitHub: GitHubForgeInfra{
			MintURL: "https://mint.example.com",
		}},
		Defaults: DefaultsConfig{
			Forge: "github",
		},
		Repos: []RepoEntry{{Repo: "org/repo"}},
	}

	populateInstalledRepo(fc, "org", "repo", "v2.3.0",
		"https://mint.example.com", "us-central1")
	fc.Errors["RepoSecretExists"] = fmt.Errorf("secret API error")

	result, err := Diff(context.Background(), m, newTestClientFactory(fc), 4, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, c := range result.Changes {
		if c.Type == "secret" {
			t.Error("sync should not report secret changes")
		}
	}
}

func TestSync_NoDrift_NoVariableWrites(t *testing.T) {
	fc := forge.NewFakeClient()
	m := newTestManifest()

	populateInstalledRepo(fc, "acme-corp", "api-server", "v2.3.0",
		"https://mint.example.com", "us-central1")
	populateInstalledRepo(fc, "acme-corp", "web-frontend", "v2.3.0",
		"https://mint.example.com", "us-central1")

	result, err := Sync(context.Background(), m, newTestClientFactory(fc), 4, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(fc.Variables) != 0 {
		t.Errorf("expected no variable writes, got %d", len(fc.Variables))
	}
	if result.Failed != 0 {
		t.Errorf("expected 0 failures, got %d", result.Failed)
	}
}

func TestSync_AppliesVariableChanges(t *testing.T) {
	fc := forge.NewFakeClient()
	m := newTestManifest()

	populateInstalledRepo(fc, "acme-corp", "api-server", "v2.3.0",
		"https://old-mint.example.com", "us-central1")

	var progressCalls []string
	progress := func(repo, phase, msg string) {
		progressCalls = append(progressCalls, fmt.Sprintf("%s/%s/%s", repo, phase, msg))
	}

	result, err := Sync(context.Background(), m, newTestClientFactory(fc), 4, []string{"acme-corp/api-server"}, progress)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	foundMintWrite := false
	for _, v := range fc.Variables {
		if v.Name == "FULLSEND_MINT_URL" && v.Value == "https://mint.example.com" {
			foundMintWrite = true
		}
	}
	if !foundMintWrite {
		t.Error("expected FULLSEND_MINT_URL to be written")
	}

	if len(result.Applied) == 0 {
		t.Error("expected applied changes")
	}

	if len(progressCalls) == 0 {
		t.Error("expected progress callbacks")
	}
}

func TestSync_DoesNotWriteSecrets(t *testing.T) {
	// Secrets are written once at install time and NOT reconciled by sync.
	fc := forge.NewFakeClient()
	m := newTestManifest()

	populateInstalledRepo(fc, "acme-corp", "api-server", "v2.3.0",
		"https://mint.example.com", "us-central1")

	result, err := Sync(context.Background(), m, newTestClientFactory(fc), 4, []string{"acme-corp/api-server"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(fc.CreatedSecrets) != 0 {
		t.Errorf("expected no secret writes, got %d", len(fc.CreatedSecrets))
	}

	for _, c := range result.Applied {
		if c.Type == "secret" {
			t.Errorf("sync should not apply secret changes, got: %+v", c)
		}
	}
}

func TestSync_VariableWriteError(t *testing.T) {
	fc := forge.NewFakeClient()
	m := newTestManifest()

	populateInstalledRepo(fc, "acme-corp", "api-server", "v2.3.0",
		"https://old-mint.example.com", "us-central1")

	fc.Errors["CreateOrUpdateRepoVariable"] = fmt.Errorf("write error")

	result, err := Sync(context.Background(), m, newTestClientFactory(fc), 4, []string{"acme-corp/api-server"}, nil)
	if err == nil {
		t.Fatal("expected error from variable write failure")
	}
	if result.Failed != 1 {
		t.Errorf("expected 1 failure, got %d", result.Failed)
	}
}

func TestSync_SecretWriteErrorNoLongerApplies(t *testing.T) {
	// Secrets are no longer managed by sync. Even if CreateRepoSecret
	// would fail, sync should not attempt it.
	fc := forge.NewFakeClient()
	m := newTestManifest()

	populateInstalledRepo(fc, "acme-corp", "api-server", "v2.3.0",
		"https://mint.example.com", "us-central1")

	fc.Errors["CreateRepoSecret"] = fmt.Errorf("secret write error")

	_, err := Sync(context.Background(), m, newTestClientFactory(fc), 4, []string{"acme-corp/api-server"}, nil)
	// No error expected — sync does not write secrets.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSync_NilProgress(t *testing.T) {
	fc := forge.NewFakeClient()
	m := newTestManifest()

	populateInstalledRepo(fc, "acme-corp", "api-server", "v2.3.0",
		"https://old-mint.example.com", "us-central1")

	_, err := Sync(context.Background(), m, newTestClientFactory(fc), 4, []string{"acme-corp/api-server"}, nil)
	if err != nil {
		t.Fatalf("unexpected error with nil progress: %v", err)
	}
}

func TestSync_DiffAPIError_SkipsReconciliation(t *testing.T) {
	fc := forge.NewFakeClient()
	m := newTestManifest()

	fc.Errors["ListRepoVariables"] = fmt.Errorf("API rate limit exceeded")

	result, err := Sync(context.Background(), m, newTestClientFactory(fc), 4, nil, nil)
	if err != nil {
		t.Fatalf("expected no error (warnings only), got: %v", err)
	}

	if len(result.Applied) != 0 {
		t.Errorf("expected no applied changes, got %d", len(result.Applied))
	}
	if len(result.Warnings) == 0 {
		t.Error("expected warnings from API error")
	}
	if result.Failed != 0 {
		t.Errorf("expected 0 failures (warnings only), got %d", result.Failed)
	}
}

func TestSync_MultipleRepos(t *testing.T) {
	fc := forge.NewFakeClient()
	m := newTestManifest()

	populateInstalledRepo(fc, "acme-corp", "api-server", "v2.3.0",
		"https://old.example.com", "us-central1")
	populateInstalledRepo(fc, "acme-corp", "web-frontend", "v2.3.0",
		"https://old.example.com", "us-central1")

	result, err := Sync(context.Background(), m, newTestClientFactory(fc), 4, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	repos := map[string]bool{}
	for _, c := range result.Applied {
		repos[c.Owner+"/"+c.Repo] = true
	}
	if !repos["acme-corp/api-server"] {
		t.Error("expected changes applied to api-server")
	}
	if !repos["acme-corp/web-frontend"] {
		t.Error("expected changes applied to web-frontend")
	}
}

func TestSync_ConcurrencyValidation(t *testing.T) {
	fc := forge.NewFakeClient()
	m := newTestManifest()

	_, err := Sync(context.Background(), m, newTestClientFactory(fc), 0, nil, nil)
	if err == nil {
		t.Fatal("expected error for invalid concurrency")
	}
	if !strings.Contains(err.Error(), "concurrency must be between 1 and 32") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSync_GlobWithPerEntryOverride(t *testing.T) {
	// Secrets are no longer managed by sync. Verify that glob-expanded
	// repos reconcile variables correctly but do not write secrets.
	fc := forge.NewFakeClient()
	fc.OrgRepos = map[string][]forge.Repository{
		"acme": {{Name: "api", FullName: "acme/api"}},
	}

	m := &Manifest{
		Version: 1,
		Forge: ForgeSection{GitHub: GitHubForgeInfra{
			MintURL: "https://mint.example.com",
		}},
		Defaults: DefaultsConfig{
			Forge: "github",
		},
		Repos: []RepoEntry{{
			Repo: "acme/*",
		}},
	}

	populateInstalledRepo(fc, "acme", "api", "v2.3.0",
		"https://mint.example.com", "us-central1")

	_, err := Sync(context.Background(), m, newTestClientFactory(fc), 4, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// No secrets should be written by sync.
	if len(fc.CreatedSecrets) != 0 {
		t.Errorf("expected no secret writes from sync, got %d", len(fc.CreatedSecrets))
	}
}

func TestFormatDiffTable_NoChanges(t *testing.T) {
	result := &DiffResult{}
	output := FormatDiffTable(result)
	if !strings.Contains(output, "No changes needed") {
		t.Errorf("expected 'No changes needed', got %q", output)
	}
}

func TestFormatDiffTable_VariableChanges(t *testing.T) {
	result := &DiffResult{
		Changes: []Change{
			{
				Owner:    "org",
				Repo:     "repo",
				Field:    "FULLSEND_MINT_URL",
				Type:     "variable",
				Action:   "update",
				OldValue: "https://old",
				NewValue: "https://new",
			},
		},
	}
	output := FormatDiffTable(result)
	if !strings.Contains(output, "FULLSEND_MINT_URL") {
		t.Error("expected field name in output")
	}
	if !strings.Contains(output, "https://old") {
		t.Error("expected old value in output")
	}
	if !strings.Contains(output, "https://new") {
		t.Error("expected new value in output")
	}
}

func TestFormatDiffTable_SecretCreate(t *testing.T) {
	result := &DiffResult{
		Changes: []Change{
			{
				Owner:  "org",
				Repo:   "repo",
				Field:  "FULLSEND_GCP_PROJECT_ID",
				Type:   "secret",
				Action: "create",
			},
		},
	}
	output := FormatDiffTable(result)
	if !strings.Contains(output, "(missing)") {
		t.Error("expected '(missing)' for create secret")
	}
	if !strings.Contains(output, "(secret value)") {
		t.Error("expected '(secret value)' for desired secret")
	}
}

func TestFormatDiffTable_VariableCreate(t *testing.T) {
	result := &DiffResult{
		Changes: []Change{
			{
				Owner:    "org",
				Repo:     "repo",
				Field:    "FULLSEND_MINT_URL",
				Type:     "variable",
				Action:   "create",
				NewValue: "https://mint.example.com",
			},
		},
	}
	output := FormatDiffTable(result)
	if !strings.Contains(output, "(not set)") {
		t.Error("expected '(not set)' for create variable with empty old value")
	}
}

func TestFormatDiffTable_WithWarnings(t *testing.T) {
	result := &DiffResult{
		Warnings: []string{"org/repo: error listing variables: rate limit"},
	}
	output := FormatDiffTable(result)
	if !strings.Contains(output, "WARNING:") {
		t.Error("expected WARNING prefix in output")
	}
	if !strings.Contains(output, "rate limit") {
		t.Error("expected warning text in output")
	}
	if strings.Contains(output, "No changes needed") {
		t.Error("should not say 'No changes needed' when there are warnings")
	}
}

func TestFormatDiffTable_ColumnHeaders(t *testing.T) {
	result := &DiffResult{
		Changes: []Change{
			{Owner: "o", Repo: "r", Field: "F", Type: "variable", Action: "update", OldValue: "a", NewValue: "b"},
		},
	}
	output := FormatDiffTable(result)
	if !strings.Contains(output, "REPO") || !strings.Contains(output, "FIELD") ||
		!strings.Contains(output, "CURRENT") || !strings.Contains(output, "DESIRED") {
		t.Errorf("missing column headers in output: %q", output)
	}
}

func TestDiff_GlobExpandError(t *testing.T) {
	fc := forge.NewFakeClient()
	fc.Errors["ListOrgRepos"] = fmt.Errorf("org not found")

	m := &Manifest{
		Version: 1,
		Forge: ForgeSection{GitHub: GitHubForgeInfra{
			MintURL: "https://mint.example.com",
		}},
		Repos: []RepoEntry{{Repo: "bad-org/*"}},
	}

	_, err := Diff(context.Background(), m, newTestClientFactory(fc), 4, nil)
	if err == nil {
		t.Fatal("expected error from glob expansion")
	}
}

func TestDiff_PerRepoOverride(t *testing.T) {
	fc := forge.NewFakeClient()
	m := &Manifest{
		Version: 1,
		Forge: ForgeSection{GitHub: GitHubForgeInfra{
			MintURL: "https://mint.example.com",
		}},
		Defaults: DefaultsConfig{
			Forge: "github",
		},
		Repos: []RepoEntry{
			{Repo: "org/repo"},
		},
	}

	populateInstalledRepo(fc, "org", "repo", "v2.3.0",
		"https://mint.example.com", "eu-west1")

	result, err := Diff(context.Background(), m, newTestClientFactory(fc), 4, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, c := range result.Changes {
		if c.Field == "FULLSEND_GCP_REGION" && c.Type == "variable" {
			t.Error("should not report region drift when forge-level region matches actual")
		}
	}
}

func TestSync_RepoFilter(t *testing.T) {
	fc := forge.NewFakeClient()
	m := newTestManifest()

	populateInstalledRepo(fc, "acme-corp", "api-server", "v2.3.0",
		"https://old.example.com", "us-central1")
	populateInstalledRepo(fc, "acme-corp", "web-frontend", "v2.3.0",
		"https://old.example.com", "us-central1")

	result, err := Sync(context.Background(), m, newTestClientFactory(fc), 4, []string{"acme-corp/api-server"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, c := range result.Applied {
		if c.Repo == "web-frontend" {
			t.Error("web-frontend should be filtered out")
		}
	}
}

func TestDiff_GuardVarFalse_Warning(t *testing.T) {
	fc := forge.NewFakeClient()
	m := &Manifest{
		Version: 1,
		Forge: ForgeSection{GitHub: GitHubForgeInfra{
			MintURL: "https://mint.example.com",
		}},
		Repos: []RepoEntry{{Repo: "org/repo"}},
	}

	fc.VariableValues["org/repo/FULLSEND_PER_REPO_INSTALL"] = "false"

	result, err := Diff(context.Background(), m, newTestClientFactory(fc), 4, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Changes) != 0 {
		t.Errorf("expected no changes when guard is false, got %d", len(result.Changes))
	}

	found := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "not installed") {
			found = true
		}
	}
	if !found {
		t.Error("expected not-installed warning")
	}
}

func TestSync_GlobExpansion(t *testing.T) {
	fc := forge.NewFakeClient()
	fc.OrgRepos = map[string][]forge.Repository{
		"acme": {{Name: "api", FullName: "acme/api"}},
	}

	m := &Manifest{
		Version: 1,
		Forge: ForgeSection{GitHub: GitHubForgeInfra{
			MintURL: "https://mint.example.com",
		}},
		Defaults: DefaultsConfig{
			Forge: "github",
		},
		Repos: []RepoEntry{{Repo: "acme/*"}},
	}

	populateInstalledRepo(fc, "acme", "api", "v2.3.0",
		"https://old.example.com", "us-central1")

	result, err := Sync(context.Background(), m, newTestClientFactory(fc), 4, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	foundWrite := false
	for _, c := range result.Applied {
		if c.Repo == "api" && c.Field == "FULLSEND_MINT_URL" {
			foundWrite = true
		}
	}
	if !foundWrite {
		t.Error("expected mint URL sync for glob-expanded repo")
	}
}

func TestDiff_MultiOrg(t *testing.T) {
	fc := forge.NewFakeClient()
	m := &Manifest{
		Version: 1,
		Forge: ForgeSection{GitHub: GitHubForgeInfra{
			MintURL: "https://mint.example.com",
		}},
		Defaults: DefaultsConfig{
			Forge: "github",
		},
		Repos: []RepoEntry{
			{Repo: "org-a/repo1"},
			{Repo: "org-b/repo2"},
		},
	}

	populateInstalledRepo(fc, "org-a", "repo1", "v2.3.0", "https://old.example.com", "us-central1")
	populateInstalledRepo(fc, "org-b", "repo2", "v2.3.0", "https://old.example.com", "us-central1")

	result, err := Diff(context.Background(), m, newTestClientFactory(fc), 4, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	orgs := map[string]bool{}
	for _, c := range result.Changes {
		orgs[c.Owner] = true
	}
	if !orgs["org-a"] || !orgs["org-b"] {
		t.Error("expected changes from both orgs")
	}
}

func TestValidateConcurrency(t *testing.T) {
	if err := validateConcurrency(1); err != nil {
		t.Errorf("expected 1 to be valid: %v", err)
	}
	if err := validateConcurrency(32); err != nil {
		t.Errorf("expected 32 to be valid: %v", err)
	}
	if err := validateConcurrency(0); err == nil {
		t.Error("expected 0 to be invalid")
	}
	if err := validateConcurrency(33); err == nil {
		t.Error("expected 33 to be invalid")
	}
}

func TestSync_NoSecretsOnNoDrift(t *testing.T) {
	// Secrets are no longer managed by sync — they are written once at
	// install time. Sync should not write secrets even on convergence.
	fc := forge.NewFakeClient()
	m := newTestManifest()

	populateInstalledRepo(fc, "acme-corp", "api-server", "v2.3.0",
		"https://mint.example.com", "us-central1")
	fc.Secrets["acme-corp/api-server/FULLSEND_GCP_PROJECT_ID"] = true

	result, err := Sync(context.Background(), m, newTestClientFactory(fc), 4, []string{"acme-corp/api-server"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(fc.CreatedSecrets) != 0 {
		t.Errorf("expected no secret writes from sync, got %d", len(fc.CreatedSecrets))
	}

	for _, c := range result.Applied {
		if c.Type == "secret" {
			t.Errorf("sync should not apply secret changes, got: %+v", c)
		}
	}
}
