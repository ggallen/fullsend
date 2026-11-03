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
  `JIRA_USER_EMAIL` with `JIRA_TOKEN`; the proposed design replaces these
  legacy inputs with the repo token secret and optional email secret.
- `fullsend issues` reads the default tracker from config. Jira URL, email, and
  token still come from flags or `JIRA_BASE_URL`, `JIRA_USER_EMAIL`, and
  `JIRA_TOKEN`. The project is currently required on each command.
- `fullsend run` status notifications and `reconcile-status` construct Jira
  clients from those same three environment variables.
- The built-in `atlassian-cloud` provider and network profile are documented
  for email-plus-token access to `*.atlassian.net`. The custom host-side script
  example also uses Basic auth against the Jira site URL.

## Proposed configuration shape

The new `tracking` key will use provider-specific mappings for GitHub, GitLab,
and Jira. The existing `create_issues` settings will move inside the selected
provider mapping, so tracker settings and issue-target policy live together.
Two prep issues below cover migration and updating the references before the
Jira implementation work.

```yaml
tracking:
  jira:
    base_url: https://example.atlassian.net
    # Optional; defaults to https://api.atlassian.com
    api_url: https://api.atlassian.com
    project: PROJ
    poll:
      enabled: true
      query: 'labels = "fullsend" AND statusCategory != Done ORDER BY updated DESC'
```

Exactly one provider key (`github`, `gitlab`, or `jira`) selects the repository's
tracker. GitHub and GitLab can initially use empty mappings; Jira has
provider-specific settings. The selected provider's `create_issues` settings
are nested in that mapping, for example `tracking.jira.create_issues`. The prep
work moves existing settings there while preserving their current behavior and
layering.

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

For bot-user authentication, optionally pass `--jira-email` to
`fullsend repos install`. Install stores it as the repo secret
`FULLSEND_JIRA_EMAIL`; later runs reuse that secret when the flag is omitted
and replace it when a new email is supplied. If the secret is absent, Fullsend
uses the service-account authentication path. Remove the repo secret to switch
an existing installation back to service-account auth. The email is never
written to either config file or printed in command output.

GitHub and GitLab will move from scalar values to provider-specific mappings
under `tracking`. Their issue-target settings will live at
`tracking.github.create_issues` or `tracking.gitlab.create_issues`; Jira's will
live at `tracking.jira.create_issues`. Credentials stay out of `config.yaml`
and `repos.yaml`; Jira authentication uses runtime repo secrets instead.

When this mapping is used in `repos.yaml`, it belongs under the existing
managed `config` key. Layered configuration should allow an installation-level
Jira site and API URL to be combined with a repository-level project and poll
query. A child provider selection replaces the inherited provider selection;
the selected provider's `create_issues` retains its existing replace-if-set
behavior.

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

Both authentication modes use the scoped API gateway URL above. The email
secret changes the authorization header: bot-user mode sends Basic
authentication with the email and token; service-account mode sends the token
as Bearer. `base_url` remains for browse links and Cloud ID discovery rather
than REST API requests.

The service-account path is the default because a service account is a
non-person identity intended for services and integrations, and its scoped
token supports Bearer auth without an email. For a regular Atlassian user
account designated as a bot, install can store its email as the optional
`FULLSEND_JIRA_EMAIL` repo secret. When that secret exists, Fullsend uses Basic
authentication with the email and a scoped user API token. When it is absent,
Fullsend uses Bearer authentication with a service-account token. Atlassian
documents the [scoped user-token flow](https://support.atlassian.com/atlassian-account/docs/manage-api-tokens-for-your-atlassian-account/).

The email secret explicitly selects the authentication mode; install cannot
infer bot versus service-account identity from the token string. Atlassian
[notes that token types cannot be distinguished from the token alone](https://support.atlassian.com/atlassian-cloud/kb/401-unauthorized-error-when-service-account-accesses-jira-or-confluence-api/).
A service-account token can also use Basic auth, so the email secret expresses
the chosen mode rather than verifying the account type. The email remains
outside `config.yaml` and `repos.yaml`.

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

Agent scripts can use the same token and, in bot-user mode, the email for
Basic auth. A script that calls Jira directly uses the selected auth mode and
API gateway URL; alternatively, a script can call `fullsend issues` and let
Fullsend load the connection from config. Pass the token only to host-side
scripts that need Jira access. Pass the email to all agent pre/post scripts
only when the `FULLSEND_JIRA_EMAIL` repo secret exists. The built-in agent
credential provider must support both scoped-token modes without exposing
credentials to the model, and its egress profile must allow `api.atlassian.com`.

Direct agent scripts use the selected auth mode, token, and gateway URL. In
bot-user mode they also receive the email when the repo secret exists.
Alternatively, they can call `fullsend issues` and let Fullsend load connection
settings from config. In either mode, `api_url` must be a compatible API
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
`--output`, and `--fullsend-dir`. `FULLSEND_JIRA_TOKEN` is required. When
`FULLSEND_JIRA_EMAIL` exists, the poller uses Basic auth with the scoped
bot-user token; otherwise it uses Bearer auth with the service-account token.

## Implementation direction

Use one scoped Jira token and the API gateway for all Fullsend Jira access,
including the poller, `fullsend issues`, run-status updates, reconciliation,
and agent scripts. When `FULLSEND_JIRA_EMAIL` exists, use Basic auth with that
email and `FULLSEND_JIRA_TOKEN`; when it is absent, use Bearer auth and require
a service-account token. Neither the token nor email is stored in config
files.

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
[#9035](https://github.com/fullsend-ai/fullsend/issues/9035) defines Jira
configuration, but needs to be updated to use the new `tracking` schema. The
two proposed prep issues below should be completed first. They are not filed;
the follow-up proposals are also unfiled. New issues should link to #8906 and
reference #9035 where they depend on the config contract.

### Proposed prep: migrate managed repository config to `tracking` via `repos install`

- Update `fullsend repos install` to migrate existing managed config from the
  scalar `tracker` key to the provider-key structure under `tracking`.
- Preserve the selected GitHub or GitLab tracker and existing settings when
  migrating. For Jira configuration, preserve the Jira values while moving
  them to `tracking.jira`.
- Migrate existing `create_issues` config to
  `tracking.<tracker>.create_issues` for the repository's selected tracker.
- Treat the updated `repos.yaml` managed `config` as the desired input, then
  have `repos install` migrate or regenerate each repository's managed
  `.fullsend/config.yaml` with the new `tracking` structure.
- Preserve current effective behavior and the selected tracker's
  `create_issues.allow_targets` policy; leave unrelated user config alone and
  make the migration repeatable. Resolve the destination from the effective
  tracker, falling back to the repository's forge provider when no tracker is
  explicit, so repositories without a tracker setting retain their policy.
- Complete this prep work before #9035 and the Jira-specific follow-up work.

### Proposed prep: move all `create_issues` references under each tracking provider

- Move the config field to `tracking.<tracker>.create_issues` and update config
  parsing, validation, layering, effective-config output, managed-config
  writeback, and safety checks to use the provider-specific path.
- Update every code, workflow, agent, example, and documentation reference to
  `create_issues` so configuration paths use
  `tracking.github.create_issues`, `tracking.gitlab.create_issues`, or
  `tracking.jira.create_issues` as appropriate.
- Preserve existing `allow_targets` behavior and defaults during the move.
- Complete this prep work before #9035 and the Jira-specific follow-up work.

### Existing: #9035 — define the Jira tracker configuration

- Amend the issue to define `tracking.jira` within the new `tracking` schema,
  after the prep issues above.
- Add `tracking.jira` fields for required `base_url` and `project`, optional
  `api_url` with the `https://api.atlassian.com` default, and `poll.enabled` /
  `poll.query`.
- Represent GitHub and GitLab as provider keys under `tracking`, replacing the
  current scalar `tracker` value.
- Place Jira's `create_issues` settings inside `tracking.jira`.
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
  required support files when `tracking.jira.poll.enabled` is `true`.
- Remove the Jira poller pieces managed by Fullsend when it is `false`.
- Leave the poll workflow uninstalled when `poll` or `poll.enabled` is absent
  from the effective config.
- Require `--jira-token` on the first enabled install when the repo's
  `FULLSEND_JIRA_TOKEN` secret is absent; write it as that repo secret.
- Replace the stored token when `--jira-token` is supplied on a later install,
  so an operator can rotate an expiring token; reuse the stored token when the
  flag is omitted.
- Store `FULLSEND_JIRA_EMAIL` as a repo secret when `--jira-email` is supplied;
  reuse it when the flag is omitted and remove it to switch to service-account
  auth.
- Never persist the token to config or print it.
- Keep the operation idempotent and leave unrelated repository files alone.
- Link this issue to #8906 and #9035; the schema issue defines the setting this
  behavior consumes.

### Proposed: support scoped Jira tokens in the shared Jira client

- Add API-gateway URL construction using `api_url` and the Jira Cloud ID.
- Resolve and cache Cloud ID from the configured `base_url` tenant-info
  endpoint.
- Use Bearer auth with `FULLSEND_JIRA_TOKEN` when `FULLSEND_JIRA_EMAIL` is
  absent. When it exists, use Basic auth with the email and scoped token. Both
  modes use the scoped API gateway URL.
- Preserve `base_url` for browse links and validate both configured URLs.
- Cover the error paths for Cloud ID lookup, invalid URLs, and API failures.

### Proposed: configure Jira polling from tracking config

- Load `tracking.jira` settings instead of Jira URL, project, and JQL flags or
  environment fallbacks.
- Read `FULLSEND_JIRA_TOKEN` from the runtime environment. When
  `FULLSEND_JIRA_EMAIL` exists, use Basic authentication with the bot-user
  token; otherwise use Bearer authentication with a service-account token.
- Honor `poll.enabled` and use the configured project and query.
- Always compose `project = PROJ AND (query)` and leave a final `ORDER BY`
  clause outside the parentheses.
- Keep operational poll arguments such as target repository and output path.

### Proposed: migrate `fullsend issues` and run-status Jira clients

- Use the shared config and scoped-token client for `fullsend issues get`,
  `create`, `post-comment`, and `link`.
- Use the required `tracking.jira.project` as the only Jira project for the
  repository's Jira issue commands; do not treat it as a fallback or expose a
  per-command Jira project override.
- Migrate `fullsend run` Jira status updates and `reconcile-status` to config
  plus `FULLSEND_JIRA_TOKEN` and the optional `FULLSEND_JIRA_EMAIL` auth-mode
  selector.
- Remove or deprecate legacy Jira email/URL flags and environment names.
- Keep `--jira-email` as an optional `fullsend repos install` input for setting
  the `FULLSEND_JIRA_EMAIL` repo secret.

### Proposed: migrate agent Jira access and secret delivery

- Update the built-in `atlassian-cloud` credential provider for the selected
  Jira auth mode and add `api.atlassian.com` to its network profile.
- Update reusable workflow secret declarations and token threading.
- Pass `FULLSEND_JIRA_EMAIL` to Jira workflows only when the repo secret exists.
- Pass the email to all agent pre/post scripts only when the secret exists.
- Update direct script examples to use the selected gateway auth mode, or show
  `fullsend issues` as the config-aware option.
- Update the Jira integration, issue-command, custom-poller, BYO-agent,
  harness-reference, operations, and config-reference documentation.
- Preserve explicit secret selection so a Jira token is not inherited by every
  host-side script or exposed to the sandboxed model.

## Secret delivery requirement

- Keep the repository secret named `FULLSEND_JIRA_TOKEN`.
- Store the optional bot email as the repo secret `FULLSEND_JIRA_EMAIL` only
  when `--jira-email` is supplied. Jira workflows receive the email secret
  only when it exists; do not pass an empty value when it is absent.
- Pass the token only to Jira workflows and host-side scripts that need Jira
  access. Pass the email to all agent pre/post scripts only when its repo
  secret exists. Expose these values to scripts under non-reserved names such
  as `JIRA_API_TOKEN` and `JIRA_EMAIL`; do not use the generic runner-secret
  bundle for `FULLSEND_` names.
- Do not expose either credential to the sandboxed model.

## Open questions

- Should the initial Jira implementation support both service-account and
  bot-user tokens, or ship service-account support first and add bot-user
  support only if needed? The proposed contract defaults to service-account
  Bearer auth; when bot-user support is enabled, the optional
  `FULLSEND_JIRA_EMAIL` secret selects Basic auth.

## References

- [Jira integration guide](../guides/user/jira-integration.md)
- [`fullsend issues` guide](../guides/user/issues-commands.md)
- [Config reference](../reference/config-reference.md)
- [ADR 0063: Polling-Based Work Discovery](../ADRs/0063-polling-based-work-discovery.md)
- [ADR 0093: Tracker-Routed Status Notifications](../ADRs/0093-tracker-routed-status-notifications.md)
