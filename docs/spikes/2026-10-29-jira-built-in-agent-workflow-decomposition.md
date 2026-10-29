# Proposal: issue decomposition for Jira-backed built-in agents

**Date:** 2026-10-29

**Status:** For review; no parent or sub-issues have been created.

**Scope:** Jira support for triage, code, fix, review, and retro.
**Out of scope:** Jira repository-install setup tracked by
[fullsend#8906](https://github.com/fullsend-ai/fullsend/issues/8906).

This note proposes an issue hierarchy, deliverables, dependencies, and
acceptance criteria. It is a planning proposal, not an implementation or an
accepted architecture decision. The issue tree should be created only after
the scope and open decisions below are agreed.

## Goal and proposed boundary

Treat Jira as the work-item tracker and the configured GitHub or GitLab
repository as the code host. Jira issues should be able to start triage and
code work. Review, fix, and retro should operate on the associated PR or MR,
with both the Jira work item and forge change proposal available to the run.
PR/MR lifecycle events remain authoritative at the code host; a Jira issue
closing is not the same event as a PR/MR merging.

For Jira-originated runs, tracker-facing summaries and issue updates should be
available in Jira. Branch, commit, PR/MR, inline-review, and other change-host
operations remain on GitHub or GitLab. Existing GitHub- and GitLab-originated
behavior must remain unchanged. The proposal is to add Jira-facing output
without removing current forge-side output until any existing Jira users have
reviewed that behavior.

## Current state

- `triage.yaml` already has Jira trigger cases and a Jira overlay. Its trigger
  covers Jira issue open/edit events, the `ready-for-triage` label, and
  `/fs-triage` comments.
- `code.yaml` has a Jira overlay and `pre-code-jira.sh`, but no `trigger:`.
  The Jira overlay supplies the Jira work-item URL and deliberately clears
  `ISSUE_NUMBER`; it still needs to be exercised through the complete pipeline.
- `fix.yaml` has no trigger. `review.yaml` has change-proposal triggers and
  GitHub/GitLab forge configuration. `retro.yaml` is limited to change
  proposals and its comment path allows only GitHub or GitLab.
- Jira polling emits `entity.kind: work_item`; it does not emit
  `entity.linked_change_proposal` or `state.change_proposal`. The normative
  adapter contract currently leaves PR-scoped review, fix, and retro out of
  Jira polling scope. See
  [`jira-poll-adapter.md`](../normative/normalized-event/v1/jira-poll-adapter.md).
- The normalized-event model already has linked-work-item and
  linked-change-proposal fields, and dispatch can project both issue and PR
  payloads when the adapter supplies the relationship. No current adapter
  fills in the Jira-to-PR/MR relationship. See the
  [NormalizedEvent execution-ref projection](../normative/normalized-event/v1/README.md#execution-ref-projection).
- The Jira guide documents `/fs-triage`, `/fs-code`, and `ready-to-code`, but
  says review/fix/retro do not run from a bare Jira issue. It also says
  substantive agent output still defaults to GitHub and points to
  [fullsend#2264](https://github.com/fullsend-ai/fullsend/issues/2264) for
  pipeline integration. See
  [`jira-integration.md`](../guides/user/jira-integration.md).
- [agents#1067](https://github.com/fullsend-ai/agents/issues/1067) is the
  existing trigger issue. Its body and checklist need to be refreshed to
  reflect the current state and the expanded five-agent goal.

## Proposed issue hierarchy

Create a new umbrella issue in `fullsend-ai/fullsend`. GitHub supports adding
sub-issues from other repositories, so the existing `agents#1067` issue can
be a child alongside fullsend issues. Both repositories are owned by the same
organization. See
[GitHub's sub-issue guide](https://docs.github.com/en/issues/tracking-your-work-with-issues/using-issues/adding-sub-issues).
Before attaching an existing issue, check whether it already has a parent;
reparent only as part of the reviewed hierarchy change. Confirm #2264 is still
open and still matches the identity-plumbing scope before reusing it.

```text
fullsend#NEW — Support the Jira-backed built-in agent workflow
├── fullsend#NEW — Define Jira work-item ↔ PR/MR association contract
├── fullsend#2264 — Preserve Jira work-item identity through agent execution
├── fullsend#NEW — Populate linked Jira/PR context in normalized events
├── fullsend#NEW — Add Jira tracker-facing output for built-in runs
├── agents#1067 — Complete Jira issue triggers for triage and code
├── agents#NEW — Support Jira-linked review and fix workflows
├── agents#NEW — Support retro for Jira-linked PRs/MRs
└── fullsend#NEW — Validate the end-to-end Jira agent workflow
```

The `NEW` entries are proposals; do not create them until this plan is agreed.
Keep documentation and focused unit/fixture tests within the implementation
issue that owns the behavior. The final validation issue owns the cross-repo
vertical scenario and regression proof.

### Parent issue — new, `fullsend-ai/fullsend`

**Proposed title:** Support the Jira-backed built-in agent workflow.

The parent states the product outcome and links its children. It does not own
implementation work. Its acceptance criteria are:

- A Jira issue can start the supported triage and code actions.
- A linked PR/MR can be reviewed, fixed, and retrospected with the Jira key
  and URL available as work-item context.
- Tracker-facing output reaches Jira; code changes and PR/MR operations stay
  on the configured code host.
- Existing GitHub and GitLab issue and PR/MR behavior passes unchanged.
- Missing or ambiguous Jira-to-PR/MR links fail closed with a useful status;
  they never select an arbitrary change proposal.
- The complete flow is covered by deterministic tests and an end-to-end
  behavior test at the strongest existing test seam.

Jira install/provisioning is not a parent acceptance criterion. Repos install
work in fullsend#8906 can proceed separately and in parallel.

### Child 1 — new, `fullsend-ai/fullsend`: define the link contract

**Proposed title:** Define how Jira work items link to code-host changes.

Decide and document one authoritative way to associate a Jira issue with a
GitHub PR or GitLab MR. The decision must cover newly-created and pre-existing
PRs/MRs, repository identity, zero/multiple matches, and how a Jira issue
command selects a change proposal. The current event model can carry one
`entity.linked_change_proposal` on a work-item event and a collection of
`state.change_proposal.linked_work_items` on a change-proposal event; define
the supported cardinality for each direction.

Options to evaluate:

1. Use an explicit Jira remote link or equivalent first-class Jira relation.
   This is clear to users and less prone to accidental text matches, but
   requires Jira write/read support and consistent link creation.
2. Use a stable, machine-readable Jira key or URL in PR/MR metadata. This is
   easy for the code stage to create and works with existing PRs after
   annotation, but needs strict parsing and ambiguity handling.
3. Use both: a first-class link as the canonical relation and PR/MR metadata
   as a visible, verifiable fallback. This is more robust but adds sync rules.

**Proposed safe default for review:** prefer an explicit link created by the
code pipeline; allow a documented metadata fallback only when it resolves to
exactly one work item in the configured Jira project and one change proposal
in the configured code repository. A link is context, never authorization.
The Jira actor role and normal dispatch authorization gate remain authoritative.
For `/fs-fix` or `/fs-review` from a Jira issue, require exactly one eligible
open PR/MR; zero or multiple matches produce no agent dispatch and a clear
status. The contract should say whether a PR/MR may link multiple Jira work
items and how a forge-originated run chooses its tracker-facing destination.

**Acceptance:** the normalized-event spec describes the relation and failure
semantics; fixtures cover both directions, no link, duplicate link, wrong
repository, and multiple links; the chosen rule does not grant write access.

**Dependencies:** none. This is the prerequisite for linked-context runtime
work and the Jira-linked review/fix/retro harnesses.

### Child 2 — existing, `fullsend-ai/fullsend#2264`: preserve work-item identity

Use #2264 for the Jira work-item key/URL pipeline gap, subject to confirming
its current body still matches this scope. Ensure Jira identity stays distinct
from forge issue numbers:

- Preserve `FULLSEND_WORK_ITEM_URL`, `FULLSEND_WORK_ITEM_SOURCE`, and
  `FULLSEND_WORK_ITEM_KEY` through dispatch, reusable workflows, runners, and
  pre/post scripts.
- Keep Jira's textual key (for example `PROJ-123`) in the generic key field.
  Do not put it in numeric `ISSUE_NUMBER` or `status-number` fields.
- Preserve current numeric GitHub/GitLab issue and PR/MR variables for
  forge-originated runs.
- Remove assumptions that every issue URL is a GitHub/GitLab URL, while
  retaining the existing validation for those sources.

**Acceptance:** Jira key and URL reach all five agent harnesses; existing
GitHub/GitLab environment-variable and payload tests remain unchanged and
pass; malformed or missing Jira identity fails closed before scripts run.

**Dependencies:** can proceed in parallel with Child 1. Agent work that
consumes the generic work-item identity depends on this child.

### Child 3 — new, `fullsend-ai/fullsend`: populate linked event context

**Proposed title:** Carry Jira and PR/MR linkage through dispatch events.

Implement the selected Child 1 contract in the Jira and code-host adapters.
For Jira issue events, attach the linked change-proposal identity and its
required `state.change_proposal` snapshot when the relation is unambiguous.
For GitHub/GitLab PR/MR events, include linked Jira work-item identity when
available. Project the two contexts into the execution payload so existing
review/fix scripts receive PR/MR number, URL, head/base refs and the Jira
work-item URL/key without another lookup at runtime.

Do not turn Jira issue `closed` into change-proposal `merged` or `closed`.
Keep PR/MR open, synchronize, review, merge and close events sourced from the
code host. If association lookup fails or is ambiguous, preserve the event's
original context and skip only linked-work-item actions.

**Acceptance:** adapter and projection tests cover valid links, absent links,
ambiguous links, wrong-repo links, matching entity IDs, and complete payloads.
Unlinked GitHub/GitLab events retain their current payloads.

**Dependencies:** Child 1. Can proceed in parallel with Child 2 and Child 4.

**Likely code surface:** `internal/jirapoll/`, the GitHub/GitLab event
adapters, `internal/normevent/`, the normalized-event examples, and
`internal/harnessdispatch/project.go`.

### Child 4 — new, `fullsend-ai/fullsend`: Jira tracker-facing output

**Proposed title:** Route built-in Jira run results to the Jira work item.

Complete the shared tracker-aware output path for Jira-originated runs. Route
tracker-facing status, triage summary, issue labels/components/transitions,
review/fix/retro summary, and the created PR/MR link to the Jira issue where
the relevant agent produces them. Keep PR/MR comments, review threads,
commits, branches, and merge operations on the code host.

Make the Jira behavior source-scoped and additive initially: do not remove
current GitHub/GitLab outputs, and do not change current forge-originated
destinations. Reuse the existing tracker client/CLI capabilities where
possible. Use least-privilege Jira credentials; do not expose write credentials
to the sandbox if a runner-side operation can perform the write. Make retries
idempotent, and ensure writes by the authenticated poller remain bot events so
they do not recursively trigger another run.

**Acceptance:** unit tests prove destination selection by source and agent
output type; Jira writes preserve comment text and link targets; failures are
visible rather than reported as successful writes; existing forge outputs
remain unchanged. The guide states which outputs go to Jira and which stay on
the code host.

**Compatibility note:** the current guide says substantive Jira-run output
defaults to GitHub. Adding Jira output is an intentional additive behavior
change. Replacing GitHub output with Jira-only output would be a separate
behavior change and is not part of this proposal.

**Dependencies:** Child 2. It can proceed in parallel with Child 1 and Child 3.

**Likely code surface:** tracker-aware issue/comment operations in
`internal/tracker/`, the dispatch/run workflow environment, and the
agent-facing execution contract. Run-status routing already selects Jira from
the source event; keep it intact while adding substantive result routing.

### Child 5 — existing, `fullsend-ai/agents#1067`: Jira issue triggers for triage/code

Refresh #1067's stale description and use it for the issue-level stages:

- Keep the existing Jira triage trigger for open/edit, `ready-for-triage`,
  and `/fs-triage`; verify it still matches the current Jira fixture and
  requires a human actor where the operation is write-capable.
- Add the code trigger for `/fs-code` and `ready-to-code`, with source,
  actor, and entity guards matching the accepted work-item contract.
- Verify the existing Jira overlays/pre-scripts consume the generic Jira
  work-item URL/key and do not require a GitHub issue number.
- Keep triage/code's code-host operations and Jira tracker output aligned with
  Child 4; avoid giving the Jira API token to code-host operations.

**Acceptance:** every trigger compiles and evaluates without CEL errors over
all normalized-event fixtures; positive and negative Jira command/label cases
are explicit; the Jira guide lists the supported triggers; existing GitHub and
GitLab trigger cases continue to pass.

**Dependencies:** trigger tests can start after Child 1. Full execution
acceptance depends on Children 2 and 4.

**Likely agents surface:** `harness/triage.yaml`, `harness/code.yaml`,
`harness/triggertest/`, Jira pre/post scripts, and `docs/triage.md` /
`docs/code.md` in the agents repository.

### Child 6 — new, `fullsend-ai/agents`: Jira-linked review and fix

**Proposed title:** Support review and fix for Jira-linked PRs/MRs.

Keep native PR/MR review events in GitHub/GitLab. Add Jira issue-command paths
only when Child 1/3 resolves one eligible linked PR/MR. Review must receive
the change proposal snapshot and diff. Fix must receive the review findings,
review body, PR/MR refs, and trigger source; it must continue to push commits
and post code-review replies through the target code host. Tracker-facing
summaries and links go to Jira under Child 4.

Preserve fork protections, reviewer/actor authorization, and current
GitHub/GitLab trigger arms. Do not let a Jira link bypass code-host ownership,
fork, or write authorization checks.

**Acceptance:** fixture tests cover Jira `/fs-review` and `/fs-fix` with one
eligible linked PR/MR, missing/ambiguous links, non-human/bot actors, and
forge-originated review/change-request events. Existing PR/MR behavior and
security guards remain unchanged.

**Dependencies:** Children 1, 2, 3, and 4.

**Likely agents surface:** `harness/fix.yaml`, `harness/review.yaml`, their
pre/post scripts and providers, trigger fixtures, and `docs/fix.md` /
`docs/review.md` in the agents repository.

### Child 7 — new, `fullsend-ai/agents`: retro for Jira-linked PRs/MRs

**Proposed title:** Post retrospectives to Jira for merged Jira-linked PRs/MRs.

Trigger retro from the code host's PR/MR merged or closed event, with the
linked Jira work item carried in the normalized event. If `/fs-retro` is
accepted from a Jira issue, require the same unique eligible PR/MR resolution
as Child 6. Preserve the current rule that a closed Jira issue by itself does
not imply that a PR/MR merged. Post the retrospective to the agreed Jira and
code-host destinations without changing current forge-only runs.

**Acceptance:** tests distinguish Jira issue closure from PR/MR merge, cover
missing/ambiguous links and duplicate dispatches, and prove existing
GitHub/GitLab retro events still match.

**Dependencies:** Children 1, 2, 3, and 4.

**Likely agents surface:** `harness/retro.yaml`, its pre/post scripts,
trigger fixtures, and `docs/retro.md` in the agents repository.

### Child 8 — new, `fullsend-ai/fullsend`: end-to-end validation

**Proposed title:** Prove the Jira-backed built-in agent lifecycle.

Add a vertical behavior test at the strongest existing test seam:

1. A human Jira comment invokes `/fs-code` on a Jira issue.
2. Code creates a branch and PR/MR in the configured code repository and
   records the agreed Jira link.
3. The forge PR/MR event invokes review with both contexts.
4. A changes-requested event or Jira `/fs-fix` invokes fix with the review
   findings; the fix pushes through the code host.
5. A merged PR/MR invokes retro with the linked Jira context.
6. Jira receives the tracker-facing summaries/links, while PR/MR artifacts
   remain on the code host.

Also prove bot-authored Jira writes do not loop, role gates remain effective,
missing/ambiguous links fail closed, retries do not duplicate output, and
GitHub/GitLab baseline fixtures still dispatch as before. Prefer deterministic
fakes/fixtures for Jira API interactions; do not require the out-of-scope
install work in #8906 to run the test.

**Dependencies:** Children 2–7. Each earlier implementation issue still owns
its focused unit and fixture tests; this child owns only the cross-repo flow.

## Ordering and parallel work

| Order | Work | Depends on | Can run in parallel with |
|---|---|---|---|
| 1 | Child 1: agree the link and event contract | — | Children 2 and 4 |
| 2 | Child 2: Jira work-item identity (#2264) | — | Children 1, 3, and 4 |
| 2 | Child 3: populate linked event context | Child 1 | Children 2 and 4 |
| 2 | Child 4: tracker-facing Jira output | Child 2 | Children 1 and 3 |
| 3 | Child 5: triage/code harnesses (#1067) | Child 1 for trigger contract; Children 2 and 4 for full execution | Children 3 and 4 after interfaces are agreed |
| 4 | Child 6: review/fix harnesses | Children 1–4 | Child 7 after shared interfaces are agreed |
| 4 | Child 7: retro harness | Children 1–4 | Child 6 after shared interfaces are agreed |
| 5 | Child 8: vertical validation | Children 2–7 | — |

The design contract must land before code relies on a Jira-to-PR/MR lookup.
The identity and output work can be developed independently after agreeing
their shared environment-variable and destination contracts. Harness changes
may proceed in parallel once those interfaces are stable.

## Non-regression requirements

- Keep all existing GitHub and GitLab trigger expressions and event
  destinations working. Add source-guarded Jira paths rather than widening
  forge-specific predicates indiscriminately.
- Keep Jira numeric `entity.id` distinct from Jira textual `entity.key`.
  Never put `PROJ-123` into a numeric issue/PR field.
- Do not use a Jira-to-PR/MR link as proof that an actor may write to either
  system. Preserve existing role, repository-ownership, fork, and dispatch
  authorization gates.
- Keep Jira issue `closed`, forge PR/MR `closed`, and forge PR/MR `merged` as
  distinct transitions.
- Treat a missing, stale, cross-repository, or ambiguous link as no eligible
  linked change. Report the reason; do not guess.
- Preserve the Jira poller's bot/self-identity classification so the new
  Jira writeback cannot recursively dispatch triage or code.
- Keep Jira and code-host credentials separate and scoped to the operation;
  avoid sending Jira write credentials into the agent sandbox unnecessarily.
- Test all current GitHub/GitLab fixtures and workflow projections alongside
  Jira cases before merge.

## Open decisions for review

1. Which Jira↔PR/MR relation is canonical: Jira remote links, a stable marker
   in PR/MR metadata, or both?
2. Should `/fs-review` and `/fs-fix` on a Jira issue be supported when there is
   exactly one active linked PR/MR? The current event model supports one
   linked change proposal on a work-item event; multiple active PRs need an
   explicit policy.
3. For a forge PR/MR linked to several Jira work items, should tracker-facing
   output go to all linked work items, a configured primary item, or only the
   work item that initiated code creation?
4. Should Jira-originated summaries be additive to existing GitHub output
   (the safe compatibility default here), or should a later approved change
   make Jira the sole tracker destination?
5. Which existing test environment can exercise the vertical flow without
   requiring installation/setup work from #8906?

## Explicit exclusions

- Do not add fullsend#8906 as a child. Jira poll installation, secret
  provisioning, schedule creation, and repo-install command integration can
  be delivered separately and in parallel.
- Do not synthesize PR/MR merge or review events from Jira issue transitions.
- Do not change default GitHub/GitLab routing or numeric issue-number
  semantics as part of this work.
- Do not include Jira Data Center support or unrelated Jira identity/role
  migration work unless an implementation proves it is a blocker.

## References

- [agents#1067 — Jira-compatible CEL triggers](https://github.com/fullsend-ai/agents/issues/1067)
- [fullsend#2264 — Jira work-item key/pipeline integration](https://github.com/fullsend-ai/fullsend/issues/2264)
- [fullsend#8906 — Jira poll installation option (excluded)](https://github.com/fullsend-ai/fullsend/issues/8906)
- [Jira poll adapter contract](../normative/normalized-event/v1/jira-poll-adapter.md)
- [Normalized-event schema](../normative/normalized-event/v1/normalized-event.schema.json)
- [Jira integration guide](../guides/user/jira-integration.md)
- [agents triage harness](https://github.com/fullsend-ai/agents/blob/main/harness/triage.yaml)
- [agents code harness](https://github.com/fullsend-ai/agents/blob/main/harness/code.yaml)
- [agents fix harness](https://github.com/fullsend-ai/agents/blob/main/harness/fix.yaml)
- [agents review harness](https://github.com/fullsend-ai/agents/blob/main/harness/review.yaml)
- [agents retro harness](https://github.com/fullsend-ai/agents/blob/main/harness/retro.yaml)
