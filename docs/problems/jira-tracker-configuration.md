# Jira Tracker Configuration and Access

Fullsend has several Jira entry points: scheduled polling, the `fullsend
issues` commands, run-status updates, reconciliation, and agent access. Their
connection settings and authentication are currently split across command-line
flags, environment variables, workflow inputs, and agent-specific credentials.
This document records the desired configuration shape, the authentication
options, and the work needed to make those entry points consistent.

The Jira poller is specified by [ADR 0063](../ADRs/0063-polling-based-work-discovery.md).
Tracker-routed status updates are specified by
[ADR 0093](../ADRs/0093-tracker-routed-status-notifications.md).

## Current behavior

- `config.yaml` accepts `tracker` only as a string. It can select a default
  tracker for `fullsend issues`, but it cannot hold Jira connection or poll
  settings.
- `fullsend poll --input-driver jira-poll` accepts Jira URL, project, and JQL
  through flags, with URL and repository fallbacks from environment variables.
  The Jira client currently uses Basic authentication and requires
  `JIRA_USER_EMAIL` with `JIRA_TOKEN`; this legacy behavior is removed by the
  proposed token-only design.
- `fullsend issues` reads the default tracker from config. Jira URL, email, and
  token still come from flags or `JIRA_BASE_URL`, `JIRA_USER_EMAIL`, and
  `JIRA_TOKEN`. The project is currently required on each command.
- `fullsend run` status notifications and `reconcile-status` construct Jira
  clients from those same three environment variables.
- The built-in `atlassian-cloud` provider and network profile are documented
  for email-plus-token access to `*.atlassian.net`. The custom host-side script
  example also uses Basic auth against the Jira site URL.

## Proposed configuration shape

GitHub and GitLab remain scalar tracker values. Jira uses a mapping containing
its connection and poll settings:

```yaml
tracker: github
```

```yaml
tracker:
  jira:
    base_url: https://example.atlassian.net
    # Optional; defaults to https://api.atlassian.com
    api_url: https://api.atlassian.com
    project: PROJ
    poll:
      enabled: true
      query: 'labels = "fullsend" AND statusCategory != Done ORDER BY updated DESC'
```

`base_url` and `project` are required for the Jira mapping. `api_url` is
optional and defaults to Atlassian's API gateway. `project` identifies the
repository's only Jira project; polling and Jira issue commands use that
project. It is not a fallback or a per-command project override. Polling is
controlled by `poll.enabled`; `poll.query` supplies the JQL filter. When
`poll.enabled` is `true`, `fullsend repos install` installs the Jira polling
pieces. When it is `false`, `repos install` removes the Jira polling pieces it
manages. If `poll` or `poll.enabled` is missing from the effective config, the
poll workflow is not installed. If `poll.query` is omitted, polling uses the
current default filter, `statusCategory != Done ORDER BY updated DESC`, scoped
to the configured project.

On the first Jira-enabled `fullsend repos install`, provide the Jira token on
the command line with `--jira-token`. Install stores it as the repo secret
`FULLSEND_JIRA_TOKEN`; later runs reuse that secret when the flag is omitted.
The token is not written to either config file or printed in command output.

GitHub and GitLab keep their current scalar form (`tracker: github` and
`tracker: gitlab`). Credentials stay out of `config.yaml` and `repos.yaml`.
The target Jira flow uses only `FULLSEND_JIRA_TOKEN`; no account email is part
of its configuration or authentication.

When this mapping is used in `repos.yaml`, it belongs under the existing
managed `config` key. Layered configuration should allow an installation-level
Jira site and API URL to be combined with a repository-level project and poll
query. A child scalar tracker selection replaces the inherited Jira mapping.

## Authentication and URL roles

The current Basic-auth requirement is legacy behavior. The target path uses a
scoped API token from the `FULLSEND_JIRA_TOKEN` environment variable. No email
is required or passed. Fullsend-managed Jira API calls, including polling,
issue commands, run-status updates, and reconciliation, use Bearer
authentication through `api_url`. Atlassian documents this
[scoped-token flow](https://support.atlassian.com/user-management/docs/manage-api-tokens-for-service-accounts/):

```text
{api_url}/ex/jira/{cloudId}/rest/api/3/{resource}
```

`base_url` identifies the Jira site, supports issue browse links, and can
resolve the Cloud ID at `/_edge/tenant_info`. Atlassian documents this
[Cloud ID lookup](https://support.atlassian.com/jira/kb/retrieve-my-atlassian-sites-cloud-id/)
and requires the API gateway URL format for scoped service-account tokens
([guidance](https://support.atlassian.com/atlassian-cloud/kb/401-unauthorized-error-when-service-account-accesses-jira-or-confluence-api/)).

Agent scripts can use the same token. A script that calls Jira directly uses
Bearer auth and the API gateway URL; alternatively, a script can call
`fullsend issues` and let Fullsend load the connection from config. The
explicit runner-secret handoff must make the token available only to scripts
that request it. The built-in agent credential provider must be updated to
support the same scoped-token flow without exposing the token to the model, and
its egress profile must allow `api.atlassian.com`.

Direct agent scripts can use the same token and gateway URL. Alternatively,
they can call `fullsend issues` and let Fullsend load connection settings from
config. In either case, `api_url` must be a compatible API gateway base URL.

## Poll query composition

The project scope is always applied by Fullsend. For a configured project
`PROJ` and query
`labels = "fullsend" AND statusCategory != Done ORDER BY updated DESC`, the
request uses:

```text
project = PROJ AND (labels = "fullsend" AND statusCategory != Done) ORDER BY updated DESC
```

The parentheses preserve the configured predicate as a unit. If the query has
a final `ORDER BY` clause, that clause remains after the closing parenthesis.
The poll command reads URL, project, enabled state, and query from config. Its
remaining command-line inputs are operational values such as `--target-repo`,
`--output`, and `--fullsend-dir`; `FULLSEND_JIRA_TOKEN` is the Jira credential.

## Implementation direction

Use one scoped Jira token and the API gateway for all Fullsend Jira access,
including the poller, `fullsend issues`, run-status updates, reconciliation,
and agent scripts. Send `FULLSEND_JIRA_TOKEN` as a Bearer token; do not require
or pass an account email.

Agent scripts should call `fullsend issues` when its command surface covers the
operation. For other Jira operations, scripts may call the REST API directly
using the same token and the configured `api_url`.

Discover and cache the Jira Cloud ID from `base_url`'s `/_edge/tenant_info`
endpoint. `cloud_id` is not a config field in this design. `base_url` remains
the site URL for issue links and Cloud ID lookup; `api_url` is the gateway base
for authenticated REST calls.

This remains a design proposal; implementation details and compatibility
behavior should be settled in the linked issues before code changes land.

## Proposed issue breakdown

The Jira work belongs under umbrella issue
[#8906](https://github.com/fullsend-ai/fullsend/issues/8906). Existing child
[#9035](https://github.com/fullsend-ai/fullsend/issues/9035) is the config
structure issue and remains part of the work. The following are proposed
follow-up issues, not yet filed; each should link to #8906 and reference #9035
where it depends on the config contract.

### Existing: #9035 — define the Jira tracker configuration

- Preserve scalar `tracker: github` and `tracker: gitlab`.
- Add `tracker.jira` fields for required `base_url` and `project`, optional
  `api_url` with the `https://api.atlassian.com` default, and `poll.enabled` /
  `poll.query`.
- Treat `project` as the sole Jira project for the repository, used by polling
  and Jira issue commands.
- Define `poll.enabled: true` to install the Jira poller through
  `fullsend repos install`, `false` to remove its managed pieces, and a
  missing `poll` or `poll.enabled` value to leave the poll workflow uninstalled.
- When `poll.query` is omitted, use the existing filter:
  `statusCategory != Done ORDER BY updated DESC`.
- Support the structure under `.fullsend/config.yaml` and `repos.yaml` managed
  config, with defined field-wise inheritance for Jira settings.
- Keep tokens and emails out of configuration.

### Proposed: install or remove Jira polling pieces from repo config

- Have `fullsend repos install` install the Jira poller workflow and its
  required support files when `tracker.jira.poll.enabled` is `true`.
- Remove the Jira poller pieces managed by Fullsend when it is `false`.
- Leave the poll workflow uninstalled when `poll` or `poll.enabled` is absent
  from the effective config.
- Require `--jira-token` on the first enabled install when the repo's
  `FULLSEND_JIRA_TOKEN` secret is absent; write it as that repo secret and
  reuse it on later installs when the flag is omitted.
- Never persist the token to config or print it.
- Keep the operation idempotent and leave unrelated repository files alone.
- Link this issue to #8906 and #9035; the schema issue defines the setting this
  behavior consumes.

### Proposed: support scoped Jira tokens in the shared Jira client

- Add API-gateway URL construction using `api_url` and the Jira Cloud ID.
- Resolve and cache Cloud ID from the configured `base_url` tenant-info
  endpoint.
- Send `FULLSEND_JIRA_TOKEN` as a Bearer token for gateway calls.
- Preserve `base_url` for browse links and validate both configured URLs.
- Cover the error paths for Cloud ID lookup, invalid URLs, and API failures.

### Proposed: configure Jira polling from tracker config

- Load `tracker.jira` settings instead of Jira URL, project, and JQL flags or
  environment fallbacks.
- Read the credential from `FULLSEND_JIRA_TOKEN` only.
- Honor `poll.enabled` and use the configured project and query.
- Always compose `project = PROJ AND (query)` and leave a final `ORDER BY`
  clause outside the parentheses.
- Keep operational poll arguments such as target repository and output path.

### Proposed: migrate `fullsend issues` and run-status Jira clients

- Use the shared config and scoped-token client for `fullsend issues get`,
  `create`, `post-comment`, and `link`.
- Use the required `tracker.jira.project` as the only Jira project for the
  repository's Jira issue commands; do not treat it as a fallback or expose a
  per-command Jira project override.
- Migrate `fullsend run` Jira status updates and `reconcile-status` to config
  plus `FULLSEND_JIRA_TOKEN`.
- Remove or deprecate the Jira email/URL flags and legacy environment names
  consistently.

### Proposed: migrate agent Jira access and secret delivery

- Update the built-in `atlassian-cloud` credential provider for scoped Bearer
  credentials and add `api.atlassian.com` to its network profile.
- Update reusable workflow secret declarations and token threading.
- Update direct script examples to use the same token-only gateway path, or
  show `fullsend issues` as the config-aware option.
- Update the Jira integration, issue-command, custom-poller, BYO-agent,
  harness-reference, operations, and config-reference documentation.
- Preserve explicit secret selection so a Jira token is not inherited by every
  host-side script or exposed to the sandboxed model.

## Open questions

- How should a non-default `api_url` override affect the gateway path and
  Cloud ID discovery?

## Secret delivery requirement

- Keep the repository secret named `FULLSEND_JIRA_TOKEN`.
- Fullsend-managed workflows may pass that token only to host-side scripts
  explicitly configured to need Jira access. Since the runner-secret contract
  reserves the `FULLSEND_` namespace, expose the token to a selected script
  under a non-reserved environment name, such as `JIRA_API_TOKEN`, rather than
  through the generic runner-secret bundle.
- Do not make the token available by default to other scripts or to the
  sandboxed model.

## References

- [Jira integration guide](../guides/user/jira-integration.md)
- [`fullsend issues` guide](../guides/user/issues-commands.md)
- [Config reference](../reference/config-reference.md)
- [ADR 0063: Polling-Based Work Discovery](../ADRs/0063-polling-based-work-discovery.md)
- [ADR 0093: Tracker-Routed Status Notifications](../ADRs/0093-tracker-routed-status-notifications.md)
