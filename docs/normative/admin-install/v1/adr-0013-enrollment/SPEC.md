# Enrollment v1 (admin install) — normative specification

**Version:** v1
**ADR:** [0013](../../../../ADRs/0013-admin-install-repo-enrollment-v1.md)
**Scope:** How the admin install enrolls a GitHub-hosted repository into the fullsend agent pipeline by opening a pull request that adds a *shim* workflow. The shim triggers the `workflow_dispatch` entrypoint for `agent.yaml` in the org’s `.fullsend` config repository using the org Actions secret `FULLSEND_DISPATCH_TOKEN` (see [ADR 0008](../../../../ADRs/0008-workflow-dispatch-for-cross-repo-dispatch.md), [ADR 0009](../../../../ADRs/0009-pull-request-target-in-shim-workflows.md)). This spec is the contract for tooling (for example the enrollment layer and `forge.Client`); hosting details follow GitHub’s REST API unless stated otherwise.

## 1. Definitions

- **Owner** — GitHub organization or user that owns the repository (the install target). In admin-install configuration this is the organization login passed to tooling as the owner namespace.
- **Target repository** — A repository listed as enabled for the pipeline in org configuration (see ADR 0011).
- **Shim workflow** — The workflow file at **shim path** whose job is to forward repository events to the agent dispatch workflow in the org’s `.fullsend` repository by running `gh workflow run` against `agent.yaml` with authentication from **dispatch secret** (see §3).
- **Enrollment branch** — The head branch for the enrollment change proposal.
- **Base branch** — The branch the enrollment pull request targets.

## 2. Dispatch target (`.fullsend` / `agent.yaml`)

At run time the shim MUST resolve the config repository as `${{ github.repository_owner }}/.fullsend` and MUST invoke the workflow file **`agent.yaml`** in that repository (GitHub CLI `gh workflow run agent.yaml --repo …` semantics).

**Rules:**

1. The committed shim MUST use GitHub Actions context `github.repository_owner` so the same file content is valid for any target repository under that owner namespace. Installers MUST NOT perform owner-string substitution or other templating on the shim body for v1.
2. The dispatch inputs MUST include at least:
   - `event_type` — `${{ github.event_name }}`
   - `source_repo` — `${{ github.repository }}`
   - `event_payload` — JSON for `${{ toJSON(github.event) }}` (same expression shape as the reference implementation).

## 3. Constants (v1)

| Item | Value |
|------|--------|
| Shim path | `.github/workflows/fullsend.yaml` |
| Enrollment branch name | `fullsend/onboard` |
| Dispatch workflow file (in `.fullsend`) | `agent.yaml` |
| Dispatch secret (org Actions secret name) | `FULLSEND_DISPATCH_TOKEN` |
| Default base branch if unspecified | `main` |
| Commit message for adding the shim | `chore: add fullsend shim workflow` |

## 4. Enrollment detection

A target repository is **enrolled** for v1 if and only if the file at **shim path** is readable on the repository’s **default branch** (forge `GetFileContent(owner, repo, shim path)` semantics: no other ref).

- If the file exists (any content): the repository MUST be treated as already enrolled; installers MUST NOT create another enrollment change proposal for v1.
- If the forge reports “not found” (404 / `ErrNotFound`): the repository MUST be treated as not enrolled.
- Any other error while checking MUST fail that repository’s enrollment attempt and MUST be surfaced to the operator; it MUST NOT be interpreted as enrolled.

## 5. Shim workflow content (normative)

The file at **shim path** MUST be valid GitHub Actions workflow YAML and MUST match the following structure and keys. Comments are non-normative except where they restate these rules.

```yaml
# fullsend shim workflow
# Routes events to the agent dispatch workflow in .fullsend.
#
# Security: pull_request_target runs the BASE branch version of this workflow,
# preventing PRs from modifying it to exfiltrate the dispatch token.
# This shim never checks out PR code, so it is not vulnerable to "pwn request"
# attacks (see: Trivy CVE-2026-33634, hackerbot-claw campaign).
name: fullsend

on:
  issues:
    types: [opened, edited, labeled]
  issue_comment:
    types: [created]
  pull_request_target:
    types: [opened, synchronize, ready_for_review]
  pull_request_review:
    types: [submitted]

jobs:
  dispatch:
    runs-on: ubuntu-latest
    steps:
      - name: Dispatch to fullsend
        env:
          GH_TOKEN: ${{ secrets.FULLSEND_DISPATCH_TOKEN }}
        run: |
          gh workflow run agent.yaml \
            --repo "${{ github.repository_owner }}/.fullsend" \
            --field event_type="${{ github.event_name }}" \
            --field source_repo="${{ github.repository }}" \
            --field event_payload='${{ toJSON(github.event) }}'
```

## 6. Forge operation sequence (install)

For each enabled target repository that is not enrolled (§4), the installer MUST perform these operations in order:

1. **CreateBranch** — Create **enrollment branch** from the target repository’s **default branch** tip (same SHA the hosting API uses for “branch from default”).
2. **CreateFileOnBranch** — Create **shim path** on **enrollment branch** with content from §5, using the commit message from §3.
3. **CreateChangeProposal** — Open a pull request with:
   - **head:** enrollment branch name (`fullsend/onboard`)
   - **base:** the configured default branch name for that repository, or `main` if none is configured (§3)
   - **title (exact):** `Connect to fullsend agent pipeline`
   - **body (exact text, Markdown):**

     ```markdown
     This PR adds a shim workflow that routes repository events to the fullsend agent dispatch workflow in the `.fullsend` config repo.

     Once merged, issues, PRs, and comments in this repo will be handled by the fullsend agent pipeline.
     ```

## 7. Batch and failure behavior

- Install MUST iterate all enabled target repositories from org configuration.
- Cancellation (`context` cancelled) MUST abort the install and return an error.
- Failure on one repository (after detection) MUST be reported (for example warning + message) and MUST NOT prevent attempting enrollment for remaining repositories.
- Uninstall: v1 does **not** require removing the shim from target repositories (no normative uninstall automation).

## 8. Analyze (read model)

For reporting, a repository is **enrolled** / **not enrolled** / **unknown** using the same **shim path** and default-branch read as §4. Partial results across enabled repos MUST be representable as degraded state when some are enrolled and some are not.
