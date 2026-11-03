# WhileSmart Agents

Application-agnostic agent execution and run orchestration for Go.

Register your own tools, choose a model and connect your application's access
checks, persistence and accounting. The library does not require a web
framework, database, identity provider, conversation model or application type.

## Install

```sh
go get github.com/whilesmart/agents
```

## Components

- `agents.Runtime` runs bounded model and tool steps against a `Host`.
- `agents.Tools` registers application-defined tools and execution handlers.
- `agents.Model` accepts any model implementation. `CompatibleModel` is an
  optional HTTP adapter for the compatible chat-completions protocol.
- `agents.WithJournal` prevents duplicate side effects across retries.
- `runs.Service` coordinates queued runs, attempts, leases, cancellation,
  explicit resumption and per-call token reservations through a repository.
- `agents.Price` calculates token costs with integer currency micro-units.

## Define a tool

```go
policy := agents.ToolAuthorizeFunc(func(ctx context.Context, tool agents.Tool, action string) error {
    return application.AuthorizeTool(ctx, tool.Name, action)
})
tools, err := agents.NewTools(policy)
if err != nil {
    return err
}
err = tools.Register(agents.Tool{
    Name:        "inventory_reserve",
    Version:     1,
    Effect:      "write",
    Description: "Reserve inventory through the application's normal operation.",
    InputSchema: json.RawMessage(`{
        "type":"object",
        "properties":{"sku":{"type":"string"},"quantity":{"type":"integer","minimum":1}},
        "required":["sku","quantity"],
        "additionalProperties":false
    }`),
}, func(ctx context.Context, execution agents.Execution) (agents.ToolResult, error) {
    return application.ReserveInventory(ctx, execution.Arguments)
})
```

The `application` in this example is your adapter. Its operations enforce the
same validation and permissions used by your other clients. A tool registration
never grants permission. The authorizer controls discovery and checks access
again on execution. Duplicate registrations are rejected.

## Run an agent

Supply a `Model` and a `Host`, then call `Runtime.Run` with opaque run and attempt
IDs and messages. `HostAdapter` can combine a registered toolset with application
callbacks for execution checkpoints, budget reservations and usage recording.
All three callbacks are required; there is no implicit permission or free usage.

`Runtime` checks cancellation and execution authority between model calls and
tool actions. Every call in a model response is checked against its registered
schema before any of that response's tools execute. Truncated responses stop
execution. Read results may be compacted in model context while original action
results and receipts remain in the returned history. Receipts are excluded
from model context to avoid duplicating outputs or exposing audit metadata.

## Durable runs and actions

Use `runs.Service` when requests need scheduling, persisted attempts and recovery.
Implement its `Repository` with your database or storage system. `Replace` must
atomically compare the expected revision; ordinary read-then-write is unsafe.
`runs.NewMemory` is for tests and single-process examples, not durable storage.

A scheduler selects a queued run and calls `Execute`. A claim acquires a lease;
an expired claim is recorded as abandoned before another attempt starts. Long
running workers can renew the current lease. Cancellation invalidates it.
Authorization is checked again during execution. Waiting and failed runs need
an explicit, authorized resume.

Each attempt stores its model-call reservations and usage. Unknown usage keeps
the full reservation, including across retries. Known usage releases unused
allowance. A cancelled or abandoned attempt does not erase its accounting.
Hourly account limits and monetary policy belong in the host's budget adapter.

Wrap effectful hosts with `WithJournal` and provide a durable `Journal`.
Operation identity is stable across attempts. Repeating the same operation
reuses its completed result; changing its input conflicts. A pending operation
or uncertain outcome stops execution instead of repeating the write. Your
application must reconcile that outcome before allowing further execution.
`NewMemoryJournal` demonstrates the contract but does not survive a restart.

## Integration boundaries

Models receive messages and tool definitions, never application credentials by
implication. Ownership, actors, resource references and approval policies remain
application concerns. Scope and requester strings are opaque to the library.
Provider credentials belong in server configuration and must not be persisted
inside run messages or tool arguments.

The library executes registered handlers. It does not sandbox uploaded code,
provision application runtimes, install dependencies, invent database migrations
or expose a public HTTP API. Applications supply those boundaries independently.

See [interfaces](docs/interfaces.md) for lifecycle and storage guarantees.

## Development

Requires Go 1.24.3 or newer. Run all local QA checks with:

```sh
bash scripts/check.sh
```

CI checks the minimum Go version and the current stable Go version. Releases
rerun QA on the tagged commit before publishing a GitHub release. Module
consumers use semantic version tags.

## License

MIT.

## Contributing

Use a feature branch and pull request. The `main` and `dev` protection definitions
in `.github/rulesets/` require review, resolved conversations and successful QA
and commit-message checks. Force pushes and branch deletion are blocked.
