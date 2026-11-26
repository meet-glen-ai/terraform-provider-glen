# Terraform provider for Glen

[Glen](https://meetglen.ai) runs teams of background agents for your org
and turns every run into knowledge. This provider keeps your Glen org as
code: each agent is a `glen_agent`, each knowledge base a
`glen_knowledge_base`, each feed its agents start on a `glen_feed`, each
of the org's tools a `glen_tool`, each of its
people a `glen_member`, each of its own roles a `glen_role`, the org's
model keys `glen_model_key`, and its
auto refill `glen_auto_refill`.
`terraform plan` shows what would change, and `terraform apply` changes
it through Glen's admin API.

## Requirements

- Terraform 1.11 or later. The write-only `credential_wo` and `key_wo`
  arguments need it.
- A personal API token of a Glen admin. Sign in to the console as a
  member with the `admin` role, open Settings, and make one under API
  tokens. Glen shows it once. The provider acts as that admin, so Glen
  records each change under their email. The token stops working when
  they revoke it or are no longer an admin.

## Install and configure

```hcl
terraform {
  required_providers {
    glen = {
      source  = "meet-glen-ai/glen"
      version = "~> 0.1"
    }
  }
}

provider "glen" {
  url = "https://app.meetglen.ai"
  # api_key comes from GLEN_API_KEY, kept out of the file.
}
```

Run `terraform init` to install the provider from the Terraform registry.

| Setting | Environment variable | Notes |
|---|---|---|
| `url` | `GLEN_URL` | Glen's API, as the console's Settings shows it under API and Terraform. |
| `api_key` | `GLEN_API_KEY` | An admin's personal API token, `glenpat_...`. Sensitive. |

Terraform does not save provider settings in state, so the token can come
from an ephemeral value, such as a secret manager's:

```hcl
ephemeral "aws_secretsmanager_secret_version" "glen_token" {
  secret_id = "glen/terraform-token"
}

provider "glen" {
  url     = "https://app.meetglen.ai"
  api_key = ephemeral.aws_secretsmanager_secret_version.glen_token.secret_string
}
```

Agents also reach Glen through its API and the console's forms. The
provider marks every write it makes as Terraform's, and the console warns
before an edit that the next apply would undo. Each agent's and knowledge
base's page in the console shows its resource, ready to copy.

## Define an agent

```hcl
resource "glen_agent" "alert_triage" {
  name         = "alert-triage"
  instructions = file("${path.module}/alert-triage.md")
  model        = "claude-opus-5-5"

  trigger {
    schedule = "0 */6 * * *"
  }

  sandbox {
    egress     = ["api.github.com", "*.githubusercontent.com"]
    web_search = true
  }

  budget {
    sessions_per_day = 200
  }

  notify {
    email {
      to = ["ann@example.com"]
      on = ["failed", "needs_you"]
    }
  }
}
```

| Argument | Notes |
|---|---|
| `name` | Required. Kebab-case. Changing it replaces the agent. |
| `instructions` | Required. The text, not a path; use `file()`. |
| `model` | Required. |
| `harness` | Optional. Unset, Glen picks it and the attribute holds Glen's choice: `claude-code` for a Claude model, and `codex` for an OpenAI model on a Glen that runs Codex. Set it only to pin one Glen would also pick. |
| `trigger` | Repeated block. Each sets exactly one of `schedule` (five-field cron, UTC), `feed` (a feed's name, usually `glen_feed.<name>.name`, or one the deployment polls) and `webhook` (`<source>:<event>`, such as `github:pull_request`). Messages need no trigger: any member can message any agent with their own token. |
| `tools` | Block. `mcp`: the tools the agent uses, a list of `{ server, as }`, where `server` is a tool's name, usually `glen_tool.<name>.name`, and `as` is `user`, `agent` or `service` (unset). See [Define a tool](#define-a-tool). |
| `sandbox` | Block. `image`: a container image for the sandbox. `egress`: the hosts the sandbox may connect to, besides Glen: a hostname such as `api.github.com`, or `*.` and a domain such as `*.githubusercontent.com`; unset, it reaches Glen only. `web_search`: the model provider's server-side web search, which runs at the provider, past the egress list; off when unset. |
| `budget` | Block. `tokens_per_session`, `sessions_per_day` and `max_turns`. |
| `notify` | Block. `email`, a block: `to`, required, the org members Glen emails, by their sign-in address, usually `glen_member.<name>.email`; and `on`, what it emails them on, each at most once: `finished` (a session settled done, with no ask open), `failed` (a session failed, not stopped by a budget) and `needs_you` (it asked, Glen held one of its calls, or a budget stopped it). Unset, Glen emails no one. Glen refuses an address that is not a member's. |
| `knowledge_base` | The knowledge base the agent reads, usually `glen_knowledge_base.<name>.name`. |
| `version` | Computed. The agent's version in Glen. |

`terraform plan` asks Glen to check each changed agent, so plan fails on
an agent Glen would refuse at apply, with Glen's message. A value that is
unknown until apply, such as an image from a repository made in the same
apply, skips that check, and Glen checks the agent at apply instead.

Each refresh reads the agent back from Glen. An agent changed some other
way shows as a change in the next plan, and an agent deleted in Glen shows
as one to create.

To manage an agent that already exists in Glen, import it by name:

```sh
terraform import glen_agent.alert_triage alert-triage
```

Creating an agent whose name Glen already has fails and asks you to import
it, so two configurations never overwrite each other's agent.

## Define a knowledge base

A knowledge base is the files that the agents which name it read, at
`/knowledge_base` in their sandboxes. Each knowledge base has one curator,
which Glen runs, and the curator is its only writer.

```hcl
resource "glen_knowledge_base" "secops" {
  name        = "secops"
  description = "What the security operations agents know about our hosts, alerts and people."
  guidance    = file("${path.module}/secops-knowledge-base.md")

  curator {
    model          = "claude-opus-5-5"
    tokens_per_day = 2000000
  }
}

resource "glen_agent" "alert_triage" {
  name           = "alert-triage"
  knowledge_base = glen_knowledge_base.secops.name
  # instructions, model, triggers, tools, as above
}
```

| Argument | Notes |
|---|---|
| `name` | Required. Kebab-case. Changing it replaces the knowledge base. Knowledge base names and agent names are separate. |
| `description` | Required, not blank. What the knowledge base is for. The console shows it and the curator reads it. |
| `guidance` | Optional. Your team's direction to the curator, such as "one page per host group". |
| `curator` | Required block. `model` and `tokens_per_day` (at least 1). The budget counts cache reads at a tenth of their tokens. |
| `version` | Computed. The knowledge base's version in Glen. |

A new knowledge base has no pages. Plan, refresh and import work as for
agents: `terraform import glen_knowledge_base.secops secops`.

At plan, Glen checks only that an agent's `knowledge_base` is a knowledge
base name, so the first plan of a new knowledge base and its agents works.
At apply, Glen refuses an agent whose knowledge base does not exist.
Terraform creates the knowledge base before the agents that reference it.

Glen refuses to delete a knowledge base while any agent names it, and the
apply fails with Glen's message naming those agents. `terraform destroy`
deletes the agents first, because they reference the knowledge base. An
agent that names the knowledge base as a plain string, or one made outside
this configuration, has no such reference, so change or delete it first.

## Define a feed

A feed is a document Glen polls for the org: an RSS feed, an OSV list or
CISA's known-exploited list. Each new entry is a message to every agent
whose trigger names the feed.

```hcl
resource "glen_feed" "zero_wire" {
  name     = "zero-wire"
  format   = "rss"
  url      = "https://radar.cloudflare.com/zero-wire/feed"
  baseline = false
}

resource "glen_agent" "intel" {
  name = "intel"
  trigger {
    feed = glen_feed.zero_wire.name
  }
  # instructions, model, tools, as above
}
```

| Argument | Notes |
|---|---|
| `name` | Required. Kebab-case. Changing it replaces the feed. It may not be a feed the deployment already polls. |
| `format` | Required. `rss` (an entry per item, keyed by its guid), `osv` (a JSON array of OSV records) or `cisa-kev`. |
| `url` | Required. https on a public address. Glen follows no redirect, so give the address the feed answers at. |
| `poll_secs` | Optional. Seconds between polls, 60 to 86400. Defaults to 3600. |
| `baseline` | Optional. Defaults to true: what the feed lists at its first poll is marked seen, so an old list does not start a session per entry. Set false to send every entry already listed. |
| `version` | Computed. The feed's version in Glen. |

Until an agent starts on a feed, Glen takes only its baseline and checks
every minute for one, so an agent created after its feed starts on it
within a minute. Glen
refuses to delete a feed while an agent's trigger names it. Terraform
destroys the agents first when their triggers reference
`glen_feed.<name>.name`; an agent that names the feed as a plain string,
or one made outside this configuration, still blocks the delete. Renaming
a feed replaces it, and the old one is deleted before the agents move to
the new name unless the feed sets `lifecycle { create_before_destroy =
true }`. Import works as for agents:
`terraform import glen_feed.zero_wire zero-wire`.

## Define a tool

A tool is something the org's agents use: an MCP server at a `url`, a
`sample` Glen answers itself, or a service the agents' CLIs call (`cli`).
The console shows the org's tools under Tools. An agent names a tool in
`tools.mcp`:

```hcl
resource "glen_tool" "tickets" {
  name = "tickets"
  url  = "https://tickets.example.com/mcp"
}

resource "glen_tool" "notion" {
  name = "notion"
  url  = "https://mcp.notion.com/mcp"

  # People and agents sign in to it in the console. Glen finds the
  # server's sign-in and registers itself there when the tool is created.
  connect {}
}

resource "glen_tool" "datadog" {
  name = "datadog"
  url  = "https://mcp.datadoghq.com/mcp"

  # Glen adds the key to every call an agent makes as the service.
  credential {
    kind   = "api-key"
    header = "DD-API-KEY"
  }
  credential_wo         = var.datadog_api_key # an ephemeral variable
  credential_wo_version = 1
}

resource "glen_tool" "github" {
  name = "github"

  cli {
    hosts = [
      { host = "api.github.com" },
      { host = "github.com", auth = "basic", user = "x-access-token" },
    ]
    allow = ["*.githubusercontent.com"]
    env   = { GH_TOKEN = "glen-proxy-injected" }
  }

  connect {
    provider = "github-oauth"
    scopes   = ["repo"]
  }
}

# The same service, read-only and one repository: the proxy sends only
# these requests with the token. On github.com it refuses the rest, a push
# included; on api.github.com it sends the rest with no token, so public
# reads still work.
resource "glen_tool" "app_source" {
  name = "app-source"

  cli {
    hosts = [
      { host = "api.github.com", others = "anonymous", requests = [
        { method = "GET", path = "/repos/acme/app" },
        { method = "GET", path = "/repos/acme/app/contents/**" },
        { method = "GET", path = "/repos/acme/app/commits/**" },
      ] },
      { host = "github.com", auth = "basic", user = "x-access-token", requests = [
        { method = "GET", path = "/acme/app.git/info/refs" },
        { method = "POST", path = "/acme/app.git/git-upload-pack" },
      ] },
    ]
    allow = ["*.githubusercontent.com", "codeload.github.com"]
    env   = { GH_TOKEN = "glen-proxy-injected" }
  }

  connect {
    provider = "github-oauth"
    scopes   = ["repo"]
  }
}

resource "glen_agent" "alert_triage" {
  name = "alert-triage"
  # instructions, model, triggers, as above

  tools {
    mcp = [
      { server = glen_tool.tickets.name },
      { server = glen_tool.datadog.name },
      { server = glen_tool.notion.name, as = "user" },
      { server = glen_tool.github.name, as = "agent" },
    ]
  }
}
```

| Argument | Notes |
|---|---|
| `name` | Required. Kebab-case, not `glen`. Changing it replaces the tool. Tool names and agent names are separate. |
| `url` | Where the MCP server answers, http or https. Set exactly one of `url`, `sample` and `cli`. |
| `sample` | A sample MCP server Glen answers itself: `meetings`. |
| `cli` | Block. A service the agents' CLIs call. Glen adds the connection's token to requests to its `hosts` outside the sandbox, so the token never enters it. `hosts` is a list of `{ host, auth, user, requests, others }`: `host` is one lowercase hostname, and `auth` is how the token is written, `bearer` (unset) or `basic` with `user` as the user. `requests`, when set, are the only requests the proxy sends on the host with the token, each `{ method, path }`. With `others` unset or `refuse`, any other gets Glen's 403, never reaches the service and is on the session refused; with `others = "anonymous"` it goes with no token at all. In a `path`, `*` matches within one segment and a final `**` the rest of the path. Paths compare without case, and one with an empty, `.` or `..` segment, or a character other than a letter, a digit and `-._~+,=:@`, is named by no rule. `allow` lists hosts the sandbox reaches untouched, such as `*.githubusercontent.com`. `env` sets variables in the sandbox to a stand-in, for a client that will not run without a token. A host with `auth = "aws"` is an AWS endpoint, such as `logs.us-west-2.amazonaws.com`: Glen signs its requests with SigV4 for `aws_service` and `aws_region`, with a session of `aws_role`, an IAM role whose name begins `glen-tool-` and which Glen assumes for each session from the org's own session, with the org's id as the external id. The role's policy is what the tool may do, so an AWS host takes no `requests`. A `cli` tool needs `connect` with a `provider`, unless every host is AWS's, when it has none. |
| `connect` | Block. People and agents sign in to the tool, in the console. `provider` is an AgentCore Identity OAuth2 provider made outside Glen; unset, Glen makes its own when the tool is created. `scopes` are what a connection asks for; changing them means everyone connects again. A sample takes no `connect`. |
| `credential` | Block. The tool's own credential, which Glen adds to every call an agent makes to it as the service, outside the sandbox. `kind` is `api-key`, a key sent as it is in `header`, or as `Authorization: Bearer` with `header` unset; or `service`, an OAuth2 client's secret that Glen trades at `token_url` for a token by the client credentials grant, as `client_id`, asking for `scopes`. Only a `url` tool takes one. Removing the block deletes the value from the org's vault. |
| `credential_wo` | Write-only and sensitive: the credential's value, the key or the client's secret. Terraform keeps it in neither plan nor state, and Glen keeps it in the org's vault and never shows it. Written when the tool is created and whenever `credential_wo_version` changes. Leave it out to set the value on the tool's page in the console. |
| `credential_wo_version` | Change it to write `credential_wo` again, such as to rotate the key. A plan compares it, never the value. |
| `version` | Computed. The tool's version in Glen. |

A credential's value goes only where the tool named when it was set.
While one is set, Glen refuses a change of `url`, `kind`, `header`,
`token_url` or `client_id`, unless the same apply gives a new
`credential_wo` and bumps `credential_wo_version`; the provider then
removes the old value, changes the tool and sets the new one.

Plan checks the tool's shape: exactly one of `url`, `sample` and `cli`,
`connect` where it is needed or refused, host names, `auth` and `user`.
Glen checks the rest at apply and the apply fails with Glen's message.

Sign-in belongs to a person or an agent, and happens on the tool's page
in the console. Terraform declares the tool and its `connect`, never
anyone's connection. An agent that names a tool people sign in to takes
`as = "user"` or `as = "agent"`.

Glen refuses to change the url or the provider of a tool people sign in
to, since their connections were made for that address. Replace the tool
instead, such as with `terraform apply -replace`. Refresh, drift and
`terraform import glen_tool.tickets tickets` work as for agents.

At plan, Glen checks an agent's `tools.mcp` against the tools it already
has, so one apply can create a tool and the agents that use it. At apply,
Glen refuses an agent that names a tool it does not have. Glen refuses to
delete a tool while any agent uses it; `terraform destroy` deletes the
agents first, because they reference the tool.

## Define a member

A member is a person of the org the provider's token belongs to, with
their role.

```hcl
resource "glen_member" "ana" {
  email = "ana@example.com"
  role  = "admin"
}
```

| Attribute | Notes |
|---|---|
| `email` | Required, lowercase. Changing it replaces the member. |
| `role` | Required: `admin` or `member`. An admin manages API tokens, agents, knowledge bases, tools and settings; a member runs agents and reads. |
| `state` | Computed: `active` once the person has joined, `invited` while their invitation is pending. |

A new member is invited by email. Changing `role` changes their role, and
an invitation not yet accepted is sent again with the new role.
Destroying a member ends the membership, with the person's console logins
and API tokens, or revokes the invitation. Glen refuses to demote or
remove the org's last admin. A person who is already a member or invited
is imported by email: `terraform import glen_member.ana ana@example.com`.

## Define a role and who may message an agent

A role is a named group of the org's members, and grants nothing by
itself. An agent's `allowed_message_senders` names the roles whose
members may message it: start it, reply to it, answer its questions,
allow or deny its held tool calls and stop it. Its admins always may.
Everyone else in the org may still read its sessions. Glen mails those
members when the agent waits on them, and sends each of them the daily
digest of its asks. Unset, every member may message the agent, only its
admins allow its held calls, and the admins are the ones told.

```hcl
resource "glen_role" "security" {
  name        = "security"
  description = "Allows containment."
  members     = [glen_member.ana.email]
}

resource "glen_agent" "alert_triage" {
  name                    = "alert-triage"
  allowed_message_senders = [glen_role.security.name]
  # instructions, model, triggers, tools, as above
}
```

| Attribute | Notes |
|---|---|
| `name` | Required. Kebab-case, at most 40 characters, neither `admin` nor `member`, which are built in. Changing it replaces the role. |
| `description` | Optional, up to 300 characters. What the role is for. |
| `members` | Optional set of emails, exactly who holds the role. Each must already be a member of the org: an invitation holds no role until it is accepted. Unset, Terraform leaves who holds the role to the console. |

On `glen_agent`, `allowed_message_senders` is an optional list of role
names; `member` names everyone in the org. Glen refuses an agent naming a role the org
does not have, and refuses to delete a role while an agent names it, so
`terraform destroy` deletes the agents first. Glen keeps every change to
a role, who made it and when. A role is imported by name:
`terraform import glen_role.security security`.

## Set a model key

```hcl
resource "glen_model_key" "anthropic" {
  model_provider = "anthropic"
  key_wo         = var.anthropic_api_key # an ephemeral variable
  key_wo_version = 1
}
```

| Attribute | Notes |
|---|---|
| `model_provider` | Required: `anthropic` or `openai`. Changing it replaces the key. |
| `key_wo` | Required, write-only and sensitive. The org's key, which Glen checks with the model provider, keeps in the org's vault and never shows. Terraform keeps it in neither plan nor state. |
| `key_wo_version` | Change it to write `key_wo` again, such as to rotate the key. |
| `set_by`, `last4` | Computed: who set the key Glen holds and its last four characters, after the last apply or refresh. Other values mean it was set elsewhere, such as in the console. |

Destroying it removes the org's key of that provider; a refresh that finds
none set shows it as one to create. `terraform import
glen_model_key.anthropic anthropic` brings in a key set in the console.

## Set auto refill

When the org's credit falls below `below_cents`, Glen pays `amount_cents`
with the card saved at the org's last top-up. An org has one auto refill,
so declare at most one. The provider's token must be an admin's.

```hcl
resource "glen_auto_refill" "org" {
  enabled      = true
  below_cents  = 1000  # $10
  amount_cents = 5000  # $50
  cap_cents    = 30000 # $300 a month at most
}
```

| Attribute | Notes |
|---|---|
| `enabled` | Required: whether Glen refills the org's credit. |
| `below_cents` | Refill when the balance falls below this: 100 to 100000. Defaults to 500. |
| `amount_cents` | What one refill pays before tax: 2000 to 100000. Defaults to 2000. |
| `cap_cents` | The most refills pay before tax in a calendar month (UTC): at least one refill, up to 1000000. Defaults to 20000. |
| `changed_by`, `changed_at` | Computed: who last changed it and when, after the last apply or refresh. |

A change made on the console's Billing page shows in the next plan, and
the apply after it sets the values back. Destroying the resource leaves
the org's auto refill as it is, on or off: to stop refills, apply
`enabled = false` first. `terraform import glen_auto_refill.org
org` brings in the org's current settings; any id works. Top-ups and the
card stay in the console, since a person pays them.

## What stays in the console

Connecting an account to a tool, sign-in, personal API tokens, sessions,
feedback and the curator inbox are people's own acts, and stay in the
console.

## Develop

Building needs Go 1.25 or later.

```sh
go build -o "$HOME/go/bin/terraform-provider-glen" .
```

To use that binary instead of the registry's, add this to
`~/.terraformrc`, with the absolute path of the directory that holds it,
and skip `terraform init` for this provider:

```hcl
provider_installation {
  dev_overrides {
    "meet-glen-ai/glen" = "/home/you/go/bin"
  }
  direct {}
}
```

### Tests

```sh
go vet ./...
go test ./...
```

The tests drive the provider through Terraform, so `terraform` must be on
`PATH`. They run against an in-memory server that answers Glen's admin
API (`internal/provider/fake_test.go`).

`TestEverySpecFieldIsCarried` reads every field path of the agent,
knowledge base and tool specs from
`internal/provider/testdata/spec-fields.json`, or from the file
`GLEN_SPEC_FIELDS` names, and fails on one a resource's schema does not
carry.

With a `glen` binary and a Postgres server, the same tests also run
against a real `glen serve`:

```sh
TF_ACC=1 GLEN_BIN=/path/to/glen DATABASE_URL=postgres://postgres@127.0.0.1:5432/postgres go test ./...
```

Each test makes its own database on that server, runs `glen migrate`, and
starts its own `glen serve` on loopback with a local store and process
sandboxes. `psql` must be on `PATH`. Without `TF_ACC=1`, `GLEN_BIN` and
`DATABASE_URL`, those subtests skip.

### Releases

A tag `v*` runs `.github/workflows/release.yml`, which builds the provider
with GoReleaser and signs its checksums with the release GPG key, as the
Terraform registry requires.

## License

[MPL-2.0](LICENSE).
