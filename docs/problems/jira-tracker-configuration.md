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
- Cross-repository issue policy currently lives in top-level
  `create_issues.allow_targets`, with GitHub organization/repository entries
  and GitLab repository entries.
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
and Jira. Each provider mapping has an `allowed_targets` mapping with fields
appropriate to that provider: GitHub supports organizations and repositories,
GitLab supports groups and repositories, and Jira supports projects. Two prep
issues below cover migrating the existing configuration and replacing
`create_issues` before the Jira implementation work.

```yaml
tracking:
  jira:
    base_url: https://example.atlassian.net
    # Optional; defaults to https://api.atlassian.com
    api_url: https://api.atlassian.com
    project: PROJ
    allowed_targets:
      projects:
        - PROJ
    poll:
      enabled: true
      query: 'labels = "fullsend" AND statusCategory != Done ORDER BY updated DESC'
```

Exactly one provider key (`github`, `gitlab`, or `jira`) selects the repository's
tracker. GitHub and GitLab can initially use empty mappings; Jira has
provider-specific settings. The selected provider's `allowed_targets` mapping
is nested in that block. GitHub uses `orgs` and `repos`; GitLab uses `groups`
and `repos`; Jira uses `projects`. Repository values use GitHub `owner/repo`
or GitLab full project paths, while Jira values are project keys.

For a GitHub repository, targets can include both organizations and individual
repositories:

```yaml
tracking:
  github:
    allowed_targets:
      orgs: [fullsend-ai]
      repos: [another-org/special-repo]
```

For a GitLab repository, targets can include both groups and individual
repositories:

```yaml
tracking:
  gitlab:
    allowed_targets:
      groups: [fullsend/team]
      repos: [fullsend/other/team-repo]
```

These scopes are additive: a destination is allowed when it matches either a
configured org/group or a configured repository. Jira uses only
`tracking.jira.allowed_targets.projects`, as shown in the Jira configuration
above.

`base_url` and `project` are required for the Jira mapping. `api_url` is
optional and defaults to Atlassian's API gateway. `project` identifies the
repository's primary Jira project and is used for polling and authorization.
`allowed_targets.projects` is a list of Jira project keys where agents may
create or move issues. Polling is controlled by `poll.enabled`; `poll.query`
supplies the JQL filter. When `poll.enabled` is `true`, `fullsend repos install`
installs the Jira polling pieces. When it is `false`, `repos install` removes
the Jira polling pieces it manages. If `poll` or `poll.enabled` is missing
from the effective config, the poll workflow is not installed. If `poll.query`
is omitted, polling uses the current default filter,
`statusCategory != Done ORDER BY updated DESC`, scoped to the configured
project.

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
under `tracking`. GitHub uses `tracking.github.allowed_targets.orgs` and
`.repos`; GitLab uses `tracking.gitlab.allowed_targets.groups` and `.repos`;
Jira uses `tracking.jira.allowed_targets.projects`. Credentials stay out of
`config.yaml` and `repos.yaml`; Jira authentication uses runtime repo secrets.

When this mapping is used in `repos.yaml`, it belongs under the existing
managed `config` key. Layered configuration should allow an installation-level
Jira site and API URL to be combined with a repository-level project and poll
query. A child provider selection replaces the inherited provider selection;
the selected provider's `allowed_targets` retains replace-if-set behavior.

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

## Issue breakdown

All work is tracked under parent issue
[#8906](https://github.com/fullsend-ai/fullsend/issues/8906). Its eight
subissues are filed and linked in GitHub. The parent records the implementation
order and parallel work lanes; blocker relationships are set on the issues.

### Prep: [#9074](https://github.com/fullsend-ai/fullsend/issues/9074)

- Replace the top-level `create_issues` config block with
  `tracking.<tracker>.allowed_targets`.
- Use `tracking.github.allowed_targets.orgs` and `.repos`,
  `tracking.gitlab.allowed_targets.groups` and `.repos`, and
  `tracking.jira.allowed_targets.projects`.
- Update config parsing, validation, layering, effective-config output,
  managed-config writeback, safety checks, agent/workflow consumers, examples,
  and documentation.
- Preserve existing defaults and same-repository behavior.

### Prep: [#9075](https://github.com/fullsend-ai/fullsend/issues/9075)

- Migrate the scalar `tracker` and legacy target configuration to the selected
  provider mapping under `tracking` when `repos install` converges managed
  `.fullsend/config.yaml` files.
- Preserve effective tracker and target policy, and leave unrelated user
  configuration alone.
- Preserve legacy GitHub organization grants as `github.allowed_targets.orgs`
  and repository grants as `github.allowed_targets.repos`; migrate GitLab
  repository grants to `gitlab.allowed_targets.repos`. GitLab groups can be
  configured through `gitlab.allowed_targets.groups`. Jira project keys belong
  in `jira.allowed_targets.projects`; do not infer Jira project keys from
  repository paths.
- Blocked by #9074; complete before #9035.

### Jira config: [#9035](https://github.com/fullsend-ai/fullsend/issues/9035)

- Define required `base_url` and `project`, optional `api_url` defaulting to
  `https://api.atlassian.com`, `allowed_targets.projects`, and poll settings.
- Keep the configured project as the repository's primary project for polling
  and authorization; restrict cross-project create/move destinations to
  `allowed_targets.projects`.
- Preserve the required JQL grouping:
  `project = PROJ AND (query) ORDER BY ...`.
- Blocked by #9075.

### Shared Jira auth/client: [#9007](https://github.com/fullsend-ai/fullsend/issues/9007)

- Discover Cloud ID from `base_url`; use `api_url` for gateway REST requests and
  keep `base_url` for browse links.
- Use Bearer auth with `FULLSEND_JIRA_TOKEN` when `FULLSEND_JIRA_EMAIL` is
  absent; use Basic auth with the email and scoped user token when present.
- This existing issue is now a subissue of #8906 and is blocked by #9035.

### Poll workflow and secrets: [#9076](https://github.com/fullsend-ai/fullsend/issues/9076)

- Install or remove managed polling files based on `tracking.jira.poll.enabled`;
  leave them uninstalled when the setting is absent.
- Require and store `FULLSEND_JIRA_TOKEN` on first Jira install; support token
  replacement and optional `FULLSEND_JIRA_EMAIL` storage.
- Blocked by #9035; can proceed in parallel with #9007.

### Poll command: [#9077](https://github.com/fullsend-ai/fullsend/issues/9077)

- Read site, API URL, project, enabled state, and JQL from `tracking.jira`;
  require the runtime token and use the optional email auth selector.
- Compose `project = PROJ AND (query)` and keep `ORDER BY` after the closing
  parenthesis.
- Blocked by #9035 and #9007.

### Issue/status clients: [#9078](https://github.com/fullsend-ai/fullsend/issues/9078)

- Migrate `fullsend issues`, Jira run-status updates, and `reconcile-status` to
  the shared client and configured project.
- Blocked by #9035 and #9007.

### Agent access: [#9079](https://github.com/fullsend-ai/fullsend/issues/9079)

- Pass Jira secrets only to host-side workflows and scripts that need them;
  pass `FULLSEND_JIRA_EMAIL` only when it exists.
- Update the built-in Jira credential provider, workflow secret threading,
  egress, direct-script examples, and related documentation.
- Blocked by #9007 and #9076; may proceed alongside #9077 and #9078.

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

None currently. GitHub organization grants remain organization grants under
`tracking.github.allowed_targets.orgs`, so migration does not need to expand
them into a snapshot of repositories.

## References

- [Jira integration guide](../guides/user/jira-integration.md)
- [`fullsend issues` guide](../guides/user/issues-commands.md)
- [Config reference](../reference/config-reference.md)
- [ADR 0063: Polling-Based Work Discovery](../ADRs/0063-polling-based-work-discovery.md)
- [ADR 0093: Tracker-Routed Status Notifications](../ADRs/0093-tracker-routed-status-notifications.md)
