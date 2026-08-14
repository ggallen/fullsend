package repos

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/fullsend-ai/fullsend/internal/forge"
)

// noopProgress is a no-op progress callback for tests.
func noopProgress(_, _, _ string) {}

type fakeScaffoldCommit struct {
	mu     sync.Mutex
	called bool
	err    error
}

func (f *fakeScaffoldCommit) fn() ScaffoldCommitFunc {
	return func(_ context.Context, _, _ string, _ []forge.TreeFile, _ bool) error {
		f.mu.Lock()
		f.called = true
		f.mu.Unlock()
		return f.err
	}
}

const (
	fakeWIFProvider  = "projects/100000/locations/global/workloadIdentityPools/fake-pool/providers/fake-provider"
	fakeWIFProvider2 = "projects/999999/locations/global/workloadIdentityPools/fake-pool/providers/fake-provider"
)

// baseCfg returns an InstallConfig suitable for most tests.
func baseCfg() InstallConfig {
	return InstallConfig{
		Owner:            "acme",
		Repo:             "widgets",
		Forge:            ForgeGitHub,
		Roles:            []string{"triage", "coder"},
		MintURL:          "https://mint.example.com",
		InferenceProject: "fake-inference-project",
		InferenceRegion:  "us-central1",
		WIFProvider:      fakeWIFProvider,
		Direct:           true,
		SkipGuardCheck:   true,
	}
}

// newFakeClientWithRepo returns a FakeClient pre-populated with a repo.
func newFakeClientWithRepo() *forge.FakeClient {
	fc := forge.NewFakeClient()
	fc.Repos = []forge.Repository{{
		FullName:      "acme/widgets",
		Name:          "widgets",
		DefaultBranch: "main",
	}}
	return fc
}

func TestInstall_FreshInstall_Direct(t *testing.T) {
	fc := newFakeClientWithRepo()
	cfg := baseCfg()
	sc := &fakeScaffoldCommit{}

	result, err := Install(context.Background(), cfg, fc, sc.fn(), noopProgress)
	if err != nil {
		t.Fatalf("Install() returned error: %v", err)
	}

	if !result.Success {
		t.Error("expected Success=true")
	}
	if result.AlreadyInstalled {
		t.Error("expected AlreadyInstalled=false for fresh install")
	}
	if !sc.called {
		t.Error("expected scaffold commit function to be called")
	}

	// Verify repository variables were set.
	if len(fc.Variables) != 4 {
		t.Errorf("expected 4 variables, got %d", len(fc.Variables))
	}
	varMap := make(map[string]string)
	for _, v := range fc.Variables {
		varMap[v.Name] = v.Value
	}
	if varMap["FULLSEND_MINT_URL"] != "https://mint.example.com" {
		t.Errorf("FULLSEND_MINT_URL = %q, want %q", varMap["FULLSEND_MINT_URL"], "https://mint.example.com")
	}
	if varMap["FULLSEND_GCP_REGION"] != "us-central1" {
		t.Errorf("FULLSEND_GCP_REGION = %q, want %q", varMap["FULLSEND_GCP_REGION"], "us-central1")
	}
	if varMap[forge.PerRepoGuardVar] != "true" {
		t.Errorf("%s = %q, want %q", forge.PerRepoGuardVar, varMap[forge.PerRepoGuardVar], "true")
	}
	if varMap["FULLSEND_CREDENTIAL_MODE"] != CredModeWIF {
		t.Errorf("FULLSEND_CREDENTIAL_MODE = %q, want %q", varMap["FULLSEND_CREDENTIAL_MODE"], CredModeWIF)
	}

	// Verify repository secrets were set.
	if len(fc.CreatedSecrets) != 2 {
		t.Errorf("expected 2 secrets, got %d", len(fc.CreatedSecrets))
	}
	secretMap := make(map[string]string)
	for _, s := range fc.CreatedSecrets {
		secretMap[s.Name] = s.Value
	}
	if secretMap["FULLSEND_GCP_PROJECT_ID"] != "fake-inference-project" {
		t.Errorf("FULLSEND_GCP_PROJECT_ID = %q, want %q", secretMap["FULLSEND_GCP_PROJECT_ID"], "fake-inference-project")
	}
	if secretMap["FULLSEND_GCP_WIF_PROVIDER"] != fakeWIFProvider {
		t.Errorf("FULLSEND_GCP_WIF_PROVIDER = %q, want %q", secretMap["FULLSEND_GCP_WIF_PROVIDER"], fakeWIFProvider)
	}

	// Verify WIF provider is propagated to result.
	if result.WIFProvider != fakeWIFProvider {
		t.Errorf("result.WIFProvider = %q, want %q", result.WIFProvider, fakeWIFProvider)
	}
}

func TestInstall_FreshInstall_PR(t *testing.T) {
	fc := newFakeClientWithRepo()
	cfg := baseCfg()
	cfg.Direct = false

	sc := &fakeScaffoldCommit{}

	result, err := Install(context.Background(), cfg, fc, sc.fn(), noopProgress)
	if err != nil {
		t.Fatalf("Install() returned error: %v", err)
	}

	if !result.Success {
		t.Error("expected Success=true")
	}
	if !sc.called {
		t.Error("expected scaffold commit function to be called")
	}
}

// markFullyInstalled sets all per-repo installation components on a
// FakeClient: guard variable, workflow file, variables, and secrets.
func markFullyInstalled(fc *forge.FakeClient, owner, repo string) {
	fullName := owner + "/" + repo
	fc.VariableValues[fullName+"/"+forge.PerRepoGuardVar] = "true"
	fc.VariableValues[fullName+"/FULLSEND_MINT_URL"] = "https://mint.example.com"
	fc.VariableValues[fullName+"/FULLSEND_GCP_REGION"] = "us-central1"
	fc.FileContents[fullName+"/.github/workflows/fullsend.yaml"] = []byte("name: fullsend")
	fc.Secrets[fullName+"/FULLSEND_GCP_PROJECT_ID"] = true
	fc.Secrets[fullName+"/FULLSEND_GCP_WIF_PROVIDER"] = true
}

func TestInstall_AlreadyInstalled_GuardTrue(t *testing.T) {
	fc := newFakeClientWithRepo()
	markFullyInstalled(fc, "acme", "widgets")

	cfg := baseCfg()
	cfg.SkipGuardCheck = false // enable guard check

	sc := &fakeScaffoldCommit{}
	result, err := Install(context.Background(), cfg, fc, sc.fn(), noopProgress)
	if err != nil {
		t.Fatalf("Install() returned error: %v", err)
	}

	if !result.AlreadyInstalled {
		t.Error("expected AlreadyInstalled=true")
	}
	if !result.Success {
		t.Error("expected Success=true")
	}

	// Verify NO writes occurred.
	if sc.called {
		t.Error("expected scaffold commit NOT to be called for already-installed repo")
	}
	if len(fc.Variables) != 0 {
		t.Error("expected no variable writes for already-installed repo")
	}
	if len(fc.CreatedSecrets) != 0 {
		t.Error("expected no secret writes for already-installed repo")
	}
}

func TestInstall_SkipGuardCheck_ProceedsEvenWithGuardTrue(t *testing.T) {
	fc := newFakeClientWithRepo()
	fc.VariableValues["acme/widgets/"+forge.PerRepoGuardVar] = "true"

	cfg := baseCfg()
	cfg.SkipGuardCheck = true // CLI path: always proceed

	sc := &fakeScaffoldCommit{}
	result, err := Install(context.Background(), cfg, fc, sc.fn(), noopProgress)
	if err != nil {
		t.Fatalf("Install() returned error: %v", err)
	}

	if result.AlreadyInstalled {
		t.Error("expected AlreadyInstalled=false when SkipGuardCheck=true")
	}
	if !result.Success {
		t.Error("expected Success=true")
	}

	// Verify writes DID occur.
	if !sc.called {
		t.Error("expected scaffold commit to be called when guard check is skipped")
	}
	if len(fc.Variables) == 0 {
		t.Error("expected variables to be written when guard check is skipped")
	}
}

func TestInstall_PartialInstall_MissingWorkflow(t *testing.T) {
	fc := newFakeClientWithRepo()
	fc.VariableValues["acme/widgets/"+forge.PerRepoGuardVar] = "true"
	fc.VariableValues["acme/widgets/FULLSEND_MINT_URL"] = "https://mint.example.com"
	fc.VariableValues["acme/widgets/FULLSEND_GCP_REGION"] = "us-central1"
	fc.Secrets["acme/widgets/FULLSEND_GCP_PROJECT_ID"] = true
	fc.Secrets["acme/widgets/FULLSEND_GCP_WIF_PROVIDER"] = true

	cfg := baseCfg()
	cfg.SkipGuardCheck = false

	sc := &fakeScaffoldCommit{}
	result, err := Install(context.Background(), cfg, fc, sc.fn(), noopProgress)
	if err != nil {
		t.Fatalf("Install() returned error: %v", err)
	}

	if result.AlreadyInstalled {
		t.Error("expected AlreadyInstalled=false for partial install (missing workflow)")
	}
	if !result.Success {
		t.Error("expected Success=true (repair)")
	}
	if !sc.called {
		t.Error("expected scaffold commit to be called for repair")
	}
}

func TestInstall_PartialInstall_MissingVariables(t *testing.T) {
	fc := newFakeClientWithRepo()
	fc.VariableValues["acme/widgets/"+forge.PerRepoGuardVar] = "true"
	fc.FileContents["acme/widgets/.github/workflows/fullsend.yaml"] = []byte("name: fullsend")
	fc.Secrets["acme/widgets/FULLSEND_GCP_PROJECT_ID"] = true
	fc.Secrets["acme/widgets/FULLSEND_GCP_WIF_PROVIDER"] = true

	cfg := baseCfg()
	cfg.SkipGuardCheck = false

	sc := &fakeScaffoldCommit{}
	result, err := Install(context.Background(), cfg, fc, sc.fn(), noopProgress)
	if err != nil {
		t.Fatalf("Install() returned error: %v", err)
	}

	if result.AlreadyInstalled {
		t.Error("expected AlreadyInstalled=false for partial install (missing variables)")
	}
	if !result.Success {
		t.Error("expected Success=true (repair)")
	}
}

func TestInstall_PartialInstall_MissingSecrets(t *testing.T) {
	fc := newFakeClientWithRepo()
	fc.VariableValues["acme/widgets/"+forge.PerRepoGuardVar] = "true"
	fc.VariableValues["acme/widgets/FULLSEND_MINT_URL"] = "https://mint.example.com"
	fc.VariableValues["acme/widgets/FULLSEND_GCP_REGION"] = "us-central1"
	fc.VariableValues["acme/widgets/FULLSEND_CREDENTIAL_MODE"] = CredModeWIF
	fc.VariablesExist["acme/widgets/FULLSEND_CREDENTIAL_MODE"] = true
	fc.FileContents["acme/widgets/.github/workflows/fullsend.yaml"] = []byte("name: fullsend")

	cfg := baseCfg()
	cfg.SkipGuardCheck = false

	sc := &fakeScaffoldCommit{}
	result, err := Install(context.Background(), cfg, fc, sc.fn(), noopProgress)
	if err != nil {
		t.Fatalf("Install() returned error: %v", err)
	}

	if result.AlreadyInstalled {
		t.Error("expected AlreadyInstalled=false for partial install (missing secrets)")
	}
	if !result.Success {
		t.Error("expected Success=true (repair)")
	}
}

func TestInstall_PartialInstall_GuardOnlySet(t *testing.T) {
	fc := newFakeClientWithRepo()
	fc.VariableValues["acme/widgets/"+forge.PerRepoGuardVar] = "true"

	cfg := baseCfg()
	cfg.SkipGuardCheck = false

	sc := &fakeScaffoldCommit{}
	result, err := Install(context.Background(), cfg, fc, sc.fn(), noopProgress)
	if err != nil {
		t.Fatalf("Install() returned error: %v", err)
	}

	if result.AlreadyInstalled {
		t.Error("expected AlreadyInstalled=false for partial install (guard only)")
	}
	if !result.Success {
		t.Error("expected Success=true (repair)")
	}
	if !sc.called {
		t.Error("expected scaffold commit to be called for repair")
	}
	if len(fc.Variables) == 0 {
		t.Error("expected variables to be written during repair")
	}
	if len(fc.CreatedSecrets) == 0 {
		t.Error("expected secrets to be written during repair")
	}
}

func TestInstall_PartialInstall_WorkflowYmlExtension(t *testing.T) {
	fc := newFakeClientWithRepo()
	fc.VariableValues["acme/widgets/"+forge.PerRepoGuardVar] = "true"
	fc.VariableValues["acme/widgets/FULLSEND_MINT_URL"] = "https://mint.example.com"
	fc.VariableValues["acme/widgets/FULLSEND_GCP_REGION"] = "us-central1"
	fc.FileContents["acme/widgets/.github/workflows/fullsend.yml"] = []byte("name: fullsend")
	fc.Secrets["acme/widgets/FULLSEND_GCP_PROJECT_ID"] = true
	fc.Secrets["acme/widgets/FULLSEND_GCP_WIF_PROVIDER"] = true

	cfg := baseCfg()
	cfg.SkipGuardCheck = false

	sc := &fakeScaffoldCommit{}
	result, err := Install(context.Background(), cfg, fc, sc.fn(), noopProgress)
	if err != nil {
		t.Fatalf("Install() returned error: %v", err)
	}

	if !result.AlreadyInstalled {
		t.Error("expected AlreadyInstalled=true (fully installed with .yml extension)")
	}
	if sc.called {
		t.Error("expected scaffold commit NOT to be called for fully-installed repo")
	}
}

func TestInstall_EmptyWIFProvider_Rejected(t *testing.T) {
	fc := newFakeClientWithRepo()
	cfg := baseCfg()
	cfg.WIFProvider = ""

	sc := &fakeScaffoldCommit{}

	_, err := Install(context.Background(), cfg, fc, sc.fn(), noopProgress)
	if err == nil {
		t.Fatal("expected error when WIF provider is empty and secrets would be written")
	}
	if sc.called {
		t.Error("expected scaffold commit NOT to be called after empty WIF provider validation")
	}
}

func TestInstall_InvalidWIFProviderFormat_Rejected(t *testing.T) {
	fc := newFakeClientWithRepo()
	cfg := baseCfg()
	cfg.WIFProvider = "not-a-valid-provider"

	sc := &fakeScaffoldCommit{}

	_, err := Install(context.Background(), cfg, fc, sc.fn(), noopProgress)
	if err == nil {
		t.Fatal("expected error when WIF provider has invalid format")
	}
	if sc.called {
		t.Error("expected scaffold commit NOT to be called after WIF provider format validation")
	}
}

func TestInstall_ScaffoldCommitFailure(t *testing.T) {
	fc := newFakeClientWithRepo()
	sc := &fakeScaffoldCommit{err: fmt.Errorf("network error")}

	cfg := baseCfg()
	cfg.Direct = true

	result, err := Install(context.Background(), cfg, fc, sc.fn(), noopProgress)
	if err == nil {
		t.Fatal("expected error from scaffold commit failure")
	}

	if result == nil {
		t.Fatal("expected non-nil result on scaffold commit failure")
	}

	if result.WIFProvider != fakeWIFProvider {
		t.Errorf("result.WIFProvider = %q, want %q (should capture partial state)",
			result.WIFProvider, fakeWIFProvider)
	}

	// Variables and secrets are written before scaffold commit (#6122),
	// so they should be present even when the commit fails.
	if len(fc.Variables) == 0 {
		t.Error("expected variables to be written before scaffold commit")
	}
	if len(fc.CreatedSecrets) == 0 {
		t.Error("expected secrets to be written before scaffold commit")
	}
}

func TestInstall_ProgressCallbackPhases(t *testing.T) {
	fc := newFakeClientWithRepo()
	cfg := baseCfg()

	sc := &fakeScaffoldCommit{}
	var phases []string
	progress := func(_, phase, _ string) {
		phases = append(phases, phase)
	}

	_, err := Install(context.Background(), cfg, fc, sc.fn(), progress)
	if err != nil {
		t.Fatalf("Install() returned error: %v", err)
	}

	// Variables and secrets are written before the scaffold commit (#6122)
	// to eliminate the race window where the workflow is live but secrets
	// don't exist yet.
	wantPhases := []string{"scaffold", "vars", "vars", "secrets", "secrets", "scaffold", "scaffold", "done"}
	if len(phases) != len(wantPhases) {
		t.Fatalf("got %d phases %v, want %d phases %v", len(phases), phases, len(wantPhases), wantPhases)
	}
	for i, want := range wantPhases {
		if phases[i] != want {
			t.Errorf("phase[%d] = %q, want %q (all phases: %v)", i, phases[i], want, phases)
			break
		}
	}
}

func TestInstall_GuardCheckError_FailsClosed(t *testing.T) {
	fc := newFakeClientWithRepo()
	fc.Errors["GetRepoVariable"] = fmt.Errorf("API rate limit")

	cfg := baseCfg()
	cfg.SkipGuardCheck = false

	sc := &fakeScaffoldCommit{}
	result, err := Install(context.Background(), cfg, fc, sc.fn(), noopProgress)
	if err == nil {
		t.Fatal("expected error when guard check fails (fail closed)")
	}

	if result == nil {
		t.Fatal("expected non-nil result on guard check failure")
	}

	if sc.called {
		t.Error("expected scaffold commit NOT to be called after guard check failure")
	}
	if len(fc.Variables) != 0 {
		t.Error("expected no variable writes after guard check failure")
	}
}

func TestInstall_SkipScaffoldAndConfig(t *testing.T) {
	fc := newFakeClientWithRepo()
	cfg := baseCfg()
	cfg.SkipScaffoldAndConfig = true

	sc := &fakeScaffoldCommit{}
	result, err := Install(context.Background(), cfg, fc, sc.fn(), noopProgress)
	if err != nil {
		t.Fatalf("Install() returned error: %v", err)
	}

	if !result.Success {
		t.Error("expected Success=true")
	}
	if sc.called {
		t.Error("expected scaffold commit NOT to be called when SkipScaffoldAndConfig=true")
	}
	if len(fc.Variables) != 0 {
		t.Error("expected no variable writes when SkipScaffoldAndConfig=true")
	}
	if len(fc.CreatedSecrets) != 0 {
		t.Error("expected no secret writes when SkipScaffoldAndConfig=true")
	}
	if result.WIFProvider != fakeWIFProvider {
		t.Errorf("result.WIFProvider = %q, want %q", result.WIFProvider, fakeWIFProvider)
	}
}

func TestInstall_VariableWriteFailure(t *testing.T) {
	fc := newFakeClientWithRepo()
	fc.Errors["CreateOrUpdateRepoVariable"] = fmt.Errorf("forbidden")

	cfg := baseCfg()
	sc := &fakeScaffoldCommit{}

	_, err := Install(context.Background(), cfg, fc, sc.fn(), noopProgress)
	if err == nil {
		t.Fatal("expected error from variable write failure")
	}
	if sc.called {
		t.Error("scaffold commit should NOT have been called — variables are written first (#6122)")
	}
}

func TestInstall_VarsAndSecretsBeforeCommit(t *testing.T) {
	fc := newFakeClientWithRepo()
	cfg := baseCfg()

	// Track call order via a scaffold commit wrapper and the progress
	// callback to verify that variables and secrets are written before
	// the scaffold is committed (#6122).
	var callOrder []string
	sc := &fakeScaffoldCommit{}
	origFn := sc.fn()
	commitFn := func(ctx context.Context, owner, repo string, files []forge.TreeFile, direct bool) error {
		callOrder = append(callOrder, "commit")
		return origFn(ctx, owner, repo, files, direct)
	}
	progress := func(_, phase, msg string) {
		if phase == "vars" && msg == "Configuring repository variables" {
			callOrder = append(callOrder, "vars")
		}
		if phase == "secrets" && msg == "Configuring repository secrets" {
			callOrder = append(callOrder, "secrets")
		}
	}

	_, err := Install(context.Background(), cfg, fc, commitFn, progress)
	if err != nil {
		t.Fatalf("Install() returned error: %v", err)
	}

	// Verify that vars and secrets entries are present — without this,
	// a progress message text change could make the test pass vacuously.
	hasVars, hasSecrets := false, false
	for _, entry := range callOrder {
		if entry == "vars" {
			hasVars = true
		}
		if entry == "secrets" {
			hasSecrets = true
		}
	}
	if !hasVars {
		t.Fatalf("vars entry not found in call order: %v", callOrder)
	}
	if !hasSecrets {
		t.Fatalf("secrets entry not found in call order: %v", callOrder)
	}

	// The commit must appear after both vars and secrets writes.
	commitIdx := -1
	for i, entry := range callOrder {
		if entry == "commit" {
			commitIdx = i
			break
		}
	}
	if commitIdx == -1 {
		t.Fatal("commit not found in call order")
	}
	for i, entry := range callOrder {
		if i >= commitIdx && (entry == "vars" || entry == "secrets") {
			t.Errorf("expected %q before commit, but it appeared at index %d (commit at %d); order: %v",
				entry, i, commitIdx, callOrder)
		}
	}
}

func TestInstall_SecretWriteFailure(t *testing.T) {
	fc := newFakeClientWithRepo()
	fc.Errors["CreateRepoSecret"] = fmt.Errorf("forbidden")

	cfg := baseCfg()
	sc := &fakeScaffoldCommit{}

	_, err := Install(context.Background(), cfg, fc, sc.fn(), noopProgress)
	if err == nil {
		t.Fatal("expected error from secret write failure")
	}
}

func TestBuildScaffoldFiles(t *testing.T) {
	cfg := baseCfg()

	files, err := BuildScaffoldFiles(cfg)
	if err != nil {
		t.Fatalf("BuildScaffoldFiles() returned error: %v", err)
	}

	if len(files) == 0 {
		t.Fatal("expected at least one scaffold file")
	}

	var hasConfig bool
	for _, f := range files {
		if f.Path == ".fullsend/config.yaml" {
			hasConfig = true
			if len(f.Content) == 0 {
				t.Error("config.yaml should have content")
			}
			if f.Mode != "100644" {
				t.Errorf("config.yaml mode = %q, want %q", f.Mode, "100644")
			}
		}
	}
	if !hasConfig {
		t.Error("expected .fullsend/config.yaml in scaffold files")
	}
}

func TestBuildScaffoldFiles_InvalidConfig(t *testing.T) {
	cfg := baseCfg()
	cfg.Roles = []string{"nonexistent-role"}

	_, err := BuildScaffoldFiles(cfg)
	if err == nil {
		t.Fatal("expected error for invalid role")
	}
}

func TestInstall_BuildScaffoldFilesError(t *testing.T) {
	fc := newFakeClientWithRepo()
	cfg := baseCfg()
	cfg.Roles = []string{"nonexistent-role"}

	sc := &fakeScaffoldCommit{}
	result, err := Install(context.Background(), cfg, fc, sc.fn(), noopProgress)
	if err == nil {
		t.Fatal("expected error from BuildScaffoldFiles failure")
	}
	if result == nil {
		t.Fatal("expected non-nil result on BuildScaffoldFiles failure")
	}
	if sc.called {
		t.Error("expected scaffold commit NOT to be called after BuildScaffoldFiles failure")
	}
}

func TestInstall_NilProgress(t *testing.T) {
	fc := newFakeClientWithRepo()
	cfg := baseCfg()
	sc := &fakeScaffoldCommit{}

	result, err := Install(context.Background(), cfg, fc, sc.fn(), nil)
	if err != nil {
		t.Fatalf("Install() returned error: %v", err)
	}
	if !result.Success {
		t.Error("expected Success=true")
	}
}

func TestInstall_CheckInstallComponents_Error(t *testing.T) {
	fc := newFakeClientWithRepo()
	fc.VariableValues["acme/widgets/"+forge.PerRepoGuardVar] = "true"
	fc.Errors["GetFileContent"] = fmt.Errorf("API error")

	cfg := baseCfg()
	cfg.SkipGuardCheck = false

	sc := &fakeScaffoldCommit{}
	_, err := Install(context.Background(), cfg, fc, sc.fn(), noopProgress)
	if err == nil {
		t.Fatal("expected error when checkInstallComponents fails")
	}
	if sc.called {
		t.Error("expected scaffold commit NOT to be called after component check failure")
	}
}

func TestCheckInstallComponents_WorkflowCheckError(t *testing.T) {
	fc := newFakeClientWithRepo()
	fc.Errors["GetFileContent"] = fmt.Errorf("API error")

	installed, err := checkInstallComponents(context.Background(), fc, "acme", "widgets", ForgeGitHub, defaultForgeConfig)
	if err == nil {
		t.Fatal("expected error from workflow file check")
	}
	if installed {
		t.Error("expected installed=false on error")
	}
}

func TestCheckInstallComponents_VariableCheckError(t *testing.T) {
	fc := newFakeClientWithRepo()
	fc.FileContents["acme/widgets/.github/workflows/fullsend.yaml"] = []byte("name: fullsend")
	fc.Errors["GetRepoVariable"] = fmt.Errorf("API rate limit")

	installed, err := checkInstallComponents(context.Background(), fc, "acme", "widgets", ForgeGitHub, defaultForgeConfig)
	if err == nil {
		t.Fatal("expected error from variable check")
	}
	if installed {
		t.Error("expected installed=false on error")
	}
}

func TestCheckInstallComponents_SecretCheckError(t *testing.T) {
	fc := newFakeClientWithRepo()
	fc.FileContents["acme/widgets/.github/workflows/fullsend.yaml"] = []byte("name: fullsend")
	fc.VariableValues["acme/widgets/FULLSEND_MINT_URL"] = "https://mint.example.com"
	fc.VariableValues["acme/widgets/FULLSEND_GCP_REGION"] = "us-central1"
	fc.VariableValues["acme/widgets/FULLSEND_CREDENTIAL_MODE"] = CredModeWIF
	fc.VariablesExist["acme/widgets/FULLSEND_CREDENTIAL_MODE"] = true
	fc.Errors["RepoSecretExists"] = fmt.Errorf("API error")

	installed, err := checkInstallComponents(context.Background(), fc, "acme", "widgets", ForgeGitHub, defaultForgeConfig)
	if err == nil {
		t.Fatal("expected error from secret check")
	}
	if installed {
		t.Error("expected installed=false on error")
	}
}

func TestCheckInstallComponents_GitLabWIF_MissingSecrets(t *testing.T) {
	fc := forge.NewFakeClient()
	fc.FileContents["acme/api/.gitlab/ci/fullsend-dispatch.yml"] = []byte("include:")
	fc.VariableValues["acme/api/FULLSEND_CREDENTIAL_MODE"] = "wif"
	fc.VariableValues["acme/api/FULLSEND_FORGE"] = "gitlab"

	installed, err := checkInstallComponents(context.Background(), fc, "acme", "api", ForgeGitLab, GitLabForgeConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if installed {
		t.Error("expected installed=false when WIF secrets are missing")
	}
}

func TestCheckInstallComponents_GitLabWIF_FullyInstalled(t *testing.T) {
	fc := forge.NewFakeClient()
	fc.FileContents["acme/api/.gitlab/ci/fullsend-dispatch.yml"] = []byte("include:")
	fc.VariableValues["acme/api/FULLSEND_CREDENTIAL_MODE"] = "wif"
	fc.VariableValues["acme/api/FULLSEND_FORGE"] = "gitlab"
	fc.Secrets["acme/api/FULLSEND_GCP_PROJECT_ID"] = true
	fc.Secrets["acme/api/FULLSEND_GCP_WIF_PROVIDER"] = true

	installed, err := checkInstallComponents(context.Background(), fc, "acme", "api", ForgeGitLab, GitLabForgeConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !installed {
		t.Error("expected installed=true when all WIF components are present")
	}
}

func TestCheckInstallComponents_GitLabToken_NoSecretsNeeded(t *testing.T) {
	fc := forge.NewFakeClient()
	fc.FileContents["acme/api/.gitlab/ci/fullsend-dispatch.yml"] = []byte("include:")
	fc.VariableValues["acme/api/FULLSEND_CREDENTIAL_MODE"] = "token"
	fc.VariableValues["acme/api/FULLSEND_FORGE"] = "gitlab"

	installed, err := checkInstallComponents(context.Background(), fc, "acme", "api", ForgeGitLab, GitLabForgeConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !installed {
		t.Error("expected installed=true for token mode without secrets")
	}
}

func TestCheckInstallComponents_GitHubOIDC_NoSecretsNeeded(t *testing.T) {
	fc := forge.NewFakeClient()
	fc.FileContents["acme/api/.github/workflows/fullsend.yml"] = []byte(shimWorkflow)
	fc.VariableValues["acme/api/FULLSEND_MINT_URL"] = "https://mint.example.com"
	fc.VariableValues["acme/api/FULLSEND_CREDENTIAL_MODE"] = "oidc"

	installed, err := checkInstallComponents(context.Background(), fc, "acme", "api", ForgeGitHub, defaultForgeConfig)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !installed {
		t.Error("expected installed=true for GitHub OIDC mode without WIF secrets")
	}
}

func TestCheckInstallComponents_GitHubWIF_NoCredModeVar_DefaultsWIF(t *testing.T) {
	fc := forge.NewFakeClient()
	fc.FileContents["acme/api/.github/workflows/fullsend.yml"] = []byte(shimWorkflow)
	fc.VariableValues["acme/api/FULLSEND_MINT_URL"] = "https://mint.example.com"
	// No FULLSEND_CREDENTIAL_MODE variable — simulates pre-existing WIF repo.

	installed, err := checkInstallComponents(context.Background(), fc, "acme", "api", ForgeGitHub, defaultForgeConfig)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if installed {
		t.Error("expected installed=false: pre-existing WIF repo without secrets should not be reported as fully installed")
	}
}

func TestCheckInstallComponents_GitHubWIF_NoCredModeVar_WithSecrets(t *testing.T) {
	fc := forge.NewFakeClient()
	fc.FileContents["acme/api/.github/workflows/fullsend.yml"] = []byte(shimWorkflow)
	fc.VariableValues["acme/api/FULLSEND_MINT_URL"] = "https://mint.example.com"
	fc.Secrets["acme/api/FULLSEND_GCP_PROJECT_ID"] = true
	fc.Secrets["acme/api/FULLSEND_GCP_WIF_PROVIDER"] = true
	// No FULLSEND_CREDENTIAL_MODE variable — simulates pre-existing WIF repo with secrets.

	installed, err := checkInstallComponents(context.Background(), fc, "acme", "api", ForgeGitHub, defaultForgeConfig)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !installed {
		t.Error("expected installed=true: pre-existing WIF repo with all secrets should be fully installed")
	}
}

func TestInstallVarsForForge_GitLab(t *testing.T) {
	cfg := InstallConfig{
		Forge: ForgeGitLab,
	}
	vars, err := installVarsForForge(cfg, "")
	if err != nil {
		t.Fatalf("installVarsForForge(GitLab) error = %v", err)
	}
	requiredKeys := []string{
		"FULLSEND_CREDENTIAL_MODE",
		"FULLSEND_FORGE",
		"FULLSEND_LAST_POLL_AT_FAST",
		"FULLSEND_LAST_POLL_AT_FULL",
		"FULLSEND_LABEL_STATE",
		forge.PerRepoGuardVar,
	}
	for _, k := range requiredKeys {
		if _, ok := vars[k]; !ok {
			t.Errorf("missing required GitLab variable %q", k)
		}
	}
	if vars["FULLSEND_CREDENTIAL_MODE"] != "token" {
		t.Errorf("FULLSEND_CREDENTIAL_MODE = %q, want %q", vars["FULLSEND_CREDENTIAL_MODE"], "token")
	}
	if vars["FULLSEND_FORGE"] != "gitlab" {
		t.Errorf("FULLSEND_FORGE = %q, want %q", vars["FULLSEND_FORGE"], "gitlab")
	}
	// GitLab vars should NOT include GitHub-specific vars.
	for _, k := range []string{"FULLSEND_MINT_URL", "FULLSEND_GCP_REGION"} {
		if _, ok := vars[k]; ok {
			t.Errorf("GitLab vars should not include %q", k)
		}
	}
}

func TestInstallVarsForForge_GitHub_OmitsEmptyRegion(t *testing.T) {
	cfg := InstallConfig{
		Forge: ForgeGitHub,
	}
	vars, err := installVarsForForge(cfg, "https://mint.example.com")
	if err != nil {
		t.Fatalf("installVarsForForge(GitHub) error = %v", err)
	}
	if _, ok := vars["FULLSEND_GCP_REGION"]; ok {
		t.Error("FULLSEND_GCP_REGION should not be set when InferenceRegion is empty")
	}
}

func TestInstallVarsForForge_GitHub_IncludesRegion(t *testing.T) {
	cfg := InstallConfig{
		Forge:           ForgeGitHub,
		InferenceRegion: "us-central1",
	}
	vars, err := installVarsForForge(cfg, "https://mint.example.com")
	if err != nil {
		t.Fatalf("installVarsForForge(GitHub) error = %v", err)
	}
	if v, ok := vars["FULLSEND_GCP_REGION"]; !ok || v != "us-central1" {
		t.Errorf("FULLSEND_GCP_REGION = %q, want %q", v, "us-central1")
	}
}

func TestInstallVarsForForge_UnsupportedForge(t *testing.T) {
	cfg := InstallConfig{Forge: "bitbucket"}
	_, err := installVarsForForge(cfg, "")
	if err == nil {
		t.Fatal("expected error for unsupported forge")
	}
	if got := err.Error(); got != `unsupported forge "bitbucket" for variable configuration` {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestInstallSecretsForForge_GitLab(t *testing.T) {
	cfg := InstallConfig{Forge: ForgeGitLab}
	secrets := installSecretsForForge(cfg, "some-provider")
	if secrets != nil {
		t.Errorf("expected nil secrets for GitLab, got %v", secrets)
	}
}

func TestInstallSecretsForForge_GitHub(t *testing.T) {
	cfg := InstallConfig{
		Forge:            ForgeGitHub,
		InferenceProject: "my-project",
	}
	secrets := installSecretsForForge(cfg, "my-provider")
	if len(secrets) != 2 {
		t.Fatalf("expected 2 secrets, got %d", len(secrets))
	}
	if secrets["FULLSEND_GCP_PROJECT_ID"] != "my-project" {
		t.Errorf("FULLSEND_GCP_PROJECT_ID = %q, want %q", secrets["FULLSEND_GCP_PROJECT_ID"], "my-project")
	}
	if secrets["FULLSEND_GCP_WIF_PROVIDER"] != "my-provider" {
		t.Errorf("FULLSEND_GCP_WIF_PROVIDER = %q, want %q", secrets["FULLSEND_GCP_WIF_PROVIDER"], "my-provider")
	}
}

func TestRequiredVarsForForge(t *testing.T) {
	ghVars := requiredVarsForForge(ForgeGitHub)
	if len(ghVars) == 0 {
		t.Fatal("expected non-empty required vars for GitHub")
	}
	glVars := requiredVarsForForge(ForgeGitLab)
	if len(glVars) == 0 {
		t.Fatal("expected non-empty required vars for GitLab")
	}
	if glVars[0] == ghVars[0] {
		t.Error("GitLab and GitHub required vars should differ")
	}
}

func TestRequiredSecretsForForge(t *testing.T) {
	ghSecretsEmpty := requiredSecretsForForge(ForgeGitHub, "")
	if ghSecretsEmpty != nil {
		t.Errorf("expected nil required secrets for GitHub with empty mode, got %v", ghSecretsEmpty)
	}
	ghSecretsWIF := requiredSecretsForForge(ForgeGitHub, CredModeWIF)
	if len(ghSecretsWIF) == 0 {
		t.Fatal("expected non-empty required secrets for GitHub WIF mode")
	}
	glSecretsToken := requiredSecretsForForge(ForgeGitLab, "token")
	if glSecretsToken != nil {
		t.Errorf("expected nil required secrets for GitLab token mode, got %v", glSecretsToken)
	}
	glSecretsWIF := requiredSecretsForForge(ForgeGitLab, "wif")
	if len(glSecretsWIF) == 0 {
		t.Fatal("expected non-empty required secrets for GitLab WIF mode")
	}
}

func TestInstall_InvalidInferenceProject(t *testing.T) {
	fc := newFakeClientWithRepo()
	cfg := baseCfg()
	cfg.InferenceProject = "x"
	cfg.SkipGuardCheck = true

	sc := &fakeScaffoldCommit{}
	_, err := Install(context.Background(), cfg, fc, sc.fn(), noopProgress)
	if err == nil {
		t.Fatal("expected error for invalid GCP project ID")
	}
	if sc.called {
		t.Error("expected scaffold commit NOT to be called after validation failure")
	}
}

func TestInstall_InvalidInferenceRegion(t *testing.T) {
	fc := newFakeClientWithRepo()
	cfg := baseCfg()
	cfg.InferenceRegion = "AB"
	cfg.SkipGuardCheck = true

	sc := &fakeScaffoldCommit{}
	_, err := Install(context.Background(), cfg, fc, sc.fn(), noopProgress)
	if err == nil {
		t.Fatal("expected error for invalid GCP region")
	}
	if sc.called {
		t.Error("expected scaffold commit NOT to be called after validation failure")
	}
}

func TestBuildScaffoldFiles_UnsupportedForge(t *testing.T) {
	cfg := InstallConfig{
		Owner: "acme",
		Repo:  "widgets",
		Forge: "bitbucket",
		Roles: []string{"triage"},
	}
	_, err := BuildScaffoldFiles(cfg)
	if err == nil {
		t.Fatal("expected error for unsupported forge")
	}
}

func TestInstall_FreshInstall_GitLab(t *testing.T) {
	fc := newFakeClientWithRepo()
	cfg := InstallConfig{
		Owner:          "acme",
		Repo:           "widgets",
		Forge:          ForgeGitLab,
		Roles:          []string{"triage"},
		Direct:         true,
		SkipGuardCheck: true,
	}
	sc := &fakeScaffoldCommit{}
	result, err := Install(context.Background(), cfg, fc, sc.fn(), noopProgress)
	if err != nil {
		t.Fatalf("Install(GitLab) returned error: %v", err)
	}
	if !result.Success {
		t.Error("expected Success=true")
	}
	if !sc.called {
		t.Error("expected scaffold commit to be called")
	}
	varMap := make(map[string]string)
	for _, v := range fc.Variables {
		varMap[v.Name] = v.Value
	}
	if varMap["FULLSEND_CREDENTIAL_MODE"] != "token" {
		t.Errorf("FULLSEND_CREDENTIAL_MODE = %q, want %q", varMap["FULLSEND_CREDENTIAL_MODE"], "token")
	}
	if varMap["FULLSEND_FORGE"] != "gitlab" {
		t.Errorf("FULLSEND_FORGE = %q, want %q", varMap["FULLSEND_FORGE"], "gitlab")
	}
	if _, ok := varMap["FULLSEND_MINT_URL"]; ok {
		t.Error("GitLab should not set FULLSEND_MINT_URL")
	}
	if len(fc.CreatedSecrets) != 0 {
		t.Errorf("expected 0 secrets for GitLab, got %d", len(fc.CreatedSecrets))
	}
}

func TestInstall_GitLab_SkipsWIFValidation(t *testing.T) {
	fc := newFakeClientWithRepo()
	cfg := InstallConfig{
		Owner:          "acme",
		Repo:           "widgets",
		Forge:          ForgeGitLab,
		Roles:          []string{"triage"},
		Direct:         true,
		SkipGuardCheck: true,
		WIFProvider:    "",
	}
	sc := &fakeScaffoldCommit{}
	result, err := Install(context.Background(), cfg, fc, sc.fn(), noopProgress)
	if err != nil {
		t.Fatalf("Install(GitLab, no WIF) returned error: %v", err)
	}
	if !result.Success {
		t.Error("expected Success=true — GitLab should skip WIF validation")
	}
}

func TestInstall_GitLab_AlreadyInstalled(t *testing.T) {
	fc := newFakeClientWithRepo()
	fullName := "acme/widgets"
	fc.VariableValues[fullName+"/"+forge.PerRepoGuardVar] = "true"
	fc.VariableValues[fullName+"/FULLSEND_CREDENTIAL_MODE"] = "token"
	fc.VariableValues[fullName+"/FULLSEND_FORGE"] = "gitlab"
	fc.FileContents[fullName+"/.gitlab/ci/fullsend-dispatch.yml"] = []byte("include:")

	cfg := InstallConfig{
		Owner:          "acme",
		Repo:           "widgets",
		Forge:          ForgeGitLab,
		Roles:          []string{"triage"},
		Direct:         true,
		SkipGuardCheck: false,
	}
	sc := &fakeScaffoldCommit{}
	result, err := Install(context.Background(), cfg, fc, sc.fn(), noopProgress)
	if err != nil {
		t.Fatalf("Install(GitLab already installed) error: %v", err)
	}
	if !result.AlreadyInstalled {
		t.Error("expected AlreadyInstalled=true")
	}
	if sc.called {
		t.Error("expected scaffold commit NOT to be called")
	}
}

func TestInstall_GitLab_ReuseSecrets(t *testing.T) {
	fc := newFakeClientWithRepo()
	cfg := InstallConfig{
		Owner:          "acme",
		Repo:           "widgets",
		Forge:          ForgeGitLab,
		Roles:          []string{"triage"},
		Direct:         true,
		SkipGuardCheck: true,
		ReuseSecrets:   true,
	}
	sc := &fakeScaffoldCommit{}
	result, err := Install(context.Background(), cfg, fc, sc.fn(), noopProgress)
	if err != nil {
		t.Fatalf("Install(GitLab, ReuseSecrets) returned error: %v", err)
	}
	if !result.Success {
		t.Error("expected Success=true")
	}
	if len(fc.CreatedSecrets) != 0 {
		t.Errorf("expected 0 secrets for GitLab ReuseSecrets, got %d", len(fc.CreatedSecrets))
	}
}

func TestInstallVarsForForge_GitLab_WithInference(t *testing.T) {
	cfg := InstallConfig{
		Forge:            ForgeGitLab,
		InferenceProject: "my-gcp-project",
		InferenceRegion:  "us-central1",
		WIFProvider:      fakeWIFProvider,
	}
	vars, err := installVarsForForge(cfg, "")
	if err != nil {
		t.Fatalf("installVarsForForge(GitLab, inference) error = %v", err)
	}
	if vars["FULLSEND_CREDENTIAL_MODE"] != "wif" {
		t.Errorf("FULLSEND_CREDENTIAL_MODE = %q, want %q", vars["FULLSEND_CREDENTIAL_MODE"], "wif")
	}
	if vars["FULLSEND_GCP_REGION"] != "us-central1" {
		t.Errorf("FULLSEND_GCP_REGION = %q, want %q", vars["FULLSEND_GCP_REGION"], "us-central1")
	}
	if _, ok := vars["FULLSEND_SA"]; ok {
		t.Error("FULLSEND_SA should not be in regular vars (it is a protected variable)")
	}
	if _, ok := vars["FULLSEND_WIF_PROVIDER"]; ok {
		t.Error("FULLSEND_WIF_PROVIDER should not be in regular vars (it is a protected variable)")
	}
	protVars := installProtectedVarsForForge(cfg)
	expectedSA := "fullsend-mint@my-gcp-project.iam.gserviceaccount.com"
	if protVars["FULLSEND_SA"] != expectedSA {
		t.Errorf("protected FULLSEND_SA = %q, want %q", protVars["FULLSEND_SA"], expectedSA)
	}
	if protVars["FULLSEND_WIF_PROVIDER"] != fakeWIFProvider {
		t.Errorf("protected FULLSEND_WIF_PROVIDER = %q, want %q", protVars["FULLSEND_WIF_PROVIDER"], fakeWIFProvider)
	}
}

func TestInstallProtectedVarsForForge_GitLab_EmptyWIFProvider(t *testing.T) {
	cfg := InstallConfig{
		Forge:            ForgeGitLab,
		InferenceProject: "my-gcp-project",
		WIFProvider:      "",
	}
	protVars := installProtectedVarsForForge(cfg)
	if _, ok := protVars["FULLSEND_WIF_PROVIDER"]; ok {
		t.Error("FULLSEND_WIF_PROVIDER should not be set when WIFProvider is empty")
	}
	expectedSA := "fullsend-mint@my-gcp-project.iam.gserviceaccount.com"
	if protVars["FULLSEND_SA"] != expectedSA {
		t.Errorf("protected FULLSEND_SA = %q, want %q", protVars["FULLSEND_SA"], expectedSA)
	}
}

func TestInstallVarsForForge_GitLab_WithoutInference(t *testing.T) {
	cfg := InstallConfig{
		Forge: ForgeGitLab,
	}
	vars, err := installVarsForForge(cfg, "")
	if err != nil {
		t.Fatalf("installVarsForForge(GitLab, no inference) error = %v", err)
	}
	if vars["FULLSEND_CREDENTIAL_MODE"] != "token" {
		t.Errorf("FULLSEND_CREDENTIAL_MODE = %q, want %q", vars["FULLSEND_CREDENTIAL_MODE"], "token")
	}
	if _, ok := vars["FULLSEND_GCP_REGION"]; ok {
		t.Error("FULLSEND_GCP_REGION should not be set without inference")
	}
	if _, ok := vars["FULLSEND_SA"]; ok {
		t.Error("FULLSEND_SA should not be set without inference")
	}
}

func TestInstallVarsForForge_GitLab_DiscoveredCredMode_PreservesWIF(t *testing.T) {
	cfg := InstallConfig{
		Forge:              ForgeGitLab,
		DiscoveredCredMode: "wif",
	}
	vars, err := installVarsForForge(cfg, "")
	if err != nil {
		t.Fatalf("installVarsForForge(GitLab, DiscoveredCredMode) error = %v", err)
	}
	if vars["FULLSEND_CREDENTIAL_MODE"] != "wif" {
		t.Errorf("FULLSEND_CREDENTIAL_MODE = %q, want %q", vars["FULLSEND_CREDENTIAL_MODE"], "wif")
	}
}

func TestInstallVarsForForge_GitLab_DiscoveredCredMode_InvalidFallsBackToToken(t *testing.T) {
	cfg := InstallConfig{
		Forge:              ForgeGitLab,
		DiscoveredCredMode: "corrupted-value",
	}
	vars, err := installVarsForForge(cfg, "")
	if err != nil {
		t.Fatalf("installVarsForForge(GitLab, invalid DiscoveredCredMode) error = %v", err)
	}
	if vars["FULLSEND_CREDENTIAL_MODE"] != "token" {
		t.Errorf("FULLSEND_CREDENTIAL_MODE = %q, want %q (invalid discovered mode should fall back to token)", vars["FULLSEND_CREDENTIAL_MODE"], "token")
	}
}

func TestInstallVarsForForge_GitLab_InferenceProjectOverridesDiscovered(t *testing.T) {
	cfg := InstallConfig{
		Forge:              ForgeGitLab,
		InferenceProject:   "my-project",
		DiscoveredCredMode: "token",
	}
	vars, err := installVarsForForge(cfg, "")
	if err != nil {
		t.Fatalf("installVarsForForge(GitLab, InferenceProject+DiscoveredCredMode) error = %v", err)
	}
	if vars["FULLSEND_CREDENTIAL_MODE"] != "wif" {
		t.Errorf("FULLSEND_CREDENTIAL_MODE = %q, want %q (InferenceProject should take precedence)", vars["FULLSEND_CREDENTIAL_MODE"], "wif")
	}
}

func TestInstallSecretsForForge_GitLab_WithInference(t *testing.T) {
	cfg := InstallConfig{
		Forge:            ForgeGitLab,
		InferenceProject: "my-gcp-project",
	}
	secrets := installSecretsForForge(cfg, fakeWIFProvider)
	if len(secrets) != 2 {
		t.Fatalf("expected 2 secrets for GitLab with inference, got %d", len(secrets))
	}
	if secrets["FULLSEND_GCP_PROJECT_ID"] != "my-gcp-project" {
		t.Errorf("FULLSEND_GCP_PROJECT_ID = %q, want %q", secrets["FULLSEND_GCP_PROJECT_ID"], "my-gcp-project")
	}
	if secrets["FULLSEND_GCP_WIF_PROVIDER"] != fakeWIFProvider {
		t.Errorf("FULLSEND_GCP_WIF_PROVIDER = %q, want %q", secrets["FULLSEND_GCP_WIF_PROVIDER"], fakeWIFProvider)
	}
}

func TestInstallSecretsForForge_GitLab_WithoutInference(t *testing.T) {
	cfg := InstallConfig{Forge: ForgeGitLab}
	secrets := installSecretsForForge(cfg, "some-provider")
	if secrets != nil {
		t.Errorf("expected nil secrets for GitLab without inference, got %v", secrets)
	}
}

func TestInstall_GitLab_WithInference(t *testing.T) {
	fc := newFakeClientWithRepo()
	cfg := InstallConfig{
		Owner:            "acme",
		Repo:             "widgets",
		Forge:            ForgeGitLab,
		Roles:            []string{"triage"},
		InferenceProject: "my-gcp-project",
		InferenceRegion:  "us-central1",
		WIFProvider:      fakeWIFProvider,
		Direct:           true,
		SkipGuardCheck:   true,
	}
	sc := &fakeScaffoldCommit{}
	result, err := Install(context.Background(), cfg, fc, sc.fn(), noopProgress)
	if err != nil {
		t.Fatalf("Install(GitLab, inference) returned error: %v", err)
	}
	if !result.Success {
		t.Error("expected Success=true")
	}

	varMap := make(map[string]string)
	for _, v := range fc.Variables {
		varMap[v.Name] = v.Value
	}
	if varMap["FULLSEND_CREDENTIAL_MODE"] != "wif" {
		t.Errorf("FULLSEND_CREDENTIAL_MODE = %q, want %q", varMap["FULLSEND_CREDENTIAL_MODE"], "wif")
	}
	if varMap["FULLSEND_GCP_REGION"] != "us-central1" {
		t.Errorf("FULLSEND_GCP_REGION = %q, want %q", varMap["FULLSEND_GCP_REGION"], "us-central1")
	}
	if _, ok := varMap["FULLSEND_SA"]; ok {
		t.Error("FULLSEND_SA should not be in regular vars (it is a protected variable)")
	}
	if _, ok := varMap["FULLSEND_WIF_PROVIDER"]; ok {
		t.Error("FULLSEND_WIF_PROVIDER should not be in regular vars (it is a protected variable)")
	}
	expectedSA := "fullsend-mint@my-gcp-project.iam.gserviceaccount.com"
	protVarMap := make(map[string]string)
	for _, v := range fc.CreatedProtectedVars {
		protVarMap[v.Name] = v.Value
	}
	if protVarMap["FULLSEND_SA"] != expectedSA {
		t.Errorf("protected FULLSEND_SA = %q, want %q", protVarMap["FULLSEND_SA"], expectedSA)
	}
	if protVarMap["FULLSEND_WIF_PROVIDER"] != fakeWIFProvider {
		t.Errorf("protected FULLSEND_WIF_PROVIDER = %q, want %q", protVarMap["FULLSEND_WIF_PROVIDER"], fakeWIFProvider)
	}

	// Verify secrets were written for GitLab with inference.
	secretMap := make(map[string]string)
	for _, s := range fc.CreatedSecrets {
		secretMap[s.Name] = s.Value
	}
	if secretMap["FULLSEND_GCP_PROJECT_ID"] != "my-gcp-project" {
		t.Errorf("FULLSEND_GCP_PROJECT_ID = %q, want %q", secretMap["FULLSEND_GCP_PROJECT_ID"], "my-gcp-project")
	}
	if secretMap["FULLSEND_GCP_WIF_PROVIDER"] != fakeWIFProvider {
		t.Errorf("FULLSEND_GCP_WIF_PROVIDER = %q, want %q", secretMap["FULLSEND_GCP_WIF_PROVIDER"], fakeWIFProvider)
	}
}

func TestInstall_GitLab_WithInference_EmptyWIFProvider_Rejected(t *testing.T) {
	fc := newFakeClientWithRepo()
	cfg := InstallConfig{
		Owner:            "acme",
		Repo:             "widgets",
		Forge:            ForgeGitLab,
		Roles:            []string{"triage"},
		InferenceProject: "my-gcp-project",
		InferenceRegion:  "us-central1",
		WIFProvider:      "", // must be set when inference is configured
		Direct:           true,
		SkipGuardCheck:   true,
	}
	sc := &fakeScaffoldCommit{}
	_, err := Install(context.Background(), cfg, fc, sc.fn(), noopProgress)
	if err == nil {
		t.Fatal("expected error when WIF provider is empty with inference configured")
	}
}

func TestBuildScaffoldFiles_GitLab(t *testing.T) {
	cfg := InstallConfig{
		Owner: "acme",
		Repo:  "widgets",
		Forge: ForgeGitLab,
		Roles: []string{"triage", "coder"},
	}
	files, err := BuildScaffoldFiles(cfg)
	if err != nil {
		t.Fatalf("BuildScaffoldFiles(GitLab) error = %v", err)
	}
	if len(files) == 0 {
		t.Fatal("expected scaffold files for GitLab, got 0")
	}
	paths := make(map[string]bool)
	for _, f := range files {
		paths[f.Path] = true
	}
	for _, expected := range []string{
		".gitlab-ci.yml",
		".gitlab/ci/fullsend-agent.yml",
		".gitlab/ci/fullsend-dispatch.yml",
		".gitlab/ci/fullsend-poll.yml",
		".fullsend/config.yaml",
	} {
		if !paths[expected] {
			t.Errorf("missing expected scaffold file %q", expected)
		}
	}
}

func TestInstall_GitHub_OIDCMode_SkipsWIFSecrets(t *testing.T) {
	fc := newFakeClientWithRepo()
	cfg := baseCfg()
	cfg.CredentialMode = CredModeOIDC
	cfg.WIFProvider = "" // not needed in OIDC mode
	cfg.InferenceProject = ""

	sc := &fakeScaffoldCommit{}
	result, err := Install(context.Background(), cfg, fc, sc.fn(), noopProgress)
	if err != nil {
		t.Fatalf("Install(GitHub, OIDC) returned error: %v", err)
	}
	if !result.Success {
		t.Error("expected Success=true")
	}
	if !sc.called {
		t.Error("expected scaffold commit to be called")
	}

	// Verify no secrets were written (OIDC mode doesn't need WIF).
	if len(fc.CreatedSecrets) != 0 {
		t.Errorf("expected 0 secrets for OIDC mode, got %d", len(fc.CreatedSecrets))
	}

	// Verify FULLSEND_CREDENTIAL_MODE is set to oidc.
	varMap := make(map[string]string)
	for _, v := range fc.Variables {
		varMap[v.Name] = v.Value
	}
	if varMap["FULLSEND_CREDENTIAL_MODE"] != "oidc" {
		t.Errorf("FULLSEND_CREDENTIAL_MODE = %q, want %q", varMap["FULLSEND_CREDENTIAL_MODE"], "oidc")
	}
	if varMap["FULLSEND_MINT_URL"] != "https://mint.example.com" {
		t.Errorf("FULLSEND_MINT_URL = %q, want %q", varMap["FULLSEND_MINT_URL"], "https://mint.example.com")
	}
}

func TestInstall_GitHub_WIFMode_RequiresWIFProvider(t *testing.T) {
	fc := newFakeClientWithRepo()
	cfg := baseCfg()
	cfg.CredentialMode = CredModeWIF
	cfg.WIFProvider = ""

	sc := &fakeScaffoldCommit{}
	_, err := Install(context.Background(), cfg, fc, sc.fn(), noopProgress)
	if err == nil {
		t.Fatal("expected error when WIF provider is empty in WIF mode")
	}
}

func TestInstallSecretsForForge_GitHub_OIDCMode_NoSecrets(t *testing.T) {
	cfg := InstallConfig{
		Forge:          ForgeGitHub,
		CredentialMode: CredModeOIDC,
	}
	secrets := installSecretsForForge(cfg, "")
	if secrets != nil {
		t.Errorf("expected nil secrets for GitHub OIDC mode, got %v", secrets)
	}
}

func TestRequiredSecretsForForge_GitHub_OIDCMode(t *testing.T) {
	secrets := requiredSecretsForForge(ForgeGitHub, CredModeOIDC)
	if secrets != nil {
		t.Errorf("expected nil required secrets for GitHub OIDC mode, got %v", secrets)
	}
}

func TestRequiredSecretsForForge_GitHub_WIFMode(t *testing.T) {
	secrets := requiredSecretsForForge(ForgeGitHub, CredModeWIF)
	if len(secrets) == 0 {
		t.Fatal("expected non-empty required secrets for GitHub WIF mode")
	}
}

func TestRequiredSecretsForForge_GitHub_EmptyMode_NoSecrets(t *testing.T) {
	secrets := requiredSecretsForForge(ForgeGitHub, "")
	if secrets != nil {
		t.Errorf("expected nil required secrets for GitHub with empty credential mode, got %v", secrets)
	}
}

func TestResolveCredentialMode(t *testing.T) {
	tests := []struct {
		name             string
		forge            string
		mode             string
		inferenceProject string
		discoveredMode   string
		want             string
	}{
		{"explicit wif", ForgeGitHub, CredModeWIF, "", "", CredModeWIF},
		{"explicit oidc", ForgeGitHub, CredModeOIDC, "", "", CredModeOIDC},
		{"explicit token", ForgeGitLab, CredModeToken, "", "", CredModeToken},
		{"github empty defaults to oidc", ForgeGitHub, "", "", "", CredModeOIDC},
		{"github with inference defaults to wif", ForgeGitHub, "", "proj", "", CredModeWIF},
		{"github discovered wif", ForgeGitHub, "", "", CredModeWIF, CredModeWIF},
		{"github discovered oidc", ForgeGitHub, "", "", CredModeOIDC, CredModeOIDC},
		{"gitlab empty defaults to token", ForgeGitLab, "", "", "", CredModeToken},
		{"gitlab with inference defaults to wif", ForgeGitLab, "", "proj", "", CredModeWIF},
		{"gitlab discovered wif", ForgeGitLab, "", "", CredModeWIF, CredModeWIF},
		{"gitlab discovered token", ForgeGitLab, "", "", CredModeToken, CredModeToken},
		{"gitlab invalid discovered defaults to token", ForgeGitLab, "", "", "invalid", CredModeToken},
		{"gitlab discovered variable normalizes to token", ForgeGitLab, "", "", "variable", CredModeToken},
		{"gitlab inference overrides discovered", ForgeGitLab, "", "proj", CredModeToken, CredModeWIF},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveCredentialMode(tt.forge, tt.mode, tt.inferenceProject, tt.discoveredMode)
			if got != tt.want {
				t.Errorf("resolveCredentialMode(%q, %q, %q, %q) = %q, want %q",
					tt.forge, tt.mode, tt.inferenceProject, tt.discoveredMode, got, tt.want)
			}
		})
	}
}

func TestInstall_InvalidCredentialMode(t *testing.T) {
	fc := forge.NewFakeClient()
	fc.Repos = append(fc.Repos, forge.Repository{
		FullName:      "acme/api",
		Name:          "api",
		DefaultBranch: "main",
	})

	cfg := InstallConfig{
		Owner:          "acme",
		Repo:           "api",
		Forge:          ForgeGitHub,
		CredentialMode: "token",
		SkipGuardCheck: true,
	}
	_, err := Install(context.Background(), cfg, fc, nil, nil)
	if err == nil {
		t.Fatal("expected error for invalid credential mode")
	}
	if !strings.Contains(err.Error(), "invalid credential mode") {
		t.Errorf("expected 'invalid credential mode' error, got: %v", err)
	}
}
