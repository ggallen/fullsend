# Proposal: Jira work items in the built-in agent workflow

**Date:** 2026-10-29

**Status:** For review; no parent or sub-issues have been created.

**Scope:** Jira as the work-item tracker for triage and code, followed by the
existing GitHub/GitLab pull request or merge request workflow.

**Out of scope:** Jira repository-install setup tracked by
[fullsend#8906](https://github.com/fullsend-ai/fullsend/issues/8906).

This note proposes an issue hierarchy, deliverables, dependencies, and
acceptance criteria. It is a planning proposal, not an implementation or an
accepted architecture decision. Create the issue tree only after the scope
and open decisions below are agreed.

## Jira's role in the workflow

Jira replaces GitHub Issues or GitLab Issues as the work-item tracker. It does
not replace the code host or its PR/MR lifecycle.

1. Triage reads a Jira work item and writes its classification and issue
   updates to Jira.
2. Code reads a Jira work item, changes code in the configured repository,
   and opens a PR/MR in GitHub or GitLab. The PR/MR should include the Jira
   key and URL for human traceability.
3. PR/MR lifecycle stages continue to run from code-host events and operate
   on the PR/MR. This scope does not add Jira integration to those stages.

The Jira issue key in the PR/MR is a reference, not an authorization signal
or a reason for PR/MR processing to call Jira. If a later product requirement
asks for Jira-side PR lifecycle updates or issue-based PR commands, scope that
separately.

## Current state

- `triage.yaml` already has Jira trigger cases and a Jira overlay. Its trigger
  covers Jira issue open/edit events, the `ready-for-triage` label, and
  `/fs-triage` comments. Full support still needs confirmation that triage's
  substantive results and issue updates are written to Jira.
- `code.yaml` already has a Jira overlay and `pre-code-jira.sh`, but no
  `trigger:`. The overlay supplies the Jira work-item URL and deliberately
  clears `ISSUE_NUMBER`; exercise the complete path and preserve the Jira key.
- Jira polling emits `entity.kind: work_item` events for issue changes. That
  is sufficient for triage and code. It does not need to synthesize PR/MR
  lifecycle events or attach change-proposal data to Jira issue events.
- The Jira guide currently says `/fs-triage`, `/fs-code`, and
  `ready-to-code` are work-item routes. It also says substantive output
  defaults to GitHub and points to
  [fullsend#2264](https://github.com/fullsend-ai/fullsend/issues/2264) for
  Jira pipeline identity work. See
  [`jira-integration.md`](../guides/user/jira-integration.md).
- [agents#1067](https://github.com/fullsend-ai/agents/issues/1067) is the
  existing trigger issue. Its body/checklist needs to be refreshed to match
  the current harnesses and this narrower Jira boundary.

## Proposed issue hierarchy

Create a new umbrella issue in `fullsend-ai/fullsend`. GitHub supports adding
sub-issues from other repositories, so `agents#1067` can be a child alongside
fullsend issues. Both repositories have the same organization owner. See
[GitHub's sub-issue guide](https://docs.github.com/en/issues/tracking-your-work-with-issues/using-issues/adding-sub-issues).
Before attaching an existing issue, check whether it already has a parent;
reparent only as part of the reviewed hierarchy change. Confirm #2264 is still
open and still matches the identity-plumbing scope before reusing it.

```text
fullsend#NEW — Use Jira as the work-item tracker for the built-in agent loop
├── fullsend#2264 — Preserve Jira work-item identity through agent execution
├── fullsend#NEW — Write triage and code results to the Jira work item
├── agents#1067 — Complete work-item and PR/MR triggers for the agent loop
└── fullsend#NEW — Verify Jira-to-code-host workflow and regressions
```

The `NEW` entries are proposals; do not create them until this plan is agreed.
Keep focused unit/fixture tests and component docs in the issue that owns the
behavior. The final validation issue owns only the cross-repository vertical
scenario.

### Parent issue — new, `fullsend-ai/fullsend`

**Proposed title:** Use Jira as the work-item tracker for the built-in agent loop.

The parent states the product outcome and links its children. It does not own
implementation work. Its acceptance criteria are:

- Human Jira issue activity can trigger triage and code using the supported
  slash commands and label rules.
- Triage's issue updates and code's created PR/MR link are available on the
  Jira work item.
- Code changes and PR/MR creation stay in the configured GitHub/GitLab repo.
- Existing PR/MR lifecycle stages continue to run from code-host events and
  keep their existing code-host outputs.
- Existing GitHub and GitLab issue and PR/MR flows pass unchanged.
- A vertical test proves the Jira issue → code PR/MR → review/fix/retro flow
  while keeping the Jira key distinct from numeric forge issue numbers.

Jira install/provisioning is not a parent acceptance criterion. Work in
fullsend#8906 can proceed separately and in parallel.

### Child 1 — existing, `fullsend-ai/fullsend#2264`: preserve work-item identity

Use #2264 for the Jira work-item key/URL pipeline gap, subject to confirming
its current body still matches this scope. Ensure Jira identity stays distinct
from forge issue numbers:

- Preserve `FULLSEND_WORK_ITEM_URL`, `FULLSEND_WORK_ITEM_SOURCE`, and
  `FULLSEND_WORK_ITEM_KEY` through dispatch, reusable workflows, runners, and
  triage/code scripts.
- Keep Jira's textual key (for example `PROJ-123`) in the generic key field.
  Do not put it in numeric `ISSUE_NUMBER` or `status-number` fields.
- Preserve current numeric GitHub/GitLab issue and PR/MR variables for
  forge-originated runs.
- Remove assumptions that every work-item URL is a GitHub/GitLab URL while
  retaining existing validation for those sources.

**Acceptance:** Jira key and URL reach triage and code; existing
GitHub/GitLab environment-variable and payload tests remain unchanged and
pass; malformed or missing Jira identity fails closed before scripts run.

**Dependencies:** none. Agent harness work consumes this contract.

### Child 2 — new, `fullsend-ai/fullsend`: write triage/code results to Jira

**Proposed title:** Write triage and code results to the Jira work item.

Complete tracker-facing writeback for the two Jira work-item stages:

- Triage posts its summary and applies supported labels, components, or status
  transitions to the originating Jira issue.
- Code posts the created PR/MR URL and a concise status/update to the Jira
  issue. Branch, commit, PR/MR, review, and merge operations stay on the code
  host.
- Reuse the existing tracker client/CLI path where possible. Keep Jira API
  write credentials in the runner-side boundary if the sandbox does not need
  them.
- Make retries idempotent and ensure writes by the authenticated Jira poller
  remain classified as bot events so they do not recursively trigger triage
  or code.
- Preserve all current GitHub/GitLab write destinations and behavior.

**Acceptance:** focused tests prove destination selection for Jira triage/code
and existing forge runs; Jira writes preserve comment text and link targets;
failures are visible rather than reported as successful writes. Update the
Jira guide to say which issue-stage outputs appear in Jira and which code
artifacts remain on GitHub/GitLab.

**Compatibility note:** current docs say substantive Jira-run output defaults
to GitHub. Adding Jira issue-stage writeback is an intentional additive
behavior change. Replacing existing forge destinations or removing existing
outputs is not part of this proposal.

**Dependencies:** Child 1. Can proceed in parallel with the agents trigger
work once the shared work-item identity is agreed.

**Likely code surface:** tracker-aware issue/comment operations in
`internal/tracker/`, dispatch/run workflow environment, and the
agent-facing execution contract. Existing run-status routing already selects
Jira from the source event; keep it intact.

### Child 3 — existing, `fullsend-ai/agents#1067`: complete the agent triggers

Refresh #1067's stale description and keep its scope on trigger/configuration
behavior:

- Retain and regression-test triage's current Jira trigger for issue open/edit,
  `ready-for-triage`, and `/fs-triage`.
- Add the Jira work-item trigger for code: `/fs-code` and `ready-to-code`, with
  source, actor, and entity guards matching the normalized-event contract.
- Verify the code Jira overlay/pre-script consumes the generic work-item URL
  and key and never requires a GitHub issue number.
- Ensure a Jira-originated code PR/MR includes the Jira key and URL in its
  title or description for traceability and Jira's development integration.
- Add or verify fix's normal code-host PR/MR trigger for `/fs-fix` and the
  supported changes-requested event. Do not make it a Jira issue trigger.
- Keep review and retro on their existing GitHub/GitLab PR/MR triggers. Do not
  add Jira API calls, Jira overlays, or Jira slash-command predicates to these
  PR-stage agents in this scope.

**Acceptance:** all expressions compile and evaluate without CEL errors over
the normalized-event fixture set; positive and negative Jira triage/code
cases are explicit; fix cases use forge PR/MR fixtures; review/retro existing
GitHub/GitLab cases continue to match. Update the agents guide pages for the
changed triggers.

**Dependencies:** trigger tests can start immediately. Full Jira execution
acceptance depends on Child 1; Jira writeback acceptance also depends on
Child 2.

**Likely agents surface:** `harness/triage.yaml`, `harness/code.yaml`,
`harness/fix.yaml`, review/retro trigger regression tests, `harness/triggertest/`,
code Jira pre/post scripts, and the corresponding `docs/{triage,code,fix}.md`.

### Child 4 — new, `fullsend-ai/fullsend`: vertical validation

**Proposed title:** Verify Jira work items through the built-in PR lifecycle.

Add a vertical behavior test at the strongest existing test seam:

1. A human Jira comment invokes `/fs-triage`; triage updates the Jira issue.
2. A human Jira comment invokes `/fs-code` or a `ready-to-code` label does so.
3. Code opens a PR/MR in the configured repo and includes the Jira key/URL;
   Jira receives the PR/MR link and a concise code-stage update.
4. The code host's normal PR/MR events invoke review, fix, and retro. Their
   prompts and outputs operate on the PR/MR; these stages make no Jira API
   calls under this contract.
5. Existing GitHub/GitLab issue and PR/MR fixtures still dispatch as before.

Also prove that bot-authored Jira writes do not loop, role gates remain
effective, retries do not duplicate Jira output, and Jira keys never enter
numeric forge issue fields. Prefer deterministic fakes/fixtures for Jira API
interactions; do not require the out-of-scope install work in #8906.

**Dependencies:** Children 1–3. Earlier issues own their focused unit and
fixture tests; this issue owns only the cross-repository flow.

## Ordering and parallel work

| Order | Work | Depends on | Can run in parallel with |
|---|---|---|---|
| 1 | Child 1: Jira work-item identity (#2264) | — | Children 2 and 3 |
| 2 | Child 2: Jira triage/code writeback | Child 1 | Child 3 |
| 2 | Child 3: agent triggers and Jira/code-host harness behavior (#1067) | Child 1 for Jira execution | Child 2 after the output contract is agreed |
| 3 | Child 4: vertical behavior validation | Children 1–3 | — |

The shared URL/key contract should be settled first. Writeback and harness
trigger changes can then proceed in parallel if they agree on the output
contract. No Jira-to-PR event association is a prerequisite: PR/MR lifecycle
agents continue to receive native code-host events.

## Non-regression requirements

- Keep existing GitHub and GitLab trigger expressions, destinations, and
  execution variables working. Add source-guarded Jira behavior rather than
  widening forge-specific predicates indiscriminately.
- Keep Jira numeric `entity.id` distinct from textual `entity.key`. Never put
  `PROJ-123` into a numeric issue/PR field.
- Preserve actor-role, repository-ownership, fork, and dispatch authorization
  gates. The Jira key in PR metadata is a reference, not authorization.
- Keep Jira issue `closed`, forge PR/MR `closed`, and forge PR/MR `merged` as
  distinct transitions.
- Keep review/fix/retro PR/MR operations on the code host. Their existing
  outputs and authorization behavior must not change as a side effect of Jira
  triage/code support.
- Preserve the Jira poller's bot/self-identity classification so issue-stage
  writeback cannot recursively dispatch triage or code.
- Keep Jira and code-host credentials separate and scoped to their operations.
- Test existing GitHub/GitLab fixtures and workflow projections alongside
  Jira cases before merge.

## Open decisions for review

1. Should Jira receive triage/code summaries additively while existing
   forge-side outputs remain, or should a later approved change make Jira the
   sole work-item destination? The safe default here is additive Jira output.
2. Should the Jira key/URL in a generated PR/MR use a simple visible
   reference, or should code also create a first-class Jira remote link?
   Neither choice should require review/fix/retro to call Jira.
3. Which existing test environment can exercise the vertical flow without
   requiring installation/setup work from #8906?

## Explicit exclusions

- Do not add fullsend#8906 as a child. Jira poll installation, secret
  provisioning, schedule creation, and repo-install command integration can
  be delivered separately and in parallel.
- Do not make review, fix, or retro Jira-driven or Jira-API clients. Their
  event source remains the GitHub/GitLab PR/MR lifecycle.
- Do not add Jira-to-PR/MR lookup, linked change-proposal fields to Jira poll
  events, or synthetic merge/review events from Jira issue transitions.
- Do not change default GitHub/GitLab routing or numeric issue-number
  semantics as part of this work.
- Do not include Jira Data Center support or unrelated Jira identity/role
  migration work unless an implementation proves it is a blocker.

## References

- [agents#1067 — Jira-compatible CEL triggers](https://github.com/fullsend-ai/agents/issues/1067)
- [fullsend#2264 — Jira work-item key/pipeline integration](https://github.com/fullsend-ai/fullsend/issues/2264)
- [fullsend#8906 — Jira poll installation option (excluded)](https://github.com/fullsend-ai/fullsend/issues/8906)
- [Jira poll adapter contract](../normative/normalized-event/v1/jira-poll-adapter.md)
- [Jira integration guide](../guides/user/jira-integration.md)
- [agents triage harness](https://github.com/fullsend-ai/agents/blob/main/harness/triage.yaml)
- [agents code harness](https://github.com/fullsend-ai/agents/blob/main/harness/code.yaml)
- [agents fix harness](https://github.com/fullsend-ai/agents/blob/main/harness/fix.yaml)
- [agents review harness](https://github.com/fullsend-ai/agents/blob/main/harness/review.yaml)
- [agents retro harness](https://github.com/fullsend-ai/agents/blob/main/harness/retro.yaml)
