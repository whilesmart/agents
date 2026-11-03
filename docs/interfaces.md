# Integration contracts

## Model

`Generate` returns text, tool calls, optional token usage and an explicit
completion flag. A response that was cut off is incomplete even if it contains
valid JSON. Unknown usage is nil. Known usage is nonnegative; numeric overflow
is rejected. Model implementations must obey context cancellation.

A model may propose only tool names supplied in the current request. Tool
arguments are untrusted JSON. The runtime validates the complete call batch
before running its first action. Validation does not replace application-level
rules such as ownership, resource access or publication policy.

## Host

`Tools` produces the current authorized catalogue. Registrations may differ by
actor, workspace, resource and run. Input schemas and versions are declarations
owned by the application.

`Checkpoint` verifies that the execution lease and authorization remain current.
It must fail when the actor loses access, approval is withdrawn or the run is
cancelled. Successful registration alone is insufficient authority.

`Reserve` records an allowance before a model call. `RecordUsage` records its
reported usage or nil. A nil usage report retains the allowance; never replace
it with zero tokens. The runs controller persists this per attempt before
calling the host's optional accounting integration. Applications may add account
quotas, monetary caps and auditing.

`Execute` calls the same application operation used by other interfaces. Return
a failed `ToolResult` for a known rejected operation. Transport errors with a
possible committed effect need reconciliation. The journal wrapper treats such
errors as uncertain and stops execution.

## Tool registry

`NewTools` requires an authorizer. `Register` requires a unique name, positive
version, valid schema and handler. Definitions are copied. Discovery checks
`discover`; execution checks `execute`, validates arguments again and verifies
the advertised version. There is no process-global registry.

`Effect` is an open string. The runtime recognizes `read` for context compaction;
other effects are retained without read-result compaction. Effect labels do not
authorize writes or substitute for an approval check.

## Run repository

`Create` inserts a new run without replacing an existing ID. `Get` returns an
independent snapshot. `Replace` atomically compares the stored revision and
requires the successor revision. Scope, requester, identity and creation time
cannot change. Validate records and do not share mutable slices with callers.

A claim transitions a queued run into a leased running attempt. An expired
attempt becomes abandoned before a new claim. An old attempt cannot publish a
late answer. Cancellation changes the revision and revokes the active lease.
Concurrent claims, cancellation and completion must resolve through atomic
revision comparison.

Run messages contain application data. Store them privately and authorize reads,
execution, cancellation and resumption through `Access`. Run IDs alone are not
credentials. Model credentials must stay outside repository records.

`Execute` performs one claimed attempt; it is not a queue server. A caller or
scheduler decides when to execute queued work. Recovery does not infer a retry
policy or automatically approve waiting actions. Long-running attempts must
renew their lease before it expires.

Execution receipts stay in returned action history and storage. Model context
contains the outcome, output and failure only; it does not include the opaque
receipt. Keep resource handles needed by the model in the explicit output.

## Action journal

`Claim` atomically establishes an operation receipt and its canonical input
fingerprint, or returns the existing receipt. Only one caller can receive a new
claim. `Complete` records the known result without overwriting a completed
receipt. A storage failure after a side effect must leave a pending or known
receipt, never make the operation appear new.

Use a namespace scoped to the application's actual authorization boundary.
`OperationKey` hashes the namespace, run ID and ordinal without delimiter
ambiguities. Attempt IDs are deliberately excluded, so retries reuse receipts.
A changed operation with the same identity conflicts rather than executing.

An uncertain operation needs an application-specific reconciliation workflow.
The library does not guess whether an external side effect committed, retry it
blindly or grant its own approval. Memory journals are suitable only where
restart durability is unnecessary.

## Compatibility and versions

The root package has no application framework, storage model or identity types.
An adapter can translate existing types without duplicating the execution loop.
Preserve persisted operation identities and receipt fingerprints when replacing
an existing runtime. Changes to tool behavior require a new tool version.
