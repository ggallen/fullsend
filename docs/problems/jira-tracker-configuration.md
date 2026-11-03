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
`FULLSEND_JIRA_TOKEN`; later runs reuse that secret when the flag is omitted
and replace it when a new token is supplied. The token is not written to
either config file or printed in command output.

GitHub and GitLab keep their current scalar form (`tracker: github` and
`tracker: gitlab`). Credentials stay out of `config.yaml` and `repos.yaml`.
The Jira token and any account email are runtime credentials, not config
fields. The optional `FULLSEND_JIRA_EMAIL` behavior is described below.

When this mapping is used in `repos.yaml`, it belongs under the existing
managed `config` key. Layered configuration should allow an installation-level
Jira site and API URL to be combined with a repository-level project and poll
query. A child scalar tracker selection replaces the inherited Jira mapping.

## Authentication and URL roles

The initial service-account path uses a scoped API token from the
`FULLSEND_JIRA_TOKEN` environment variable. Fullsend-managed Jira API calls,
including polling, issue commands, run-status updates, and reconciliation,
use Bearer authentication through `api_url`; this path does not require or
pass an account email. Atlassian documents this
[service-account token flow](https://support.atlassian.com/user-management/docs/manage-api-tokens-for-service-accounts/):

```text
{api_url}/ex/jira/{cloudId}/rest/api/3/{resource}
```

`base_url` identifies the Jira site, supports issue browse links, and is used
to resolve the Cloud ID at `${base_url}/_edge/tenant_info`. Atlassian documents
this [Cloud ID lookup](https://support.atlassian.com/jira/kb/retrieve-my-atlassian-sites-cloud-id/)
and requires the API gateway URL format for scoped service-account tokens
([guidance](https://support.atlassian.com/atlassian-cloud/kb/401-unauthorized-error-when-service-account-accesses-jira-or-confluence-api/)).

`api_url` is the API gateway base URL. Fullsend appends the standard
`/ex/jira/{cloudId}/rest/api/3/{resource}` path to it. An override replaces
only the gateway base and must be a compatible gateway or proxy that supports
that path. Cloud ID discovery always uses `base_url`, regardless of the
`api_url` override.

The initial choice is a service account because it is a non-person identity
intended for services and integrations, and its scoped token supports the
token-only Bearer path. A regular Atlassian user account designated as a bot
uses the account-token flow documented with Basic authentication and an email
plus token ([regular account token docs](https://support.atlassian.com/atlassian-account/docs/manage-api-tokens-for-your-atlassian-account/)).
If bot-user auth is supported, an optional `FULLSEND_JIRA_EMAIL` runtime
value can select that Basic-auth mode: when set, use the email and
`FULLSEND_JIRA_TOKEN`; when absent, use Bearer auth and require a
service-account token. This selects the auth mode, not the account type; a
service-account token can also use Basic auth. The email remains outside
`config.yaml` and `repos.yaml`.

Service-account tokens also need rotation. Atlassian sets their expiration to
one year by default and allows an expiration from 1 to 365 days. User API
tokens also default to one year; organization policy can set their expiration
from 1 to 365 days or to never expire. A bot account may have a shorter
policy, but it is not inherently subject to more frequent rotation. The
service-account choice is about identity and token-only Bearer
authentication, not avoiding rotation. Before a token expires, an operator
must create a replacement and update the repository's `FULLSEND_JIRA_TOKEN`
secret. Supplying `--jira-token` on a later `fullsend repos install` replaces
the stored secret; omitting it reuses the existing secret. Revoke the old
token after the replacement is active.

Atlassian documents [service-account token expiry](https://support.atlassian.com/user-management/docs/manage-api-tokens-for-service-accounts/)
and [user API token expiration policies](https://support.atlassian.com/security-and-access-policies/docs/authentication-policy-settings-for-your-organizations/).

Agent scripts can use the same token and, when configured, the email for Basic
auth. A script that calls Jira directly uses the selected auth mode and API
gateway URL; alternatively, a script can call `fullsend issues` and let
Fullsend load the connection from config. The explicit runner-secret handoff
must make the token and optional email available only to scripts that request
them. The built-in agent credential provider must support the selected
scoped-token flow without exposing credentials to the model, and its egress
profile must allow `api.atlassian.com`.

Direct agent scripts use the selected auth mode, the same token, and the
gateway URL. In Basic mode they also need the configured runtime email.
Alternatively, they can call `fullsend issues` and let Fullsend load connection
settings from config. In either case, `api_url` must be a compatible API
gateway base URL.

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
`--output`, and `--fullsend-dir`. `FULLSEND_JIRA_TOKEN` is required. If
bot-user auth is supported and `FULLSEND_JIRA_EMAIL` is set, the poller uses
Basic auth; otherwise it uses Bearer auth with a service-account token.

## Implementation direction

Use one scoped Jira token and the API gateway for all Fullsend Jira access,
including the poller, `fullsend issues`, run-status updates, reconciliation,
and agent scripts. If bot-user auth is supported, use
`FULLSEND_JIRA_EMAIL` to select Basic auth with that email and
`FULLSEND_JIRA_TOKEN`; when it is absent, use Bearer auth and require a
service-account token. Neither the token nor email is stored in config files.

Agent scripts should call `fullsend issues` when its command surface covers the
operation. For other Jira operations, scripts may call the REST API directly
using the same credentials and the configured `api_url`.

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
  `FULLSEND_JIRA_TOKEN` secret is absent; write it as that repo secret.
- Replace the stored token when `--jira-token` is supplied on a later install,
  so an operator can rotate an expiring token; reuse the stored token when the
  flag is omitted.
- If bot-user auth is supported, thread optional `FULLSEND_JIRA_EMAIL` from
  the repository runtime into Fullsend-managed Jira workflows when set; keep
  it out of config files.
- Never persist the token to config or print it.
- Keep the operation idempotent and leave unrelated repository files alone.
- Link this issue to #8906 and #9035; the schema issue defines the setting this
  behavior consumes.

### Proposed: support scoped Jira tokens in the shared Jira client

- Add API-gateway URL construction using `api_url` and the Jira Cloud ID.
- Resolve and cache Cloud ID from the configured `base_url` tenant-info
  endpoint.
- Use Bearer auth with `FULLSEND_JIRA_TOKEN` when `FULLSEND_JIRA_EMAIL` is
  absent. If bot-user auth is supported, use Basic auth with the email and
  token when it is set.
- Preserve `base_url` for browse links and validate both configured URLs.
- Cover the error paths for Cloud ID lookup, invalid URLs, and API failures.

### Proposed: configure Jira polling from tracker config

- Load `tracker.jira` settings instead of Jira URL, project, and JQL flags or
  environment fallbacks.
- Read `FULLSEND_JIRA_TOKEN` from the runtime environment. If bot-user auth is
  supported, use `FULLSEND_JIRA_EMAIL` when set to select Basic authentication;
  otherwise use Bearer auth with a service-account token.
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
  plus `FULLSEND_JIRA_TOKEN`. If bot-user auth is supported, also use the
  optional `FULLSEND_JIRA_EMAIL` auth-mode selector.
- Remove or deprecate the Jira email/URL flags and legacy environment names
  consistently.

### Proposed: migrate agent Jira access and secret delivery

- Update the built-in `atlassian-cloud` credential provider for the selected
  Jira auth mode and add `api.atlassian.com` to its network profile.
- Update reusable workflow secret declarations and token threading.
- If bot-user auth is supported, thread optional `FULLSEND_JIRA_EMAIL` to Jira
  workflows and explicitly selected host-side scripts when it is set.
- Update direct script examples to use the selected gateway auth mode, or show
  `fullsend issues` as the config-aware option.
- Update the Jira integration, issue-command, custom-poller, BYO-agent,
  harness-reference, operations, and config-reference documentation.
- Preserve explicit secret selection so a Jira token is not inherited by every
  host-side script or exposed to the sandboxed model.

## Open question

- Should Fullsend support only scoped service-account tokens with Bearer auth,
  or also regular Jira bot-user tokens? If both are supported, the proposed
  selector is `FULLSEND_JIRA_EMAIL`: when set, use Basic auth with that email
  and `FULLSEND_JIRA_TOKEN`; when absent, use Bearer auth and require a
  service-account token. The email stays outside `config.yaml` and
  `repos.yaml` and must be supplied at runtime. This broadens compatibility
  while adding an auth mode and email delivery to workflows and selected
  scripts. The variable selects auth mode by convention; it does not verify
  account type, since service-account tokens can also use Basic auth.

## Secret delivery requirement

- Keep the repository secret named `FULLSEND_JIRA_TOKEN`.
- Fullsend-managed workflows may pass that token only to host-side scripts
  explicitly configured to need Jira access. Since the runner-secret contract
  reserves the `FULLSEND_` namespace, expose the token to a selected script
  under a non-reserved environment name, such as `JIRA_API_TOKEN`, rather than
  through the generic runner-secret bundle.
- If bot-user auth is supported, treat `FULLSEND_JIRA_EMAIL` as a
  Fullsend-managed runtime value and pass it only to Jira workflows and
  explicitly selected host-side scripts; do not route it through the generic
  runner-secret bundle.
- Do not make the token available by default to other scripts or to the
  sandboxed model.

## References

- [Jira integration guide](../guides/user/jira-integration.md)
- [`fullsend issues` guide](../guides/user/issues-commands.md)
- [Config reference](../reference/config-reference.md)
- [ADR 0063: Polling-Based Work Discovery](../ADRs/0063-polling-based-work-discovery.md)
- [ADR 0093: Tracker-Routed Status Notifications](../ADRs/0093-tracker-routed-status-notifications.md)
