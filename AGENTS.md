# Working in Agents

Read README.md and docs/interfaces.md before changing public contracts.

Keep the runtime independent of application frameworks, identity providers,
resource models, storage engines and UI transports. Extend behavior through
small interfaces and adapters. Applications own permissions and business rules.

Registering a tool does not authorize it. Validate the whole model call batch
before side effects. Recheck execution authority around model calls and tools.
Never treat unknown usage as free or repeat a pending write without reconciling
its receipt. Preserve operation identities across attempts.

Use Type and type for owned discriminators. Keep effect identifiers open to
application extensions. Do not introduce application-specific tool names or
ownership records into the library.

Run bash scripts/check.sh before publication. External protocol fixtures must
come from captured responses. Tests must not call paid model endpoints.

Use commit prefixes feat, fix, refactor, docs, ui or test, with a capitalized
subject under 80 characters. Never add authorship footers or tool signatures.
