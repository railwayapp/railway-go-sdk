# Thin Railway Infrastructure as Code authoring helpers for Go.

Module: `github.com/railwayapp/railway-go-sdk` (import name `railway`).

Author `.railway/railway.go` using this module, then `railway config plan` /
`railway config apply`. One repository (or monorepo) for the whole
environment: one file describing every resource. One repository per service:
one file per repository, each with a named partial (see
[Multi-repo projects](#multi-repo-projects)).

```go
package main

import "github.com/railwayapp/railway-go-sdk"

func Railway() railway.Project {
  db := railway.Postgres("db")
  web := railway.ServiceNamed("web", railway.ServiceConfig{
    "source": railway.Github("org/app"),
    "start":  "./app",
    "env": map[string]any{
      "DATABASE_URL": db.Env("DATABASE_URL"),
    },
    // Per-environment tracing switches; omit the block to leave tracing off.
    "tracing": map[string]any{"enabled": true, "autoInstrumentation": true},
  })
  return railway.ProjectNamed("my-app", []any{db, web})
}
```

Put a `go.mod` next to `.railway/railway.go` (the CLI `go run`s from that
directory):

```
module railway-config

go 1.22

require github.com/railwayapp/railway-go-sdk v0.2.0
```

Config as Code migration: `railway config migrate --lang go`.

## Project variables

Shared project variables are a project policy, not a service setting. Pass
`ProjectConfig` to `ProjectNamed`. Omitted, the file does not declare a policy.
The evaluated payload is `project.variables`: `{"managed": bool, "ignore": []string}`.
`managed` claims shared project variables for this file. `ignore` lists names
left untouched. Service `variables` / `env` are still that service's variables.

```go
return railway.ProjectNamed("my-app", []any{web}, railway.ProjectConfig{
  Variables: &railway.VariablesPolicy{
    Managed: true,
    Ignore:  []string{"STRIPE_KEY"},
  },
})
```

## Pull request context

`railway.Context.PR` is nil when the IaC context JSON has no `pr` object.
Otherwise it is decoded from `pr` (`number`, `branch`, `base`).

```go
func Railway(ctx railway.Context) railway.Project {
  if ctx.PR != nil {
    // ctx.PR.Number, ctx.PR.Branch, ctx.PR.Base
  }
  return railway.ProjectNamed("my-app", []any{})
}
```

## Environments

`environments` (`[]string`) on a service, database, bucket, volume, or group
is emitted as `environments` on that resource. Omit it to leave the field unset.

```go
web := railway.ServiceNamed("web", railway.ServiceConfig{
  "environments": []string{"production", "staging"},
})
```

## GitHub source

`Github(repo)` with no `branch` is environment-owned: Railway uses the branch
connected to the environment (a pull-request environment tracks the PR) instead
of a branch pinned in this file. Pass `branch` to pin one. An empty branch is
the same as omitting it.

```go
railway.Github("org/app")                              // environment-owned
railway.Github("org/app", map[string]any{"branch": "main"}) // pinned
```

## Multi-repo projects

A named partial lets each repository manage its own slice of a Railway
environment. Declare `const Partial = "<name>"` in `railway.go` (same role as
`export const partial` in TypeScript and `PARTIAL` in Python). The CLI records
which partial owns each resource; omitting a resource then only deletes it if
your partial owns it. A partial name is 1 to 64 characters from `a-z`, `A-Z`,
`0-9`, `.`, `_`, and `-`.

An `api` repository owns the API service and its database:

```go
// api/.railway/railway.go
package main

import "github.com/railwayapp/railway-go-sdk"

const Partial = "api"

func Railway() railway.Project {
  db := railway.Postgres("postgres")
  api := railway.ServiceNamed("api", railway.ServiceConfig{
    "env": map[string]any{
      "DATABASE_URL": db.Env("DATABASE_URL"),
    },
  })
  return railway.ProjectNamed("acme", []any{api, db})
}
```

A `web` repository owns the frontend service:

```go
// web/.railway/railway.go
package main

import "github.com/railwayapp/railway-go-sdk"

const Partial = "web"

func Railway() railway.Project {
  web := railway.ServiceNamed("web", railway.ServiceConfig{})
  return railway.ProjectNamed("acme", []any{web})
}
```

After both repositories apply, partial `api` owns `service.api` and
`database.postgres`, and partial `web` owns `service.web`. Removing `db` from
the `api` file deletes the database on the next `api` apply. Nothing the `web`
file does can touch it.

Ownership rules the CLI enforces on every plan and apply:

- A resource declared in a file whose partial does not own it fails with
  `Cannot manage service "api": already managed by partial "web".` Move the
  declaration to the owning repository, or remove it from the owner first.
- A file without `Partial` fails in an environment that already has named
  partials. Once any partial has a name, every file targeting that environment
  must declare one.
- A named partial only deletes resources it owns. Resources owned by other
  partials, or not yet owned by any, are left alone when missing from your file.
- The first `railway config apply` from a partial claims ownership of every
  resource the file declares, even when the plan shows no configuration changes.
- Do not rename a partial after apply. The resources stay owned by the old name
  and the renamed file fails the foreign-resource check.

Apply from each repository's own pipeline; Railway does not read `.railway/`
during deploys.

See https://docs.railway.com/infrastructure-as-code
