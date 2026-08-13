# fullsend-ai Per-Org to Per-Repo Migration Plan

## Inventory

| Repo | Order |
|------|-------|
| experiments | 1st |
| metrics | 2nd |
| agents | 3rd |
| fullsend | 4th |

## What `repos migrate` does

For the targeted repo, `repos migrate` performs these steps:

1. **Reads per-org config** — fetches `config.yaml` from the `.fullsend` repo to confirm the repo is enrolled
2. **Checks installation status** — looks for `FULLSEND_PER_REPO_INSTALL` guard variable; skips repos already per-repo
3. **Warns about non-portable fields** — `max_implementation_retries` and `auto_merge` have no per-repo equivalent and will be lost
4. **Provisions WIF** — checks if GCP Workload Identity Federation is already set up for the repo; provisions it if not
5. **Builds per-repo config** — carries over portable org config fields: roles, allowed_remote_resources, agents, create_issues, kill_switch, runtime
6. **Commits scaffold files** — writes `.github/workflows/fullsend.yaml` and `.fullsend/config.yaml` directly to the default branch (with `--direct`)
7. **Writes repo-level variables** — `FULLSEND_MINT_URL`, `FULLSEND_PER_REPO_INSTALL`, `FULLSEND_GCP_REGION`
8. **Writes repo-level secrets** — `FULLSEND_GCP_PROJECT_ID`, `FULLSEND_GCP_WIF_PROVIDER`
9. **Registers per-repo WIF with mint** — adds the repo to the mint service's `PER_REPO_WIF_REPOS` (serialized to avoid race conditions)
10. **Unenrolls from per-org config** — sets `enabled: false` for the repo in `.fullsend/config.yaml` and commits the update
11. **Generates `repos.yaml` manifest** — includes the migrated repo with per-repo overrides where values differ from defaults

It does **not**: delete the `.fullsend` repo, remove org-level variables/secrets, remove old workflow files from `.fullsend`, or touch GitHub Apps.

## Phase 0: Prerequisites

- Identify GCP project ID

## Phase 1: Migrate repos individually

For each repo in order:

```bash
# migrate
fullsend repos migrate fullsend-ai --project <GCP_PROJECT_ID> --direct --repo <repo>

# check status
fullsend repos status -f repos.yaml
```

Then verify by opening a test issue:

```
Title: test: verify per-repo install
Body: This is a test issue to verify the per-repo fullsend workflow. Close after triage responds.
```

Wait for the triage agent to respond with labels/priority — this confirms the full chain: workflow trigger -> mint token -> GCP WIF auth -> agent execution. Close the issue after triage responds. Proceed to the next repo.

Order:
1. experiments
2. metrics
3. agents
4. fullsend

## Phase 2: Unenroll org from mint

Remove the org from the mint's `ALLOWED_ORGS` and WIF provider condition to block per-org token generation. Per-repo WIF registrations (added by migrate in step 9) are unaffected — they use `PER_REPO_WIF_REPOS`, a separate env var.

```bash
fullsend mint unenroll fullsend-ai --project <GCP_PROJECT_ID>
```

## Phase 3: Manual cleanup

### Org-level variable (delete 1)

```bash
gh variable delete FULLSEND_MINT_URL --org fullsend-ai
```

### `.fullsend` repo — workflows (delete 8, keep 3)

| Action | Files |
|--------|-------|
| Delete | `code.yml`, `dispatch.yml`, `fix.yml`, `renovate.yml`, `repo-maintenance.yml`, `retro.yml`, `review.yml`, `triage.yml` |
| Keep | `scribe.yml`, `prioritize-scheduler.yml`, `prioritize.yml` |

### `.fullsend` repo — files (delete 3)

| Action | Files |
|--------|-------|
| Delete | `config.yaml`, `renovate.json`, `CODEOWNERS` |

### `.fullsend` repo — directories (delete 2, keep 1)

| Action | Directory | Reason |
|--------|-----------|--------|
| Delete | `hack/` | `update-agent-hashes.sh`, `summarize-rice.py` — dead |
| Delete | `templates/` | Per-org dispatch shims — dead |
| Keep | `docs/` | Scribe documentation |

### `.fullsend` repo — variables (delete 5, keep 10)

| Action | Variable | Reason |
|--------|----------|--------|
| Delete | `FULLSEND_FULLSEND_CLIENT_ID` | Not used by remaining workflows |
| Delete | `FULLSEND_GCP_AUTH_MODE` | Per-org config |
| Delete | `FULLSEND_PRIORITIZE_CLIENT_ID` | Not used by remaining workflows |
| Delete | `FULLSEND_RETRO_CLIENT_ID` | Not used by remaining workflows |
| Delete | `FULLSEND_REVIEW_CLIENT_ID` | Not used by remaining workflows |
| Keep | `FULLSEND_CODER_CLIENT_ID` | Used by scribe |
| Keep | `FULLSEND_TRIAGE_CLIENT_ID` | Used by scribe |
| Keep | `FULLSEND_GCP_REGION` | Used by scribe |
| Keep | `FULLSEND_MINT_URL` | Used by prioritize-scheduler |
| Keep | `FULLSEND_PROJECT_NUMBER` | Used by prioritize-scheduler |
| Keep | `OTEL_RESOURCE_ATTRIBUTES` | Observability |
| Keep | `SCRIBE_DRY_RUN` | Scribe config |
| Keep | `SCRIBE_GDRIVE_SEARCH_QUERY` | Scribe config |
| Keep | `SCRIBE_LOOKBACK_HOURS` | Scribe config |
| Keep | `SCRIBE_TARGET_REPO` | Scribe config |

### `.fullsend` repo — secrets (delete 5, keep 7)

| Action | Secret | Reason |
|--------|--------|--------|
| Delete | `FULLSEND_FULLSEND_APP_PRIVATE_KEY` | Not used by remaining workflows |
| Delete | `FULLSEND_GH_CLASSIFY_PROJECT_PAT` | gh-classify dead |
| Delete | `FULLSEND_PRIORITIZE_APP_PRIVATE_KEY` | Not used by remaining workflows |
| Delete | `FULLSEND_RETRO_APP_PRIVATE_KEY` | Not used by remaining workflows |
| Delete | `FULLSEND_REVIEW_APP_PRIVATE_KEY` | Not used by remaining workflows |
| Keep | `FULLSEND_CODER_APP_PRIVATE_KEY` | Used by scribe |
| Keep | `FULLSEND_TRIAGE_APP_PRIVATE_KEY` | Used by scribe |
| Keep | `FULLSEND_GCP_PROJECT_ID` | Used by scribe |
| Keep | `FULLSEND_GCP_WIF_PROVIDER` | Used by scribe |
| Keep | `FULLSEND_GCP_WIF_SA_EMAIL` | GCP auth |
| Keep | `SCRIBE_GCP_SA_KEY_JSON` | Used by scribe |
| Keep | `SCRIBE_SLACK_WEBHOOK_URL` | Used by scribe |
