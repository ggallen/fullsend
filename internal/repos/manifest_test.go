package repos

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fullsend-ai/fullsend/internal/forge"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// validManifest is shared across parse, validate, resolve, and
// round-trip tests that all need the same well-formed baseline.
const validManifest = `
version: 1
forge:
  github:
    mint_url: https://mint.example.com
    fullsend_ref: main
defaults:
  forge: github
  allowed_remote_resources:
    - resource-a
    - resource-b
repos:
  - acme/repo-one
  - acme/repo-two
`

func TestParseSimpleManifest(t *testing.T) {
	var m Manifest
	err := yaml.Unmarshal([]byte(validManifest), &m)
	require.NoError(t, err)

	assert.Equal(t, 1, m.Version)
	assert.Equal(t, "https://mint.example.com", m.Forge.GitHub.MintURL)
	assert.Equal(t, "main", m.Forge.GitHub.FullsendRef)
	assert.Equal(t, []string{"resource-a", "resource-b"}, m.Defaults.AllowedRemoteResources)
	require.Len(t, m.Repos, 2)
	assert.Equal(t, "acme/repo-one", m.Repos[0].Repo)
	assert.Equal(t, "acme/repo-two", m.Repos[1].Repo)
}

func TestParseMixedStringAndObjectRepos(t *testing.T) {
	input := `
version: 1
forge:
  github:
    mint_url: https://mint.example.com
repos:
  - acme/simple
  - repo: acme/custom
    forge: gitlab
  - acme/another-simple
`
	var m Manifest
	err := yaml.Unmarshal([]byte(input), &m)
	require.NoError(t, err)

	require.Len(t, m.Repos, 3)

	assert.Equal(t, "acme/simple", m.Repos[0].Repo)
	assert.False(t, m.Repos[0].Forge.Set)

	assert.Equal(t, "acme/custom", m.Repos[1].Repo)
	assert.True(t, m.Repos[1].Forge.Set)
	assert.Equal(t, "gitlab", m.Repos[1].Forge.Value)

	assert.Equal(t, "acme/another-simple", m.Repos[2].Repo)
}

func TestParseManifestWithGlobPatterns(t *testing.T) {
	input := `
version: 1
forge:
  github:
    mint_url: https://mint.example.com

repos:
  - acme/*
  - repo: other-org/service-*
    forge: gitlab
`
	var m Manifest
	err := yaml.Unmarshal([]byte(input), &m)
	require.NoError(t, err)

	require.Len(t, m.Repos, 2)
	assert.Equal(t, "acme/*", m.Repos[0].Repo)
	assert.Equal(t, "other-org/service-*", m.Repos[1].Repo)
	assert.Equal(t, "gitlab", m.Repos[1].Forge.Value)
}

func TestRepoEntryUnmarshalYAML_StringForm(t *testing.T) {
	var entry RepoEntry
	node := &yaml.Node{Kind: yaml.ScalarNode, Value: "acme/my-repo"}
	err := entry.UnmarshalYAML(node)
	require.NoError(t, err)
	assert.Equal(t, "acme/my-repo", entry.Repo)
	assert.False(t, entry.Forge.Set)
}

func TestRepoEntryUnmarshalYAML_ObjectForm(t *testing.T) {
	input := `
repo: acme/my-repo
forge: gitlab
`
	var entry RepoEntry
	err := yaml.Unmarshal([]byte(input), &entry)
	require.NoError(t, err)
	assert.Equal(t, "acme/my-repo", entry.Repo)
	assert.True(t, entry.Forge.Set)
	assert.Equal(t, "gitlab", entry.Forge.Value)
}

func TestNullableString_Omitted(t *testing.T) {
	input := `repo: acme/test`
	var entry RepoEntry
	err := yaml.Unmarshal([]byte(input), &entry)
	require.NoError(t, err)
	assert.False(t, entry.Forge.Set)
	assert.False(t, entry.Forge.Null)
	assert.Equal(t, "", entry.Forge.Value)
	assert.True(t, entry.Forge.IsZero())
}

func TestNullableString_ExplicitNull(t *testing.T) {
	input := `
repo: acme/test
forge: null
`
	var entry RepoEntry
	err := yaml.Unmarshal([]byte(input), &entry)
	require.NoError(t, err)
	assert.True(t, entry.Forge.Set)
	assert.True(t, entry.Forge.Null)
	assert.False(t, entry.Forge.IsZero())
}

func TestNullableString_ExplicitValue(t *testing.T) {
	input := `
repo: acme/test
forge: gitlab
`
	var entry RepoEntry
	err := yaml.Unmarshal([]byte(input), &entry)
	require.NoError(t, err)
	assert.True(t, entry.Forge.Set)
	assert.False(t, entry.Forge.Null)
	assert.Equal(t, "gitlab", entry.Forge.Value)
	assert.False(t, entry.Forge.IsZero())
}

func TestNullableString_EmptyString(t *testing.T) {
	input := `
repo: acme/test
forge: ""
`
	var entry RepoEntry
	err := yaml.Unmarshal([]byte(input), &entry)
	require.NoError(t, err)
	assert.True(t, entry.Forge.Set)
	assert.False(t, entry.Forge.Null)
	assert.Equal(t, "", entry.Forge.Value)
}

func TestNullableString_DirectUnmarshal(t *testing.T) {
	type wrapper struct {
		Field NullableString `yaml:"field"`
	}

	t.Run("value", func(t *testing.T) {
		var w wrapper
		require.NoError(t, yaml.Unmarshal([]byte("field: hello"), &w))
		assert.True(t, w.Field.Set)
		assert.False(t, w.Field.Null)
		assert.Equal(t, "hello", w.Field.Value)
	})

	t.Run("null via struct leaves zero value", func(t *testing.T) {
		// yaml.v3 skips UnmarshalYAML for null-tagged struct fields,
		// leaving the field at its zero value. This is why RepoEntry
		// uses decodeNullable for correct null detection.
		var w wrapper
		require.NoError(t, yaml.Unmarshal([]byte("field: null"), &w))
		assert.False(t, w.Field.Set, "yaml.v3 does not call UnmarshalYAML for null struct fields")
	})

	t.Run("null via direct node decode", func(t *testing.T) {
		node := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}
		var ns NullableString
		require.NoError(t, ns.UnmarshalYAML(node))
		assert.True(t, ns.Set)
		assert.True(t, ns.Null)
	})

	t.Run("empty", func(t *testing.T) {
		var w wrapper
		require.NoError(t, yaml.Unmarshal([]byte("other: value"), &w))
		assert.False(t, w.Field.Set)
	})
}

func TestNullableString_ReuseClears(t *testing.T) {
	// Verify that unmarshalling a non-null value into a NullableString
	// that previously held null clears the Null flag.
	var ns NullableString

	// First: set to null.
	nullNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}
	require.NoError(t, ns.UnmarshalYAML(nullNode))
	assert.True(t, ns.Null)

	// Second: set to a value — Null must be cleared.
	valueNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "hello"}
	require.NoError(t, ns.UnmarshalYAML(valueNode))
	assert.True(t, ns.Set)
	assert.False(t, ns.Null, "Null must be cleared when decoding a non-null value")
	assert.Equal(t, "hello", ns.Value)
}

func TestDecodeNullable_ReuseClears(t *testing.T) {
	var ns NullableString

	// First: decode null.
	nullNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}
	require.NoError(t, decodeNullable(nullNode, &ns))
	assert.True(t, ns.Null)

	// Second: decode a value — Null must be cleared.
	valueNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "world"}
	require.NoError(t, decodeNullable(valueNode, &ns))
	assert.True(t, ns.Set)
	assert.False(t, ns.Null, "Null must be cleared when decoding a non-null value")
	assert.Equal(t, "world", ns.Value)
}

func TestNullableString_MarshalYAML(t *testing.T) {
	tests := []struct {
		name string
		ns   NullableString
	}{
		{"omitted", NullableString{}},
		{"null", NullableString{Set: true, Null: true}},
		{"value", NullableString{Set: true, Value: "hello"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val, err := tt.ns.MarshalYAML()
			require.NoError(t, err)
			switch tt.name {
			case "omitted":
				assert.Nil(t, val)
			case "null":
				node, ok := val.(*yaml.Node)
				require.True(t, ok)
				assert.Equal(t, "!!null", node.Tag)
			case "value":
				assert.Equal(t, "hello", val)
			}
		})
	}
}

func TestValidate_Valid(t *testing.T) {
	var m Manifest
	err := yaml.Unmarshal([]byte(validManifest), &m)
	require.NoError(t, err)
	assert.NoError(t, m.Validate())
}

func TestValidate_WrongVersion(t *testing.T) {
	input := `
version: 2
forge:
  github:
    mint_url: https://mint.example.com

repos:
  - acme/repo
`
	var m Manifest
	require.NoError(t, yaml.Unmarshal([]byte(input), &m))
	err := m.Validate()
	assert.ErrorContains(t, err, "unsupported manifest version 2")
}

func TestValidate_MissingMintURL_GitHubRepos(t *testing.T) {
	m := Manifest{
		Version:  1,
		Forge:    ForgeSection{GitHub: GitHubForgeInfra{}},
		Defaults: DefaultsConfig{Forge: "github"},
		Repos:    []RepoEntry{{Repo: "acme/repo"}},
	}
	err := m.Validate()
	assert.ErrorContains(t, err, "forge.github.mint_url is required")
}

func TestValidate_InvalidMintURL_GitHubRepos(t *testing.T) {
	m := Manifest{
		Version:  1,
		Forge:    ForgeSection{GitHub: GitHubForgeInfra{MintURL: "http://not-https.example.com"}},
		Defaults: DefaultsConfig{Forge: "github"},
		Repos:    []RepoEntry{{Repo: "acme/repo"}},
	}
	err := m.Validate()
	assert.ErrorContains(t, err, "forge.github.mint_url must be a valid HTTPS URL")
}

func TestValidate_GitLabOnly_NoMintRequired(t *testing.T) {
	m := Manifest{
		Version:  1,
		Forge:    ForgeSection{GitLab: GitLabForgeInfra{URL: "https://gitlab.example.com"}},
		Defaults: DefaultsConfig{Forge: "gitlab"},
		Repos:    []RepoEntry{{Repo: "acme/repo"}},
	}
	assert.NoError(t, m.Validate())
}

func TestValidate_MixedForge_RequiresMint(t *testing.T) {
	m := Manifest{
		Version:  1,
		Forge:    ForgeSection{GitLab: GitLabForgeInfra{URL: "https://gitlab.example.com"}},
		Defaults: DefaultsConfig{Forge: "gitlab"},
		Repos: []RepoEntry{
			{Repo: "gitlab-group/repo"},
			{Repo: "gh-org/repo", Forge: NullableString{Set: true, Value: "github"}},
		},
	}
	err := m.Validate()
	assert.ErrorContains(t, err, "forge.github.mint_url is required")
}

func TestValidate_InvalidRepoFormat(t *testing.T) {
	tests := []struct {
		name  string
		entry string
	}{
		{"no slash", "just-a-name"},
		{"empty owner", "/repo"},
		{"empty repo", "owner/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := `
version: 1
forge:
  github:
    mint_url: https://mint.example.com

defaults:
  forge: github
repos:
  - ` + tt.entry + `
`
			var m Manifest
			require.NoError(t, yaml.Unmarshal([]byte(input), &m))
			err := m.Validate()
			assert.ErrorContains(t, err, "must be in owner/repo format")
		})
	}
}

func TestValidate_EmptyRepoField(t *testing.T) {
	m := Manifest{
		Version: 1,
		Forge: ForgeSection{GitHub: GitHubForgeInfra{
			MintURL: "https://mint.example.com",
		}},
		Defaults: DefaultsConfig{Forge: "github"},
		Repos:    []RepoEntry{{Repo: ""}},
	}
	err := m.Validate()
	assert.ErrorContains(t, err, "repo field is required")
}

func TestValidate_DuplicateRepos(t *testing.T) {
	input := `
version: 1
forge:
  github:
    mint_url: https://mint.example.com

defaults:
  forge: github
repos:
  - acme/repo
  - acme/repo
`
	var m Manifest
	require.NoError(t, yaml.Unmarshal([]byte(input), &m))
	err := m.Validate()
	assert.ErrorContains(t, err, "duplicate repo")
}

func TestValidate_InvalidGlob(t *testing.T) {
	input := `
version: 1
forge:
  github:
    mint_url: https://mint.example.com

defaults:
  forge: github
repos:
  - acme/[invalid
`
	var m Manifest
	require.NoError(t, yaml.Unmarshal([]byte(input), &m))
	err := m.Validate()
	assert.ErrorContains(t, err, "invalid glob pattern")
}

func TestValidate_ValidGlob(t *testing.T) {
	input := `
version: 1
forge:
  github:
    mint_url: https://mint.example.com

defaults:
  forge: github
repos:
  - acme/service-*
  - acme/lib-[abc]
`
	var m Manifest
	require.NoError(t, yaml.Unmarshal([]byte(input), &m))
	assert.NoError(t, m.Validate())
}

func TestValidate_InferenceProjectNumberNoLongerRequired(t *testing.T) {
	// InferenceProjectNumber is now an install-time-only CLI flag,
	// not stored in the manifest. Validate should not reject it.
	m := Manifest{
		Version:  1,
		Forge:    ForgeSection{GitHub: GitHubForgeInfra{MintURL: "https://mint.example.com"}},
		Defaults: DefaultsConfig{Forge: "github"},
		Repos:    []RepoEntry{{Repo: "acme/repo"}},
	}
	err := m.Validate()
	assert.NoError(t, err)
}

func TestValidate_DeprecatedFieldsNowError(t *testing.T) {
	// Removed fields (inference_project, base_harness) are rejected
	// as unknown by the custom UnmarshalYAML.
	input := `
version: 1
forge:
  github:
    mint_url: https://mint.example.com
defaults:
  forge: github
repos:
  - repo: acme/repo
    inference_project: old-proj
`
	dir := t.TempDir()
	p := filepath.Join(dir, "repos.yaml")
	require.NoError(t, os.WriteFile(p, []byte(input), 0o644))
	_, err := LoadManifest(context.Background(), p)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown field")
}

func TestValidate_InvalidForgeFullsendRef(t *testing.T) {
	m := Manifest{
		Version:  1,
		Forge:    ForgeSection{GitHub: GitHubForgeInfra{MintURL: "https://mint.example.com", FullsendRef: "v1.0.0; rm -rf /"}},
		Defaults: DefaultsConfig{Forge: "github"},
		Repos:    []RepoEntry{{Repo: "acme/repo"}},
	}
	err := m.Validate()
	assert.ErrorContains(t, err, "forge.github.fullsend_ref")
	assert.ErrorContains(t, err, "invalid characters")
}

func TestValidate_OwnerWildcard(t *testing.T) {
	tests := []struct {
		name string
		repo string
	}{
		{"star in owner", "*/service-*"},
		{"question mark in owner", "acme?/repo"},
		{"bracket in owner", "[abc]/repo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := Manifest{
				Version:  1,
				Forge:    ForgeSection{GitHub: GitHubForgeInfra{MintURL: "https://mint.example.com"}},
				Defaults: DefaultsConfig{Forge: "github"},
				Repos:    []RepoEntry{{Repo: tt.repo}},
			}
			err := m.Validate()
			assert.ErrorContains(t, err, "glob characters are not allowed in owner segment")
		})
	}
}

func TestValidate_ForgeRequired(t *testing.T) {
	m := Manifest{
		Version: 1,
		Forge: ForgeSection{GitHub: GitHubForgeInfra{
			MintURL: "https://mint.example.com",
		}},
		Repos: []RepoEntry{{Repo: "acme/repo"}},
	}
	err := m.Validate()
	assert.ErrorContains(t, err, "forge is required")
}

func TestValidate_InvalidDefaultForge(t *testing.T) {
	m := Manifest{
		Version: 1,
		Forge: ForgeSection{GitHub: GitHubForgeInfra{
			MintURL: "https://mint.example.com",
		}},
		Defaults: DefaultsConfig{Forge: "bitbucket"},
		Repos:    []RepoEntry{{Repo: "acme/repo"}},
	}
	err := m.Validate()
	assert.ErrorContains(t, err, "not a supported forge")
}

func TestValidate_InvalidPerRepoForge(t *testing.T) {
	m := Manifest{
		Version: 1,
		Forge: ForgeSection{GitHub: GitHubForgeInfra{
			MintURL: "https://mint.example.com",
		}},
		Defaults: DefaultsConfig{Forge: "github"},
		Repos: []RepoEntry{{
			Repo:  "acme/repo",
			Forge: NullableString{Set: true, Value: "svn"},
		}},
	}
	err := m.Validate()
	assert.ErrorContains(t, err, "not supported")
}

func TestValidate_GitLabForge(t *testing.T) {
	m := Manifest{
		Version: 1,
		Forge: ForgeSection{
			GitHub: GitHubForgeInfra{
				MintURL: "https://mint.example.com",
			},
			GitLab: GitLabForgeInfra{URL: "https://gitlab.example.com"},
		},
		Defaults: DefaultsConfig{Forge: "gitlab"},
		Repos:    []RepoEntry{{Repo: "acme/repo"}},
	}
	assert.NoError(t, m.Validate())
}

func TestValidate_PerRepoForgeOverride(t *testing.T) {
	m := Manifest{
		Version: 1,
		Forge: ForgeSection{
			GitHub: GitHubForgeInfra{
				MintURL: "https://mint.example.com",
			},
			GitLab: GitLabForgeInfra{URL: "https://gitlab.example.com"},
		},
		Defaults: DefaultsConfig{Forge: "github"},
		Repos: []RepoEntry{{
			Repo:  "acme/repo",
			Forge: NullableString{Set: true, Value: "gitlab"},
		}},
	}
	assert.NoError(t, m.Validate())
}

func TestRepoEntryUnmarshalYAML_ForgeField(t *testing.T) {
	input := `
repo: acme/my-repo
forge: gitlab
`
	var entry RepoEntry
	err := yaml.Unmarshal([]byte(input), &entry)
	require.NoError(t, err)
	assert.Equal(t, "acme/my-repo", entry.Repo)
	assert.True(t, entry.Forge.Set)
	assert.Equal(t, "gitlab", entry.Forge.Value)
}

func TestRepoEntryUnmarshalYAML_DeprecatedFieldsRejected(t *testing.T) {
	tests := []struct {
		name  string
		input string
		field string
	}{
		{
			name:  "inference_project",
			input: "repo: acme/my-repo\ninference_project: old-proj\n",
			field: "inference_project",
		},
		{
			name:  "base_harness",
			input: "repo: acme/my-repo\nbase_harness: https://example.com/harness.yaml\n",
			field: "base_harness",
		},
		{
			name:  "inference_region",
			input: "repo: acme/my-repo\ninference_region: us-east1\n",
			field: "inference_region",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var entry RepoEntry
			err := yaml.Unmarshal([]byte(tt.input), &entry)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "unknown field")
			assert.Contains(t, err.Error(), tt.field)
		})
	}
}

func TestRepoEntryUnmarshalYAML_UnknownFieldRejected(t *testing.T) {
	input := `
repo: acme/my-repo
bogus: value
`
	var entry RepoEntry
	err := yaml.Unmarshal([]byte(input), &entry)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown field")
}

func TestResolveConfig_IncludesForge(t *testing.T) {
	var m Manifest
	err := yaml.Unmarshal([]byte(validManifest), &m)
	require.NoError(t, err)

	cfg, ok := m.ResolveConfig("acme", "repo-one")
	require.True(t, ok)
	assert.Equal(t, "github", cfg.Forge)
}

func TestExpandGlobs(t *testing.T) {
	input := `
version: 1
forge:
  github:
    mint_url: https://mint.example.com
defaults:
  forge: github
repos:
  - acme/explicit-repo
  - acme/service-*
`
	var m Manifest
	require.NoError(t, yaml.Unmarshal([]byte(input), &m))

	fc := forge.NewFakeClient()
	fc.Repos = []forge.Repository{
		{Name: "explicit-repo", FullName: "acme/explicit-repo"},
		{Name: "service-api", FullName: "acme/service-api"},
		{Name: "service-web", FullName: "acme/service-web"},
		{Name: "lib-utils", FullName: "acme/lib-utils"},
		// Archived and fork repos are always excluded.
		{Name: "service-old", FullName: "acme/service-old", Archived: true},
		{Name: "service-fork", FullName: "acme/service-fork", Fork: true},
		// Private repos are included because ExpandGlobs passes
		// includePrivate=true (repos.yaml is per-repo mode).
		{Name: "service-priv", FullName: "acme/service-priv", Private: true},
	}

	ctx := context.Background()
	resolved, err := m.ExpandGlobs(ctx, newTestClientFactory(fc))
	require.NoError(t, err)

	// Should have: explicit-repo, service-api, service-priv, service-web
	// (not lib-utils which doesn't match the glob, not archived/fork).
	require.Len(t, resolved, 4)

	// Sorted alphabetically.
	assert.Equal(t, "acme", resolved[0].Owner)
	assert.Equal(t, "explicit-repo", resolved[0].Repo)
	assert.Equal(t, "acme/explicit-repo", resolved[0].Entry.Repo)

	assert.Equal(t, "acme", resolved[1].Owner)
	assert.Equal(t, "service-api", resolved[1].Repo)

	assert.Equal(t, "acme", resolved[2].Owner)
	assert.Equal(t, "service-priv", resolved[2].Repo)

	assert.Equal(t, "acme", resolved[3].Owner)
	assert.Equal(t, "service-web", resolved[3].Repo)
}

func TestExpandGlobs_IncludesPrivateRepos(t *testing.T) {
	input := `
version: 1
forge:
  github:
    mint_url: https://mint.example.com

repos:
  - acme/*
`
	var m Manifest
	require.NoError(t, yaml.Unmarshal([]byte(input), &m))

	fc := forge.NewFakeClient()
	fc.Repos = []forge.Repository{
		{Name: "public-repo", FullName: "acme/public-repo"},
		{Name: "private-repo", FullName: "acme/private-repo", Private: true},
		{Name: "archived-repo", FullName: "acme/archived-repo", Archived: true},
		{Name: "forked-repo", FullName: "acme/forked-repo", Fork: true},
	}

	ctx := context.Background()
	resolved, err := m.ExpandGlobs(ctx, newTestClientFactory(fc))
	require.NoError(t, err)

	// Private repos should be included (per-repo mode), but archived
	// and forked repos remain excluded.
	require.Len(t, resolved, 2)

	repoNames := make(map[string]bool)
	for _, rr := range resolved {
		repoNames[rr.Repo] = true
	}
	assert.True(t, repoNames["public-repo"], "public repo should be included")
	assert.True(t, repoNames["private-repo"], "private repo should be included in per-repo mode")
	assert.False(t, repoNames["archived-repo"], "archived repo should be excluded")
	assert.False(t, repoNames["forked-repo"], "forked repo should be excluded")
}

func TestExpandGlobs_ExplicitWinsOverGlob(t *testing.T) {
	input := `
version: 1
forge:
  github:
    mint_url: https://mint.example.com

defaults:
  forge: github
repos:
  - repo: acme/service-api
    forge: github
  - acme/service-*
`
	var m Manifest
	require.NoError(t, yaml.Unmarshal([]byte(input), &m))

	fc := forge.NewFakeClient()
	fc.Repos = []forge.Repository{
		{Name: "service-api", FullName: "acme/service-api"},
		{Name: "service-web", FullName: "acme/service-web"},
	}

	ctx := context.Background()
	resolved, err := m.ExpandGlobs(ctx, newTestClientFactory(fc))
	require.NoError(t, err)

	require.Len(t, resolved, 2)

	// service-api should use the explicit entry (with forge override).
	for _, rr := range resolved {
		if rr.Repo == "service-api" {
			assert.True(t, rr.Entry.Forge.Set)
			assert.Equal(t, "github", rr.Entry.Forge.Value)
		}
	}
}

func TestExpandGlobs_ListOrgReposError(t *testing.T) {
	input := `
version: 1
forge:
  github:
    mint_url: https://mint.example.com

repos:
  - acme/*
`
	var m Manifest
	require.NoError(t, yaml.Unmarshal([]byte(input), &m))

	fc := forge.NewFakeClient()
	fc.Errors = map[string]error{
		"ListOrgRepos": assert.AnError,
	}

	ctx := context.Background()
	_, err := m.ExpandGlobs(ctx, newTestClientFactory(fc))
	assert.Error(t, err)
	assert.ErrorContains(t, err, "expanding glob")
	assert.ErrorContains(t, err, "listing repos for org")
}

func TestExpandGlobs_NoGlobs(t *testing.T) {
	input := `
version: 1
forge:
  github:
    mint_url: https://mint.example.com

repos:
  - acme/repo-a
  - acme/repo-b
`
	var m Manifest
	require.NoError(t, yaml.Unmarshal([]byte(input), &m))

	fc := forge.NewFakeClient()
	ctx := context.Background()
	resolved, err := m.ExpandGlobs(ctx, newTestClientFactory(fc))
	require.NoError(t, err)

	require.Len(t, resolved, 2)
	assert.Equal(t, "repo-a", resolved[0].Repo)
	assert.Equal(t, "repo-b", resolved[1].Repo)
}

func TestResolveConfig_DefaultsOnly(t *testing.T) {
	var m Manifest
	require.NoError(t, yaml.Unmarshal([]byte(validManifest), &m))

	cfg, found := m.ResolveConfig("acme", "repo-one")
	assert.True(t, found)
	assert.Equal(t, "acme", cfg.Owner)
	assert.Equal(t, "repo-one", cfg.Repo)
	assert.Equal(t, "https://mint.example.com", cfg.MintURL)
	assert.Equal(t, "main", cfg.FullsendRef)
	assert.Equal(t, []string{"resource-a", "resource-b"}, cfg.AllowedRemoteResources)
}

func TestResolveConfig_ForgeFields(t *testing.T) {
	input := `
version: 1
forge:
  github:
    mint_url: https://mint.example.com
    fullsend_ref: main
defaults:
  forge: github
repos:
  - acme/special
  - acme/normal
`
	var m Manifest
	require.NoError(t, yaml.Unmarshal([]byte(input), &m))

	// All repos get the same forge-level config.
	cfg, found := m.ResolveConfig("acme", "special")
	assert.True(t, found)
	assert.Equal(t, "main", cfg.FullsendRef)

	cfg2, found2 := m.ResolveConfig("acme", "normal")
	assert.True(t, found2)
	assert.Equal(t, "main", cfg2.FullsendRef)
}

func TestResolveConfig_ForgeNullOverride(t *testing.T) {
	input := `
version: 1
forge:
  github:
    mint_url: https://mint.example.com
    fullsend_ref: main
defaults:
  forge: github
repos:
  - repo: acme/no-forge-override
    forge: null
`
	var m Manifest
	require.NoError(t, yaml.Unmarshal([]byte(input), &m))

	// Explicit null forge stops the fallback chain → empty string.
	cfg, found := m.ResolveConfig("acme", "no-forge-override")
	assert.True(t, found)
	assert.Equal(t, "", cfg.Forge) // null stops fallback
}

func TestResolveConfig_UnknownRepo(t *testing.T) {
	var m Manifest
	require.NoError(t, yaml.Unmarshal([]byte(validManifest), &m))

	// Repo not listed in manifest; should get forge-level settings but found=false.
	cfg, found := m.ResolveConfig("acme", "unknown")
	assert.False(t, found)
	assert.Equal(t, "acme", cfg.Owner)
	assert.Equal(t, "unknown", cfg.Repo)
}

func TestResolveConfig_MultiOrg(t *testing.T) {
	input := `
version: 1
forge:
  github:
    mint_url: https://mint.example.com
defaults:
  forge: github
repos:
  - org-a/repo
  - org-b/repo
`
	var m Manifest
	require.NoError(t, yaml.Unmarshal([]byte(input), &m))

	_, foundA := m.ResolveConfig("org-a", "repo")
	assert.True(t, foundA)

	_, foundB := m.ResolveConfig("org-b", "repo")
	assert.True(t, foundB)
}

func TestResolveConfigForEntry_GlobExpanded(t *testing.T) {
	input := `
version: 1
forge:
  github:
    mint_url: https://mint.example.com
    fullsend_ref: main
defaults:
  forge: github
repos:
  - acme/service-*
`
	var m Manifest
	require.NoError(t, yaml.Unmarshal([]byte(input), &m))

	fc := forge.NewFakeClient()
	fc.Repos = []forge.Repository{
		{Name: "service-api", FullName: "acme/service-api"},
		{Name: "service-web", FullName: "acme/service-web"},
	}

	ctx := context.Background()
	resolved, err := m.ExpandGlobs(ctx, newTestClientFactory(fc))
	require.NoError(t, err)
	require.Len(t, resolved, 2)

	for _, rr := range resolved {
		cfg := m.ResolveConfigForEntry(rr.Owner, rr.Repo, rr.Entry)
		assert.Equal(t, "main", cfg.FullsendRef, "forge-level config must apply for %s", rr.Repo)
		assert.Equal(t, "https://mint.example.com", cfg.MintURL)
	}
}

func TestLoadManifest_File(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repos.yaml")
	err := os.WriteFile(path, []byte(validManifest), 0644)
	require.NoError(t, err)

	m, err := LoadManifest(context.Background(), path)
	require.NoError(t, err)
	assert.Equal(t, 1, m.Version)
	assert.Equal(t, "https://mint.example.com", m.Forge.GitHub.MintURL)
	require.Len(t, m.Repos, 2)
}

func TestLoadManifest_FileNotFound(t *testing.T) {
	_, err := LoadManifest(context.Background(), "/nonexistent/path/repos.yaml")
	assert.Error(t, err)
	assert.ErrorContains(t, err, "reading manifest file")
}

func TestFetchManifestURL_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/yaml")
		_, _ = w.Write([]byte(validManifest))
	}))
	defer srv.Close()

	data, err := fetchManifestURL(context.Background(), srv.URL, true)
	require.NoError(t, err)

	var m Manifest
	require.NoError(t, yaml.Unmarshal(data, &m))
	assert.Equal(t, 1, m.Version)
	require.Len(t, m.Repos, 2)
}

func TestFetchManifestURL_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := fetchManifestURL(context.Background(), srv.URL, true)
	assert.Error(t, err)
	assert.ErrorContains(t, err, "HTTP 404")
}

func TestFetchManifestURL_SSRFBlocked(t *testing.T) {
	_, err := fetchManifestURL(context.Background(), "http://127.0.0.1:9999/steal", false)
	require.Error(t, err)
	assert.ErrorContains(t, err, "blocked")
}

func TestLoadManifest_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	err := os.WriteFile(path, []byte("version: [bad: {yaml"), 0644)
	require.NoError(t, err)

	_, err = LoadManifest(context.Background(), path)
	assert.Error(t, err)
	assert.ErrorContains(t, err, "parsing manifest YAML")
}

func TestLoadManifest_LegacyMintKey_Rejected(t *testing.T) {
	// The legacy top-level 'mint:' key was never released externally.
	// Unknown top-level fields are now rejected by KnownFields(true).
	manifest := `
version: 1
mint:
  url: https://mint.example.com
  project: my-project
  region: us-central1
defaults:
  forge: github
repos:
  - repo: acme/foo
    forge: github
`
	dir := t.TempDir()
	path := filepath.Join(dir, "repos.yaml")
	require.NoError(t, os.WriteFile(path, []byte(manifest), 0644))

	_, err := LoadManifest(context.Background(), path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mint")
}

func TestLoadManifest_RejectsUnknownDefaultsField(t *testing.T) {
	manifest := `
version: 1
forge:
  github:
    mint_url: https://mint.example.com
defaults:
  forge: github
  fullsend_ref: main
repos:
  - acme/repo
`
	dir := t.TempDir()
	path := filepath.Join(dir, "repos.yaml")
	require.NoError(t, os.WriteFile(path, []byte(manifest), 0644))

	_, err := LoadManifest(context.Background(), path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fullsend_ref")
}

func TestLoadManifest_RejectsUnknownForgeGitHubField(t *testing.T) {
	manifest := `
version: 1
forge:
  github:
    mint_url: https://mint.example.com
    bogus_field: val
defaults:
  forge: github
repos:
  - acme/repo
`
	dir := t.TempDir()
	path := filepath.Join(dir, "repos.yaml")
	require.NoError(t, os.WriteFile(path, []byte(manifest), 0644))

	_, err := LoadManifest(context.Background(), path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bogus_field")
}

func TestLoadManifest_RejectsUnknownForgeGitLabField(t *testing.T) {
	manifest := `
version: 1
forge:
  gitlab:
    url: https://gitlab.example.com
    bogus_field: val
defaults:
  forge: gitlab
repos:
  - acme/repo
`
	dir := t.TempDir()
	path := filepath.Join(dir, "repos.yaml")
	require.NoError(t, os.WriteFile(path, []byte(manifest), 0644))

	_, err := LoadManifest(context.Background(), path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bogus_field")
}

func TestLoadManifest_RejectsUnknownTopLevelField(t *testing.T) {
	manifest := `
version: 1
forge:
  github:
    mint_url: https://mint.example.com
defaults:
  forge: github
unknown_section: true
repos:
  - acme/repo
`
	dir := t.TempDir()
	path := filepath.Join(dir, "repos.yaml")
	require.NoError(t, os.WriteFile(path, []byte(manifest), 0644))

	_, err := LoadManifest(context.Background(), path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown_section")
}

func TestLoadManifest_RejectsUnknownForgeSectionField(t *testing.T) {
	manifest := `
version: 1
forge:
  github:
    mint_url: https://mint.example.com
  bitbucket:
    url: https://bitbucket.example.com
defaults:
  forge: github
repos:
  - acme/repo
`
	dir := t.TempDir()
	path := filepath.Join(dir, "repos.yaml")
	require.NoError(t, os.WriteFile(path, []byte(manifest), 0644))

	_, err := LoadManifest(context.Background(), path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bitbucket")
}

func TestParseManifestBytes_EmptyAndCommentOnlyInput(t *testing.T) {
	// yaml.Decoder.Decode returns io.EOF for empty or comment-only input.
	// parseManifestBytes must treat this as a no-op (matching the old
	// yaml.Unmarshal behavior) so callers like SetDefault can handle
	// empty manifest files as zero-value manifests.
	tests := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"whitespace only", "   \n\n  "},
		{"comment only", "# this is a comment\n# another comment\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var m Manifest
			err := parseManifestBytes([]byte(tc.input), &m)
			require.NoError(t, err, "parseManifestBytes should treat %q as a no-op", tc.name)
			assert.Equal(t, Manifest{}, m, "manifest should remain zero-value")
		})
	}
}

func TestLoadManifest_HTTPRejected(t *testing.T) {
	_, err := LoadManifest(context.Background(), "http://example.com/repos.yaml")
	assert.Error(t, err)
	assert.ErrorContains(t, err, "insecure http:// not supported")
}

func TestLoadManifest_FTPSchemeNotSupported(t *testing.T) {
	_, err := LoadManifest(context.Background(), "ftp://example.com/repos.yaml")
	assert.Error(t, err)
	assert.ErrorContains(t, err, "reading manifest file")
}

func TestLoadManifest_OversizedResponse(t *testing.T) {
	// Create a server that returns a response larger than maxManifestBytes.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/yaml")
		_, _ = w.Write([]byte(strings.Repeat("x", maxManifestBytes+100)))
	}))
	defer srv.Close()

	ctx := context.Background()
	_, err := fetchManifestURL(ctx, srv.URL, true)
	require.Error(t, err)
	assert.ErrorContains(t, err, "exceeds maximum size")
}

func TestLoadManifest_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(5 * time.Second)
		_, _ = w.Write([]byte(validManifest))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := fetchManifestURL(ctx, srv.URL, true)
	require.Error(t, err)
}

func TestMarshalRoundTrip(t *testing.T) {
	var original Manifest
	require.NoError(t, yaml.Unmarshal([]byte(validManifest), &original))

	data, err := original.Marshal()
	require.NoError(t, err)

	var roundTripped Manifest
	require.NoError(t, yaml.Unmarshal(data, &roundTripped))

	assert.Equal(t, original.Version, roundTripped.Version)
	assert.Equal(t, original.Forge, roundTripped.Forge)
	assert.Equal(t, original.Defaults, roundTripped.Defaults)
	require.Len(t, roundTripped.Repos, len(original.Repos))
	for i := range original.Repos {
		assert.Equal(t, original.Repos[i].Repo, roundTripped.Repos[i].Repo)
	}
}

func TestMarshalRoundTrip_WithForgeOverride(t *testing.T) {
	input := `
version: 1
forge:
  github:
    mint_url: https://mint.example.com

repos:
  - repo: acme/with-override
    forge: gitlab
  - acme/simple
`
	var original Manifest
	require.NoError(t, yaml.Unmarshal([]byte(input), &original))

	data, err := original.Marshal()
	require.NoError(t, err)

	var roundTripped Manifest
	require.NoError(t, yaml.Unmarshal(data, &roundTripped))

	require.Len(t, roundTripped.Repos, 2)
	assert.Equal(t, "acme/with-override", roundTripped.Repos[0].Repo)
	assert.Equal(t, "gitlab", roundTripped.Repos[0].Forge.Value)
	assert.Equal(t, "acme/simple", roundTripped.Repos[1].Repo)
}

func TestResolveField(t *testing.T) {
	tests := []struct {
		name     string
		override NullableString
		fallback string
		builtin  string
		want     string
	}{
		{
			name:     "override set",
			override: NullableString{Set: true, Value: "override"},
			fallback: "fallback",
			builtin:  "builtin",
			want:     "override",
		},
		{
			name:     "override null stops chain",
			override: NullableString{Set: true, Null: true},
			fallback: "fallback",
			builtin:  "builtin",
			want:     "",
		},
		{
			name:     "override not set falls to fallback",
			override: NullableString{},
			fallback: "fallback",
			builtin:  "builtin",
			want:     "fallback",
		},
		{
			name:     "no fallback falls to builtin",
			override: NullableString{},
			fallback: "",
			builtin:  "builtin",
			want:     "builtin",
		},
		{
			name:     "all empty",
			override: NullableString{},
			fallback: "",
			builtin:  "",
			want:     "",
		},
		{
			name:     "override set to empty string falls to fallback",
			override: NullableString{Set: true, Value: ""},
			fallback: "fallback",
			builtin:  "builtin",
			want:     "fallback",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveField(tt.override, tt.fallback, tt.builtin)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFetchManifestURL_RedirectToHTTPRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://evil.example.com/steal", http.StatusFound)
	}))
	defer srv.Close()

	_, err := fetchManifestURL(context.Background(), srv.URL, true)
	require.Error(t, err)
	assert.ErrorContains(t, err, "redirect to non-HTTPS URL")
}

func TestLoadManifest_OversizedLocalFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "huge.yaml")
	err := os.WriteFile(path, []byte(strings.Repeat("x", maxManifestBytes+100)), 0644)
	require.NoError(t, err)

	_, err = LoadManifest(context.Background(), path)
	require.Error(t, err)
	assert.ErrorContains(t, err, "exceeds maximum size")
}

func TestExpandGlobs_MultiOrg(t *testing.T) {
	input := `
version: 1
forge:
  github:
    mint_url: https://mint.example.com

repos:
  - org-a/*
  - org-b/service-*
`
	var m Manifest
	require.NoError(t, yaml.Unmarshal([]byte(input), &m))

	fc := forge.NewFakeClient()
	fc.OrgRepos = map[string][]forge.Repository{
		"org-a": {
			{Name: "app", FullName: "org-a/app"},
			{Name: "lib", FullName: "org-a/lib"},
		},
		"org-b": {
			{Name: "service-api", FullName: "org-b/service-api"},
			{Name: "other", FullName: "org-b/other"},
		},
	}

	ctx := context.Background()
	resolved, err := m.ExpandGlobs(ctx, newTestClientFactory(fc))
	require.NoError(t, err)

	// org-a/* matches app, lib (from org-a).
	// org-b/service-* matches only service-api (from org-b).
	require.Len(t, resolved, 3)

	repoNames := make(map[string]bool)
	for _, rr := range resolved {
		repoNames[rr.Owner+"/"+rr.Repo] = true
	}
	assert.True(t, repoNames["org-a/app"])
	assert.True(t, repoNames["org-a/lib"])
	assert.True(t, repoNames["org-b/service-api"])
	assert.False(t, repoNames["org-b/other"], "other should not match service-*")
}

func TestValidate_RejectsSameOwnerMixedForge(t *testing.T) {
	input := `
version: 1
forge:
  github:
    mint_url: https://mint.example.com

defaults:
  forge: github
repos:
  - acme/api
  - repo: acme/ml-pipeline
    forge: gitlab
`
	var m Manifest
	require.NoError(t, yaml.Unmarshal([]byte(input), &m))

	err := m.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "all repos under the same owner must use the same forge")
	assert.Contains(t, err.Error(), `owner "acme"`)
}

func TestValidate_AllowsDifferentOwnersDifferentForges(t *testing.T) {
	input := `
version: 1
forge:
  github:
    mint_url: https://mint.example.com

  gitlab:
    url: https://gitlab.example.com
defaults:
  forge: github
repos:
  - acme/api
  - repo: gitlab-group/ml-pipeline
    forge: gitlab
`
	var m Manifest
	require.NoError(t, yaml.Unmarshal([]byte(input), &m))

	err := m.Validate()
	require.NoError(t, err)
}

func TestDistinctForges(t *testing.T) {
	input := `
version: 1
forge:
  github:
    mint_url: https://mint.example.com

defaults:
  forge: github
repos:
  - acme/api
  - acme/web
  - repo: gitlab-group/ml
    forge: gitlab
`
	var m Manifest
	require.NoError(t, yaml.Unmarshal([]byte(input), &m))

	forges := m.DistinctForges()
	assert.Equal(t, []string{"github", "gitlab"}, forges)
}

func TestDistinctForges_SingleForge(t *testing.T) {
	var m Manifest
	require.NoError(t, yaml.Unmarshal([]byte(validManifest), &m))

	forges := m.DistinctForges()
	assert.Equal(t, []string{"github"}, forges)
}

func TestValidate_GitHubURL_DefaultsToGitHubCom(t *testing.T) {
	m := Manifest{
		Version: 1,
		Forge: ForgeSection{GitHub: GitHubForgeInfra{
			MintURL: "https://mint.example.com",
		}},
		Defaults: DefaultsConfig{Forge: "github"},
		Repos:    []RepoEntry{{Repo: "acme/repo"}},
	}
	require.NoError(t, m.Validate())
	assert.Empty(t, m.Forge.GitHub.URL, "Validate must not mutate the receiver")
}

func TestValidate_GitHubURL_ExplicitValue(t *testing.T) {
	m := Manifest{
		Version: 1,
		Forge: ForgeSection{GitHub: GitHubForgeInfra{
			URL:     "https://ghes.example.com",
			MintURL: "https://mint.example.com",
		}},
		Defaults: DefaultsConfig{Forge: "github"},
		Repos:    []RepoEntry{{Repo: "acme/repo"}},
	}
	require.NoError(t, m.Validate())
	assert.Equal(t, "https://ghes.example.com", m.Forge.GitHub.URL)
}

func TestValidate_GitHubURL_InvalidURL(t *testing.T) {
	m := Manifest{
		Version: 1,
		Forge: ForgeSection{GitHub: GitHubForgeInfra{
			URL:     "http://insecure.example.com",
			MintURL: "https://mint.example.com",
		}},
		Defaults: DefaultsConfig{Forge: "github"},
		Repos:    []RepoEntry{{Repo: "acme/repo"}},
	}
	err := m.Validate()
	assert.ErrorContains(t, err, "forge.github.url must be a valid HTTPS URL")
}

func TestValidate_GitLabURL_Required(t *testing.T) {
	m := Manifest{
		Version:  1,
		Defaults: DefaultsConfig{Forge: "gitlab"},
		Repos:    []RepoEntry{{Repo: "acme/repo"}},
	}
	err := m.Validate()
	assert.ErrorContains(t, err, "forge.gitlab.url is required")
}

func TestValidate_GitLabURL_Valid(t *testing.T) {
	m := Manifest{
		Version:  1,
		Forge:    ForgeSection{GitLab: GitLabForgeInfra{URL: "https://gitlab.cee.redhat.com"}},
		Defaults: DefaultsConfig{Forge: "gitlab"},
		Repos:    []RepoEntry{{Repo: "acme/repo"}},
	}
	require.NoError(t, m.Validate())
}

func TestValidate_GitLabURL_InvalidURL(t *testing.T) {
	m := Manifest{
		Version:  1,
		Forge:    ForgeSection{GitLab: GitLabForgeInfra{URL: "http://insecure.example.com"}},
		Defaults: DefaultsConfig{Forge: "gitlab"},
		Repos:    []RepoEntry{{Repo: "acme/repo"}},
	}
	err := m.Validate()
	assert.ErrorContains(t, err, "forge.gitlab.url must be a valid HTTPS URL")
}

func TestValidate_GitLabURL_NotRequiredWhenNotReferenced(t *testing.T) {
	// GitLab URL is only required when GitLab repos are present.
	m := Manifest{
		Version: 1,
		Forge: ForgeSection{GitHub: GitHubForgeInfra{
			MintURL: "https://mint.example.com",
		}},
		Defaults: DefaultsConfig{Forge: "github"},
		Repos:    []RepoEntry{{Repo: "acme/repo"}},
	}
	require.NoError(t, m.Validate())
}

func TestParseManifest_URLFields(t *testing.T) {
	input := `
version: 1
forge:
  github:
    url: https://ghes.example.com
    mint_url: https://mint.example.com

  gitlab:
    url: https://gitlab.cee.redhat.com
defaults:
  forge: github
repos:
  - acme/repo
`
	var m Manifest
	err := yaml.Unmarshal([]byte(input), &m)
	require.NoError(t, err)
	assert.Equal(t, "https://ghes.example.com", m.Forge.GitHub.URL)
	assert.Equal(t, "https://gitlab.cee.redhat.com", m.Forge.GitLab.URL)
}

func TestMarshalRoundTrip_URLFields(t *testing.T) {
	m := Manifest{
		Version: 1,
		Forge: ForgeSection{
			GitHub: GitHubForgeInfra{
				URL:     "https://ghes.example.com",
				MintURL: "https://mint.example.com",
			},
			GitLab: GitLabForgeInfra{URL: "https://gitlab.example.com"},
		},
		Defaults: DefaultsConfig{Forge: "github"},
		Repos:    []RepoEntry{{Repo: "acme/repo"}},
	}
	data, err := m.Marshal()
	require.NoError(t, err)

	var roundTripped Manifest
	require.NoError(t, yaml.Unmarshal(data, &roundTripped))
	assert.Equal(t, "https://ghes.example.com", roundTripped.Forge.GitHub.URL)
	assert.Equal(t, "https://gitlab.example.com", roundTripped.Forge.GitLab.URL)
}

func TestValidate_GitHubURL_RejectsPathComponent(t *testing.T) {
	m := Manifest{
		Version: 1,
		Forge: ForgeSection{GitHub: GitHubForgeInfra{
			URL:     "https://ghes.example.com/prefix",
			MintURL: "https://mint.example.com",
		}},
		Defaults: DefaultsConfig{Forge: "github"},
		Repos:    []RepoEntry{{Repo: "acme/repo"}},
	}
	err := m.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must not contain a path component")
}

func TestValidate_GitLabURL_RejectsPathComponent(t *testing.T) {
	m := Manifest{
		Version:  1,
		Forge:    ForgeSection{GitLab: GitLabForgeInfra{URL: "https://gitlab.example.com/api/v4"}},
		Defaults: DefaultsConfig{Forge: "gitlab"},
		Repos:    []RepoEntry{{Repo: "acme/repo"}},
	}
	err := m.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must not contain a path component")
}

func TestValidate_GitHubURL_TrailingSlashAccepted(t *testing.T) {
	m := Manifest{
		Version: 1,
		Forge: ForgeSection{GitHub: GitHubForgeInfra{
			URL:     "https://ghes.example.com/",
			MintURL: "https://mint.example.com",
		}},
		Defaults: DefaultsConfig{Forge: "github"},
		Repos:    []RepoEntry{{Repo: "acme/repo"}},
	}
	require.NoError(t, m.Validate())
}

func TestValidate_ForgeURL_RejectsUserinfo(t *testing.T) {
	m := Manifest{
		Version:  1,
		Forge:    ForgeSection{GitHub: GitHubForgeInfra{URL: "https://user@ghes.example.com", MintURL: "https://mint.example.com"}},
		Defaults: DefaultsConfig{Forge: "github"},
		Repos:    []RepoEntry{{Repo: "acme/repo"}},
	}
	err := m.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "userinfo")
}

func TestValidate_ForgeURL_RejectsQueryParams(t *testing.T) {
	m := Manifest{
		Version:  1,
		Forge:    ForgeSection{GitLab: GitLabForgeInfra{URL: "https://gitlab.example.com?token=abc"}},
		Defaults: DefaultsConfig{Forge: "gitlab"},
		Repos:    []RepoEntry{{Repo: "acme/repo"}},
	}
	err := m.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "query parameters")
}

func TestValidate_ForgeURL_RejectsFragment(t *testing.T) {
	m := Manifest{
		Version:  1,
		Forge:    ForgeSection{GitLab: GitLabForgeInfra{URL: "https://gitlab.example.com#section"}},
		Defaults: DefaultsConfig{Forge: "gitlab"},
		Repos:    []RepoEntry{{Repo: "acme/repo"}},
	}
	err := m.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fragment")
}

func TestForgeSectionFromURL_GitHub(t *testing.T) {
	s := ForgeSectionFromURL(ForgeGitHub, "https://ghes.example.com")
	assert.Equal(t, "https://ghes.example.com", s.GitHub.URL)
	assert.Empty(t, s.GitLab.URL)
}

func TestForgeSectionFromURL_GitLab(t *testing.T) {
	s := ForgeSectionFromURL(ForgeGitLab, "https://gitlab.example.com")
	assert.Equal(t, "https://gitlab.example.com", s.GitLab.URL)
	assert.Empty(t, s.GitHub.URL)
}

func TestForgeSectionFromURL_Empty(t *testing.T) {
	s := ForgeSectionFromURL(ForgeGitHub, "")
	assert.Empty(t, s.GitHub.URL)
	assert.Empty(t, s.GitLab.URL)
}

func TestResolveConfig_PerRepoOverrides(t *testing.T) {
	input := `
version: 1
forge:
  github:
    mint_url: https://mint.example.com
    fullsend_ref: v1.0.0
defaults:
  forge: github
  allowed_remote_resources:
    - https://default.example.com/
repos:
  - acme/inherits
  - repo: acme/overrides
    fullsend_ref: v2.0.0
    mint_url: https://eu-mint.example.com
    allowed_remote_resources:
      - https://special.example.com/
`
	var m Manifest
	require.NoError(t, yaml.Unmarshal([]byte(input), &m))

	t.Run("inherits_forge_defaults", func(t *testing.T) {
		cfg, found := m.ResolveConfig("acme", "inherits")
		require.True(t, found)
		assert.Equal(t, "https://mint.example.com", cfg.MintURL)
		assert.Equal(t, "v1.0.0", cfg.FullsendRef)
		assert.Equal(t, []string{"https://default.example.com/"}, cfg.AllowedRemoteResources)
	})

	t.Run("per_repo_override_takes_precedence", func(t *testing.T) {
		cfg, found := m.ResolveConfig("acme", "overrides")
		require.True(t, found)
		assert.Equal(t, "https://eu-mint.example.com", cfg.MintURL)
		assert.Equal(t, "v2.0.0", cfg.FullsendRef)
		assert.Equal(t, []string{"https://special.example.com/"}, cfg.AllowedRemoteResources)
	})
}

func TestRepoEntryUnmarshalYAML_PerRepoOverrideFields(t *testing.T) {
	input := `
repo: acme/my-repo
fullsend_ref: v2.0.0
mint_url: https://eu-mint.example.com
allowed_remote_resources:
  - https://special.example.com/
`
	var entry RepoEntry
	err := yaml.Unmarshal([]byte(input), &entry)
	require.NoError(t, err)
	assert.Equal(t, "acme/my-repo", entry.Repo)
	assert.True(t, entry.FullsendRef.Set)
	assert.Equal(t, "v2.0.0", entry.FullsendRef.Value)
	assert.True(t, entry.MintURL.Set)
	assert.Equal(t, "https://eu-mint.example.com", entry.MintURL.Value)
	assert.Equal(t, []string{"https://special.example.com/"}, entry.AllowedRemoteResources)
}

func TestRepoEntryUnmarshalYAML_NullAllowedRemoteResources(t *testing.T) {
	input := `
repo: acme/my-repo
allowed_remote_resources: null
`
	var entry RepoEntry
	err := yaml.Unmarshal([]byte(input), &entry)
	require.NoError(t, err)
	assert.Equal(t, "acme/my-repo", entry.Repo)
	// Explicit null sets an empty (non-nil) slice to stop inheritance.
	assert.NotNil(t, entry.AllowedRemoteResources)
	assert.Empty(t, entry.AllowedRemoteResources)
}

func TestMarshalRoundTrip_PerRepoOverrides(t *testing.T) {
	m := Manifest{
		Version: 1,
		Forge: ForgeSection{GitHub: GitHubForgeInfra{
			MintURL:     "https://mint.example.com",
			FullsendRef: "v1.0.0",
		}},
		Defaults: DefaultsConfig{
			Forge:                  "github",
			AllowedRemoteResources: []string{"https://default.example.com/"},
		},
		Repos: []RepoEntry{
			{Repo: "acme/inherits"},
			{
				Repo:                   "acme/overrides",
				FullsendRef:            NullableString{Set: true, Value: "v2.0.0"},
				MintURL:                NullableString{Set: true, Value: "https://eu-mint.example.com"},
				AllowedRemoteResources: []string{"https://special.example.com/"},
			},
		},
	}
	data, err := m.Marshal()
	require.NoError(t, err)

	var roundTripped Manifest
	require.NoError(t, yaml.Unmarshal(data, &roundTripped))

	require.Len(t, roundTripped.Repos, 2)
	assert.Equal(t, "acme/inherits", roundTripped.Repos[0].Repo)

	assert.Equal(t, "acme/overrides", roundTripped.Repos[1].Repo)
	assert.True(t, roundTripped.Repos[1].FullsendRef.Set)
	assert.Equal(t, "v2.0.0", roundTripped.Repos[1].FullsendRef.Value)
	assert.True(t, roundTripped.Repos[1].MintURL.Set)
	assert.Equal(t, "https://eu-mint.example.com", roundTripped.Repos[1].MintURL.Value)
	assert.Equal(t, []string{"https://special.example.com/"}, roundTripped.Repos[1].AllowedRemoteResources)
}

func TestMarshalRoundTrip_AllowedRemoteResourcesNull(t *testing.T) {
	input := `
version: 1
forge:
  github:
    mint_url: https://mint.example.com
defaults:
  forge: github
  allowed_remote_resources:
    - https://default.example.com/
repos:
  - repo: acme/deny-all
    allowed_remote_resources: null
  - acme/inherits
`
	var m Manifest
	require.NoError(t, yaml.Unmarshal([]byte(input), &m))

	require.Len(t, m.Repos, 2)
	assert.NotNil(t, m.Repos[0].AllowedRemoteResources)
	assert.Empty(t, m.Repos[0].AllowedRemoteResources)
	assert.Nil(t, m.Repos[1].AllowedRemoteResources)

	data, err := m.Marshal()
	require.NoError(t, err)

	var roundTripped Manifest
	require.NoError(t, yaml.Unmarshal(data, &roundTripped))

	require.Len(t, roundTripped.Repos, 2)
	assert.NotNil(t, roundTripped.Repos[0].AllowedRemoteResources,
		"explicit null should round-trip as non-nil empty slice")
	assert.Empty(t, roundTripped.Repos[0].AllowedRemoteResources)
	assert.Nil(t, roundTripped.Repos[1].AllowedRemoteResources,
		"omitted field should round-trip as nil")
}

func TestValidate_PerRepoMintURLMustBeHTTPS(t *testing.T) {
	m := Manifest{
		Version:  1,
		Forge:    ForgeSection{GitHub: GitHubForgeInfra{MintURL: "https://mint.example.com"}},
		Defaults: DefaultsConfig{Forge: "github"},
		Repos: []RepoEntry{
			{
				Repo:    "acme/api",
				MintURL: NullableString{Set: true, Value: "http://insecure.example.com"},
			},
		},
	}
	err := m.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "per-repo mint_url must be a valid HTTPS URL")
}

func TestValidate_PerRepoFullsendRefInvalid(t *testing.T) {
	m := Manifest{
		Version:  1,
		Forge:    ForgeSection{GitHub: GitHubForgeInfra{MintURL: "https://mint.example.com"}},
		Defaults: DefaultsConfig{Forge: "github"},
		Repos: []RepoEntry{
			{
				Repo:        "acme/api",
				FullsendRef: NullableString{Set: true, Value: "v1.0.0; rm -rf /"},
			},
		},
	}
	err := m.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "per-repo fullsend_ref")
	assert.Contains(t, err.Error(), "invalid characters")
}

func TestValidate_PerRepoFullsendRefValid(t *testing.T) {
	m := Manifest{
		Version:  1,
		Forge:    ForgeSection{GitHub: GitHubForgeInfra{MintURL: "https://mint.example.com"}},
		Defaults: DefaultsConfig{Forge: "github"},
		Repos: []RepoEntry{
			{
				Repo:        "acme/api",
				FullsendRef: NullableString{Set: true, Value: "v2.1.0-beta.1"},
			},
		},
	}
	err := m.Validate()
	require.NoError(t, err)
}

func TestParseGitLabFullsendRef(t *testing.T) {
	input := `
version: 1
forge:
  gitlab:
    url: https://gitlab.example.com
    fullsend_ref: v0.34.0
defaults:
  forge: gitlab
repos:
  - acme/frontend
`
	var m Manifest
	err := yaml.Unmarshal([]byte(input), &m)
	require.NoError(t, err)
	assert.Equal(t, "v0.34.0", m.Forge.GitLab.FullsendRef)
}

func TestValidate_GitLabFullsendRefInvalid(t *testing.T) {
	m := Manifest{
		Version: 1,
		Forge: ForgeSection{GitLab: GitLabForgeInfra{
			URL:         "https://gitlab.example.com",
			FullsendRef: "v1.0.0; rm -rf /",
		}},
		Defaults: DefaultsConfig{Forge: "gitlab"},
		Repos:    []RepoEntry{{Repo: "acme/api"}},
	}
	err := m.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "forge.gitlab.fullsend_ref")
	assert.Contains(t, err.Error(), "invalid characters")
}

func TestValidate_GitLabFullsendRefValid(t *testing.T) {
	m := Manifest{
		Version: 1,
		Forge: ForgeSection{GitLab: GitLabForgeInfra{
			URL:         "https://gitlab.example.com",
			FullsendRef: "v0.34.0",
		}},
		Defaults: DefaultsConfig{Forge: "gitlab"},
		Repos:    []RepoEntry{{Repo: "acme/api"}},
	}
	err := m.Validate()
	require.NoError(t, err)
}

func TestValidate_CredentialMode_ValidForGitHub(t *testing.T) {
	for _, mode := range []string{"wif", "oidc"} {
		m := Manifest{
			Version: 1,
			Forge: ForgeSection{GitHub: GitHubForgeInfra{
				MintURL:        "https://mint.example.com",
				CredentialMode: mode,
			}},
			Defaults: DefaultsConfig{Forge: "github"},
			Repos:    []RepoEntry{{Repo: "acme/api"}},
		}
		err := m.Validate()
		require.NoError(t, err, "mode %q should be valid for GitHub", mode)
	}
}

func TestValidate_CredentialMode_InvalidForGitHub(t *testing.T) {
	m := Manifest{
		Version: 1,
		Forge: ForgeSection{GitHub: GitHubForgeInfra{
			MintURL:        "https://mint.example.com",
			CredentialMode: "token",
		}},
		Defaults: DefaultsConfig{Forge: "github"},
		Repos:    []RepoEntry{{Repo: "acme/api"}},
	}
	err := m.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "credential_mode")
}

func TestValidate_CredentialMode_ValidForGitLab(t *testing.T) {
	for _, mode := range []string{"wif", "token"} {
		m := Manifest{
			Version: 1,
			Forge: ForgeSection{GitLab: GitLabForgeInfra{
				URL:            "https://gitlab.example.com",
				CredentialMode: mode,
			}},
			Defaults: DefaultsConfig{Forge: "gitlab"},
			Repos:    []RepoEntry{{Repo: "acme/api"}},
		}
		err := m.Validate()
		require.NoError(t, err, "mode %q should be valid for GitLab", mode)
	}
}

func TestValidate_CredentialMode_InvalidForGitLab(t *testing.T) {
	m := Manifest{
		Version: 1,
		Forge: ForgeSection{GitLab: GitLabForgeInfra{
			URL:            "https://gitlab.example.com",
			CredentialMode: "oidc",
		}},
		Defaults: DefaultsConfig{Forge: "gitlab"},
		Repos:    []RepoEntry{{Repo: "acme/api"}},
	}
	err := m.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "credential_mode")
}

func TestValidate_PerRepoCredentialMode_Invalid(t *testing.T) {
	m := Manifest{
		Version: 1,
		Forge: ForgeSection{GitHub: GitHubForgeInfra{
			MintURL: "https://mint.example.com",
		}},
		Defaults: DefaultsConfig{Forge: "github"},
		Repos: []RepoEntry{{
			Repo:           "acme/api",
			CredentialMode: NullableString{Set: true, Value: "token"},
		}},
	}
	err := m.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "credential_mode")
	assert.Contains(t, err.Error(), "not valid for forge")
}

func TestResolveConfig_CredentialMode(t *testing.T) {
	m := Manifest{
		Version: 1,
		Forge: ForgeSection{GitHub: GitHubForgeInfra{
			MintURL:        "https://mint.example.com",
			CredentialMode: "oidc",
		}},
		Defaults: DefaultsConfig{Forge: "github"},
		Repos: []RepoEntry{
			{Repo: "acme/api"},
			{Repo: "acme/special", CredentialMode: NullableString{Set: true, Value: "wif"}},
		},
	}
	err := m.Validate()
	require.NoError(t, err)

	cfg1, _ := m.ResolveConfig("acme", "api")
	assert.Equal(t, "oidc", cfg1.CredentialMode)

	cfg2, _ := m.ResolveConfig("acme", "special")
	assert.Equal(t, "wif", cfg2.CredentialMode)
}

func TestIsValidCredentialMode(t *testing.T) {
	assert.True(t, IsValidCredentialMode("github", "wif"))
	assert.True(t, IsValidCredentialMode("github", "oidc"))
	assert.False(t, IsValidCredentialMode("github", "token"))

	assert.True(t, IsValidCredentialMode("gitlab", "wif"))
	assert.True(t, IsValidCredentialMode("gitlab", "token"))
	assert.False(t, IsValidCredentialMode("gitlab", "oidc"))

	assert.False(t, IsValidCredentialMode("bitbucket", "wif"))
}

func TestResolveConfig_GitLabFullsendRef(t *testing.T) {
	input := `
version: 1
forge:
  gitlab:
    url: https://gitlab.example.com
    fullsend_ref: v0.34.0
defaults:
  forge: gitlab
repos:
  - acme/frontend
  - repo: acme/pinned
    fullsend_ref: v0.33.0
`
	var m Manifest
	require.NoError(t, yaml.Unmarshal([]byte(input), &m))

	t.Run("inherits forge-level ref", func(t *testing.T) {
		cfg, found := m.ResolveConfig("acme", "frontend")
		require.True(t, found)
		assert.Equal(t, "gitlab", cfg.Forge)
		assert.Equal(t, "v0.34.0", cfg.FullsendRef)
	})

	t.Run("per-repo override", func(t *testing.T) {
		cfg, found := m.ResolveConfig("acme", "pinned")
		require.True(t, found)
		assert.Equal(t, "gitlab", cfg.Forge)
		assert.Equal(t, "v0.33.0", cfg.FullsendRef)
	})
}

func TestResolveConfig_GitLabNoFullsendRef(t *testing.T) {
	input := `
version: 1
forge:
  gitlab:
    url: https://gitlab.example.com
defaults:
  forge: gitlab
repos:
  - acme/frontend
`
	var m Manifest
	require.NoError(t, yaml.Unmarshal([]byte(input), &m))

	cfg, found := m.ResolveConfig("acme", "frontend")
	require.True(t, found)
	assert.Equal(t, "gitlab", cfg.Forge)
	assert.Equal(t, "", cfg.FullsendRef)
}

func TestResolveConfig_GitLabFullsendRefNullOverride(t *testing.T) {
	input := `
version: 1
forge:
  gitlab:
    url: https://gitlab.example.com
    fullsend_ref: v0.34.0
defaults:
  forge: gitlab
repos:
  - repo: acme/unpinned
    fullsend_ref: null
`
	var m Manifest
	require.NoError(t, yaml.Unmarshal([]byte(input), &m))

	cfg, found := m.ResolveConfig("acme", "unpinned")
	require.True(t, found)
	assert.Equal(t, "", cfg.FullsendRef, "null should stop fallback chain")
}

func TestIsNumeric(t *testing.T) {
	assert.True(t, IsNumeric("123456789"))
	assert.True(t, IsNumeric("0"))
	assert.False(t, IsNumeric(""))
	assert.False(t, IsNumeric("abc"))
	assert.False(t, IsNumeric("123abc"))
	assert.False(t, IsNumeric("12.34"))
}

func TestIsValidGCPRegion(t *testing.T) {
	assert.True(t, IsValidGCPRegion("us-central1"))
	assert.True(t, IsValidGCPRegion("europe-west4"))
	assert.True(t, IsValidGCPRegion("asia-southeast1"))
	assert.False(t, IsValidGCPRegion(""))
	assert.False(t, IsValidGCPRegion("ab"))
	assert.False(t, IsValidGCPRegion("1us-central"))
	assert.False(t, IsValidGCPRegion("US-CENTRAL1"))
	assert.False(t, IsValidGCPRegion("us central1"))
	assert.False(t, IsValidGCPRegion("us-central-"))
}

func TestIsValidGCPProjectID(t *testing.T) {
	assert.True(t, IsValidGCPProjectID("my-project-123"))
	assert.True(t, IsValidGCPProjectID("abcdef"))
	assert.True(t, IsValidGCPProjectID("a-long-project-name-with-30ch"))
	assert.False(t, IsValidGCPProjectID("short"))
	assert.False(t, IsValidGCPProjectID(""))
	assert.False(t, IsValidGCPProjectID("1starts-with-digit"))
	assert.False(t, IsValidGCPProjectID("HAS-UPPERCASE"))
	assert.False(t, IsValidGCPProjectID("has_underscore"))
	assert.False(t, IsValidGCPProjectID("has spaces"))
	assert.False(t, IsValidGCPProjectID("a-project-id-that-is-way-too-long-for-gcp"))
	assert.False(t, IsValidGCPProjectID("my-project-"))
}
