# Todo Task Tracker — Agent Instructions

## Build & Run

```bash
# Build, lint, and test (via Makefile)
make build  # go build -o todo .
make test   # go test ./... -count=1
make lint   # golangci-lint run ./...
make all    # lint + test + build

# Run with default SQLite DB (~/.todo.db)
./todo task list

# Run with custom DB path
./todo --db /tmp/test.db task list

# Run with config file
./todo --config path/to/config.yaml task list
```

## Project Structure

```
model/           # Data types: Task, Note, Link, TaskTag, TaskBlocker, TaskState, errors
config/          # YAML config loading via viper
store/           # Store interface, event types, option types
store/gormstore/ # GORM implementation (SQLite + PostgreSQL)
store/synced/    # VectorSyncer: observer that syncs to vector store
embed/           # Embedder interface
embed/ollama/    # Ollama embedding implementation
embed/openai/    # OpenAI embedding implementation
vectorstore/     # VectorStore interface
vectorstore/pgvector/ # pgvector (PostgreSQL) implementation
audit/           # Structured audit logger (StoreObserver)
cmd/             # Cobra CLI commands
mcp/             # MCP server and tool definitions
scripts/         # Cert generation and container entrypoint helpers
deploy/          # Systemd quadlet files and production config
```

## Key Interfaces

- **`store.Store`** — interface for all task operations. Implemented by `gormstore.GormStore`.
- **`store.StoreObserver`** — receives `StoreEvent` after mutations. Used by `audit.Logger` and `synced.VectorSyncer`.
- **`store.SemanticSearcher`** — vector similarity search. Implemented by `synced.VectorSyncer`.
- **`embed.Embedder`** — generates vector embeddings. Implementations: `ollama.Embedder`, `openai.Embedder`.
- **`vectorstore.VectorStore`** — vector storage and search. Implementation: `pgvector.Store`.

## MCP Server

Start the MCP server for AI agent access:

```bash
# stdio transport (for Claude Code / Claude Desktop)
./todo mcp --db /path/to/todo.db

# HTTP streamable transport
./todo mcp --transport http --addr :8080 --db /path/to/todo.db
```

### Claude Code Configuration

Add to your MCP settings:

```json
{
  "mcpServers": {
    "todo": {
      "command": "/path/to/todo",
      "args": ["mcp", "--db", "/path/to/todo.db"]
    }
  }
}
```

### Available MCP Tools (24 core + 1 optional semantic)

**Tasks:** `create_task`, `list_tasks`, `get_task`, `get_tasks`, `update_task`, `set_task_state`, `set_task_priority`, `update_blockers`, `set_task_archived`, `delete_task`, `set_parent`

`create_task` accepts an optional `parent_id` (omit for top-level, set to create a subtask under an existing non-archived parent) and an optional `links` array. Each link item is `{type, url, description}` (description optional). Inline links are inserted in the same transaction as the task, so any link-validation failure rolls the whole call back. Only one `task.created` event fires regardless of link count — the vector syncer re-embeds the task once with link descriptions included. Prefer inline `links` over per-link `add_link` calls when creating a task that already has its references in hand: it is atomic and avoids the per-link re-embed churn. `create_task` returns full task detail by default; pass an `include` array to restrict expensive fields.

`set_task_state`, `set_task_priority`, and `set_task_archived` all accept an `ids` array (1..100). Single-task callers pass `ids: [42]`. The mutations are atomic across the whole array. `set_task_priority` promotes blockers to at least match the priority of any task they block.

**Notes:** `add_note`, `update_note`, `list_notes`, `delete_note`

**Links:** `add_link` (with optional `description`), `list_links`, `update_link`, `delete_link`

**Checkpoints:** `set_checkpoint`, `get_checkpoint`, `delete_checkpoint`

**Tags:** `add_tags`, `remove_tags` — both accept `ids` (array, 1..100) and `tags`. Atomic across the whole array.

**Semantic (when vector enabled):** `semantic_search`

`semantic_search` runs in one of two modes:
- **Query mode** — pass `query` (natural-language search string). Optional `task_id` scopes results to entities tied to a specific task.
- **Context mode** — pass `related_to_task_id`. The tool aggregates that task's text and notes as the query and excludes the source task and its attached notes from results. Scope `task_id` is ignored in this mode.

Exactly one of `query` and `related_to_task_id` must be provided; passing both or neither is rejected. Other options (`type`, `limit`, `include_archived`) apply to both modes. Semantic search excludes archived items by default. Pass `include_archived: true` (MCP) or `--include-archived` (CLI) to include them.

**Migration callout (breaking change):** the `semantic_search_context` MCP tool is removed. Callers should pass `related_to_task_id` to `semantic_search` instead. The `Store.SemanticSearcher` Go interface keeps both `SemanticSearch` and `SemanticSearchContext` methods unchanged, so non-MCP callers (CLI `search context`) are unaffected.

**Migration callout (breaking change — `update_blockers`):** the `add_blockers` and `remove_blockers` MCP tools are removed. Use `update_blockers(task_id, add?, remove?)` instead; at least one of `add`/`remove` must be non-empty. Removals are processed first so a blocker can be swapped in one call without tripping cycle detection on the stale row. Validations are unchanged: no self-blocking or cycles, blocker cannot be Done or archived, blocker priority is promoted, state transitions to Blocked when any blockers remain and auto-Unblocked when all are removed. A new `Store.UpdateBlockers(ctx, taskID, add, remove)` method backs it; `Store.AddBlockers` and `Store.RemoveBlockers` are unchanged so CLI `task block`/`task unblock` continue to work.

**Migration callouts (breaking changes — `bulk_*` fold):**
- The four `bulk_update_state`, `bulk_update_priority`, `bulk_add_tags`, and `bulk_remove_tags` MCP tools are removed. Their array-of-IDs behavior is now the default on `set_task_state`, `set_task_priority` (new), `add_tags`, and `remove_tags`.
- `set_task_state`, `add_tags`, and `remove_tags` no longer accept a singular `task_id`; they accept `ids: number[]` (1..100). Single-task callers pass `ids: [42]`.
- `set_task_state` now returns `[]Task` (always an array), not a single `Task`. Single-ID callers must read `result[0]`. The same applies to `set_task_priority`.
- `set_task_priority` is a new tool symmetric with `set_task_state`. The existing `update_task.priority` field is still the patch path when changing priority alongside other fields on one task.
- The `Store.AddTags`, `Store.RemoveTags`, `Store.SetTaskState`, and `Store.Bulk*` Go methods are all unchanged, so the CLI commands (`task tag`/`task untag`/`task state`/`task bulk-state`/`task bulk-priority`/`task bulk-add-tags`/`task bulk-remove-tags`) continue to work.

### `list_tasks` Filtering

`list_tasks` supports the following filters (all optional, combined with AND logic):

| Parameter | Type | Description |
|-----------|------|-------------|
| `states` | array[string] | Filter by state (OR logic): New, Progressing, Blocked, Unblocked, Done |
| `include_archived` | boolean | Include archived tasks (default: false) |
| `include_subtasks` | boolean | Include subtasks in flat list (default: false) |
| `parent_id` | number | Filter to subtree of this task ID |
| `tags` | array[string] | Task must have ALL specified tags (superset/AND logic) |
| `tags_subset_of` | array[string] | Task's tags must all be within this set (subset check) |
| `overdue` | boolean | Only tasks past their due date |
| `has_due_date` | boolean | true = only with due date, false = only without |
| `due_before` | string | Due before this date, exclusive (YYYY-MM-DD) |
| `due_after` | string | Due after this date, exclusive (YYYY-MM-DD) |
| `due_on` | string | Due on this calendar day (YYYY-MM-DD, UTC) |
| `priority_min` | number | Priority >= this value (inclusive) |
| `priority_max` | number | Priority <= this value (inclusive) |
| `query` | string | Case-insensitive substring match on title, description, and link descriptions (max 500 chars). Use for exact keyword lookups; use `semantic_search` for conceptual matches. |
| `sort_by` | string | Sort: priority (default), due, created, updated |

**Combining tag filters:** Use `tags` + `tags_subset_of` together for exact tag matching (task has at least AND only these tags).

**CLI equivalents:** `--state` (repeatable), `--has-due-date`, `--no-due-date`, `--due-before`, `--due-after`, `--due-on`, `--priority-min`, `--priority-max`, `--tag-subset-of`, `--query`/`-q`.

**Migration callout (breaking change):** the `list_tasks` MCP parameter `state` (single string) has been renamed to `states` (array of strings) with OR-logic across the listed values. Clients that previously passed `state: "Done"` should now pass `states: ["Done"]`. The CLI `--state` flag keeps its name but is now a `StringSlice` (repeatable / comma-separated) — single-value invocations remain compatible.

### `get_tasks` Batch fetch

`get_tasks` fetches multiple tasks in one call (max 100 IDs). The response is `{"tasks": [...], "not_found": [...]}`:

- **Input order is preserved** — `tasks[i]` corresponds to the i-th unique input ID. Unlike the `bulk_*` mutation ops (which sort ascending), reads keep caller order so clients can align positions with their request.
- **Duplicates collapse** to first occurrence.
- **Missing IDs do not error** — they go into `not_found` instead, so partial hits still return data.
- **`include`** uses the same enum as `get_task` (`description`, `notes`, `links`, `parent`, `children`, `blockers`, `blocking`, plus `*`) and applies uniformly to every returned task.
- Both arrays are always present in the response, rendered as `[]` when empty (never `null`).
- CLI equivalent: `todo task get-many <id> <id> [...]` (full detail by default; `--json` emits the structured response).

## Task States

`New` -> `Progressing` -> `Done`

A task can be `Blocked` (via `update_blockers` with an `add` list) or `Unblocked` (auto-transition when all blockers are removed).

- Use `set_task_state` for New, Progressing, Done.
- Use `update_blockers` (with `add` and/or `remove` arrays) to manage Blocked state. Both lists can be supplied in one call to swap blockers atomically.
- Unblocked is an auto-transition — it fires when a Blocked task's last blocker is removed (via `update_blockers`, `task unblock`, or a Done cascade). It is not directly settable.
- Setting Done auto-unblocks dependents with no remaining blockers.

**Migration callout (breaking change — `set_task_state` blocker preservation):**
Transitioning a Blocked task to any non-Done state (e.g. `Progressing`, `New`) now requires the caller to opt into dropping the outstanding blocker rows. Previously the rows were silently deleted on every state change, which masked dependency loss.

- MCP: pass `force_clear_blockers: true` on `set_task_state` to drop blockers as part of the transition. Without it, the call returns an error wrapping `ErrInvalidState` and the task's state and blocker rows are unchanged.
- CLI: `todo task state <id> <state> --force-clear-blockers` and `todo task bulk-state --force-clear-blockers`.
- Done is still terminal — Done transitions always clear the task's blocker rows and auto-unblock dependents, no flag required.
- Non-Blocked tasks are unaffected (there are no blocker rows to preserve).
- Store interface change: `Store.SetTaskState` and `Store.BulkUpdateState` now take a `store.SetTaskStateOptions{ForceClearBlockers}` argument. The CLI commands above route through it.

**Migration callout (breaking change — `set_task_archived` is now atomic):**
The previous `set_task_archived` implementation looped `ArchiveTask` per ID outside any wrapping transaction, leaving a committed prefix when one ID failed mid-batch. The tool now wraps the entire array in a single transaction via the new `Store.BulkSetArchived(ids, archived) -> []TaskDetail`.

- Failure of any ID rolls back the whole batch — no partial commit. Callers that previously had to re-query archived state on error no longer need to.
- Cross-input blockers are intentionally permitted: if A blocks B and both IDs are in the call, the external-blocker check treats the union of all input subtrees as the "set," so the call succeeds (where the per-ID loop would have rejected it).
- Response is `[]TaskDetail` in input order with duplicates collapsed to first occurrence (matches `get_tasks` semantics).
- `Store.ArchiveTask(id, archived)` is preserved as a thin wrapper over `BulkSetArchived([id], archived)` so the CLI `task archive` / `task unarchive` callers are unaffected.

**Migration callout (breaking change — `Unblocked` is no longer directly settable):**
The `set_task_state` MCP enum drops `Unblocked`; the store rejects it with `ErrInvalidState`. `Unblocked` was always meant to be a transient auto-transition that fires when a Blocked task's last blocker is removed, not a target a user picks. Manually setting it had no well-defined meaning.

- MCP: the `state` enum on `set_task_state` is now `"New" | "Progressing" | "Done"`. Calls with `"Unblocked"` are rejected at the schema layer.
- Store: `Store.SetTaskState(_, _, StateUnblocked, _)` and `Store.BulkUpdateState(_, _, StateUnblocked, _)` return an error wrapping `ErrInvalidState` regardless of the current state.
- CLI: the `--state` parse path accepts the string `Unblocked` (for backward-compatible flag parsing) but the store rejects it with a clear error message pointing to `Progressing` or `New`.
- Auto-transition path is unchanged: removing the last blocker (via `update_blockers`, `task unblock`, or a Done cascade) still transitions a Blocked task to `Unblocked` automatically. `Unblocked` remains a valid filter value for `list_tasks states`.

## Priority

Lower number = higher importance. P0 > P1 > P2. Negative priorities are valid.

Blockers are automatically promoted to at least match the priority of tasks they block.

## Validation Rules

- **All string fields**: Must be valid UTF-8, no null bytes; normalized to NFC on input; leading/trailing whitespace trimmed
- **Title**: required, max 512 characters (Unicode code points)
- **Description**: optional, max 100,000 characters
- **Tags**: alphanumeric, hyphens, and underscores only (`[a-zA-Z0-9_-]+`), max 100 chars per tag, max 50 tags per task
- **Notes**: required non-empty text, max 50,000 characters; `task_id` optional (omit for standalone notes)
- **Links**: URL required, max 2000 bytes; description optional, max 1000 characters
- **Checkpoints**: `recap` required, `next_steps` required, `open_threads` optional — each field max 10,000 characters
- **Search queries**: max 500 characters
- **Bulk operations**: max 100 IDs per call

## Subtask Hierarchy

Tasks can be organized in parent-child trees. Use `set_parent` (omit `parent_id` to make a task top-level) or pass `parent_id` to `create_task` at creation time.

- `list_tasks` shows top-level by default. Use `include_subtasks: true` or `parent_id` filter.
- Deleting a parent promotes children. Use `recursive: true` to delete the entire subtree.
- Archiving always cascades to the subtask tree.

**Migration callouts (breaking changes):**
- `create_subtask` is removed. Use `create_task` with `parent_id` to create a subtask. The unified `create_task` now returns full task detail by default (the old `create_task` returned only the bare task); pass `include` to restrict expensive fields.
- `unparent` is removed. Use `set_parent` with `parent_id` omitted to make a task top-level.
- `archive_task` and `unarchive_task` are replaced by `set_task_archived(ids: number[], archived: boolean)`. The new tool accepts an array of IDs (max 100, must be non-empty) and returns full detail for every task processed. Each task is still archived atomically (subtree cascade preserved); cross-task atomicity is not guaranteed — a failure aborts at that point with the prefix already committed. On error, callers should re-query archived state to determine which IDs were processed.

## Notes

Notes can either be attached to a task (`task_id` set) or standalone (`task_id` omitted). The model supports both at every layer:

- **Create:** `add_note` accepts an optional `task_id`. Omit it for a standalone note.
- **List / search:** `list_notes` is the single query tool. Set `task_id` to restrict to that task's notes; otherwise `scope` selects breadth — `"all"` (attached + standalone, default), `"standalone"` (orphan notes), or `"attached"` (notes with a parent task). Optional `query` applies a case-insensitive substring filter on the note text. Archived notes are excluded unless `include_archived: true` is set.
- **Update / reparent:** `update_note` operates by `note_id` alone. Provide any of `text`, `task_id` (reparent target), `clear_task_id: true` (detach to standalone), or `archived`. At least one must be provided.
- **Delete:** `delete_note` takes only `note_id`.
- **Archive:** standalone notes have their own `archived` flag (`note archive <id>` / `note unarchive <id>` from the CLI, or `update_note` with `archived: true|false` via MCP). Task-attached notes also have an `archived` column, but semantic search inherits archived state from the parent task while the note is attached — the per-note flag only takes effect after the note is detached (orphaned or explicitly cleared).
- **Task deletion:** `delete_task` orphans a task's notes by default (sets `task_id=NULL`); their per-note `archived` flag (typically `false`) then governs them. Pass `delete_notes: true` to hard-delete them instead.

**Migration callouts (breaking changes):**
- `list_all_notes` and `search_notes` MCP tools are removed. Their behavior is folded into `list_notes` via the `scope` and `query` parameters. Migration:
  - `list_all_notes()` → `list_notes()` (default scope is now `"all"`).
  - `search_notes(query, task_id?, include_archived?)` → `list_notes({ query, task_id?, include_archived? })`. The 200-row default cap is preserved when `query` is set and no explicit `limit` is provided.
  - `list_notes()` (no `task_id`) previously returned **standalone only**. It now returns **everything** by default. To get the old behavior, pass `scope: "standalone"`.
  - At the Go store layer, `Store.ListNotes(taskID *uint)`, `Store.ListAllNotes()`, and `Store.SearchNotes(query, opts)` collapse to a single `Store.ListNotes(ctx, ListNotesOptions{TaskID, Scope, Query, IncludeArchived, Limit})`. `SearchNotesOptions` is removed.
  - The unified `list_notes` excludes archived by default (matching the old `search_notes`). The previous list paths returned archived rows; callers that need them must pass `include_archived: true` (or `IncludeArchived: true` at the store layer). **This applies to the `task_id`-set path too:** `list_notes({ task_id: N })` previously returned that task's archived notes; it now excludes them.
- `update_note` no longer takes `task_id` as a "find note in this task" parameter; `task_id` now means "reparent to this task." Clients that previously passed `task_id` plus `text` will silently move the note. Update such callers.
- `delete_note` no longer takes `task_id` — only `note_id`.
- The `notes.task_id` column is now nullable. The first run against an existing DB performs an automatic migration (Postgres: `DROP NOT NULL`; SQLite: 12-step ALTER table rebuild).

## Checkpoints

A **checkpoint** is a singleton "resume here" bookmark per task — at most one per task, DB-enforced via a unique index on `task_id`. Separate from notes: notes capture durable knowledge; checkpoints are transient pointers that say "you were here."

- **Three fields:** `recap` (required), `next_steps` (required), `open_threads` (optional). Max 10,000 characters each.
- **Upsert semantics:** `set_checkpoint` creates if absent, replaces if present. There is no separate `add`/`update` split.
- **Not embedded:** checkpoints are not indexed in the vector store and do not appear in `semantic_search` results. They are bookmarks, not searchable knowledge.
- **Archive behavior:** `set_checkpoint` is rejected on archived tasks (`ErrArchived`); `get_checkpoint` and `delete_checkpoint` work against archived tasks so a paused-then-archived task remains readable and cleanable.
- **Task deletion** cascades to the checkpoint. Task archival does not delete it.
- **`get_task`** includes the checkpoint inline under `"checkpoint"` (omitted when absent).
- **`list_tasks`** items include a `has_checkpoint` boolean; the CLI `task list` table shows a `CHK` column marked `*` for tasks with a checkpoint.

## Vector / RAG Setup

Enable in config (requires PostgreSQL with the pgvector extension):

```yaml
vector:
  enabled: true
  embedder: ollama
  store: pgvector
  ollama:
    model: nomic-embed-text
    url: http://localhost:11434
```

Requires Ollama running and PostgreSQL with the pgvector extension (`pgvector/pgvector:pg16` image). Use `todo vector reindex` to build/rebuild the index from scratch.

Vector search is only available with the PostgreSQL backend. When using SQLite, vector search is automatically disabled.

### Failure recovery (`vector_dirty` reconciler)

Real-time embed failures (embedder timeout, rate-limit, transient DB blip) are automatically recovered without operator intervention:

1. `VectorSyncer.OnEvent` catches the failure and calls `Store.MarkVectorDirty` to flag the affected `tasks` / `notes` rows.
2. Under `todo mcp`, a background goroutine started by `VectorSyncer.StartReconciler` wakes every `vector.reconcile_interval` (default 30s) and drains up to `vector.reconcile_batch_size` (default 100) dirty rows per tick.
3. Each batch flows through the normal `embedTasks` / `embedNotes` paths, so per-entity locks still apply and a concurrent fresh update for the same entity serializes correctly.
4. On successful re-embed the dirty flag is cleared inside `embedTasks` / `embedNotes`.
5. If the batch still fails, the flag stays set and the next tick retries.

Short-lived CLI commands (`todo task create`, etc.) don't run the reconciler — they just write the flag if a sync fails. The next `todo mcp` boot picks up the accumulated dirty rows. The `task.deleted` and `note.deleted` paths intentionally don't mark anything (the entity is gone; only `todo vector reindex` can clean orphan chunks from a failed delete).

A task's embedding text includes its title, description, tags, link descriptions, priority, and state. Notes are embedded as separate documents (both attached and standalone notes appear in `semantic_search` results). Link `description` content is therefore searchable via `semantic_search`; URLs and link types are not embedded. Adding/updating/deleting a link automatically re-embeds its parent task. Reparenting a note re-embeds the note (with new `task_id` metadata) but does not re-embed any task, since task embeddings do not include note text.

### Chunking

Long descriptions and notes are split into overlapping ~3000-rune chunks (200-rune overlap) before embedding, so content past `nomic-embed-text`'s ~2048-token training window stays searchable. Each chunk gets a header (task title + tags + state, or for notes "Note for: <parent title>") so mid-body chunks remain self-contained for retrieval. Storage row IDs are `task:<id>:<chunkIdx>` and `note:<id>:<chunkIdx>`. `semantic_search` (in both query and context modes) aggregates per-doc: each result lists every matched chunk in `Chunks[]` (sorted by score) with the parent doc's best score as `Score`.

**Migration callout (breaking storage change):** the row ID format changed and a `chunk_index` column was added to `vector_documents`. The schema migration is automatic on first connect. Old single-doc rows (`task:42`, `note:17`) won't be overwritten by the new chunked IDs, but the sync paths (`embedTasks`/`embedNotes`/`Reindex`) call `DeleteTaskDocs`/`DeleteNoteDocs` before re-upserting, which clears them out for any task or note that gets touched. The recommended migration step is **`todo vector reindex --clear`** — it drops the table outright, which also removes orphaned rows for tasks deleted before the upgrade. A plain `todo vector reindex` (no `--clear`) will clean up old rows for *current* tasks and notes but leaves orphans behind.

## MCP HTTP transport — API key

The HTTP transport supports two authentication forms:

- **Single-tenant** — `mcp.api_key` (string). One key; every authenticated request is attributed to actor `"default"` in audit logs. Suitable for personal deployments. Also configurable via the `TODO_MCP_API_KEY` env var.
- **Multi-tenant** — `mcp.api_keys` (map of label → key). Each label appears as the `actor` field on audit records for requests authenticated with the corresponding key, so a multi-client deployment attributes every mutation to the MCP client that issued it. Env-var config is awkward for maps — use a config file.

The two forms are mutually exclusive; setting both rejects startup.

Three startup-time invariants regardless of form:

1. **Auth without TLS is refused** unless `--insecure` is passed (developer override). TLS cert/key come from `mcp.tls_cert` / `mcp.tls_key` or the matching env vars.
2. **Every configured key must be ≥ 20 characters.** `--insecure` does NOT bypass this — transport security and credential strength are independent concerns. For multi-tenant, a single weak key rejects startup with the offending label in the error.
3. **The bearer middleware runs all key comparisons per request** (no early-return on match), so per-key timing can't distinguish which key was hit. Per-request cost is N constant-time 32-byte hash compares — trivial up to thousands of keys.

Generate a fresh key with:

```bash
todo mcp gen-key  # prints 64 hex chars (32 random bytes)

# Single-tenant wiring:
TODO_MCP_API_KEY=$(todo mcp gen-key) ./todo mcp --transport http

# Multi-tenant — set in a config file:
# mcp:
#   api_keys:
#     alice: "<paste output of todo mcp gen-key>"
#     bob:   "<paste output of todo mcp gen-key>"
```

Audit records pick up the matched label via `store.SetActorContext`, set by the bearer middleware on each authenticated request. The `StoreEvent.Actor` field surfaces in the audit log as the `actor=` attribute when present; it is empty for stdio-MCP and CLI usage.

**Migration callouts:**

- **Breaking change** — existing deployments running with an API key shorter than 20 characters will fail to start. Generate a longer key with `todo mcp gen-key` and update the config / env var.
- **Deprecation hint** — `mcp.api_key` (single-tenant) still works and maps to actor `"default"`. New multi-client deployments should prefer `mcp.api_keys` so audit logs can attribute mutations to specific clients. The MCP server logs an info-level note at startup when the legacy form is in use.

## TLS Certificates

The deployment uses a local CA to issue TLS serving certificates for PostgreSQL and the MCP server.

### Generating Certificates

```bash
make certs          # Generate CA + serving certs (idempotent, skips existing)
make certs-renew    # Regenerate serving certs (preserves CA)

# Add environment-specific SANs to the MCP cert (e.g., Tailscale, LAN IP):
MCP_EXTRA_SANS="DNS:myhost.example.ts.net,IP:192.168.1.100" make certs
```

Certs are stored in `~/.config/todo/certs/`:

```
ca.key          # CA private key (never mounted into containers)
ca.crt          # CA certificate (distributed to services that need trust)
postgres/       # PostgreSQL serving cert + key
mcp/            # MCP server serving cert + key
```

### Trusting the CA

External clients need to trust the CA to connect to the MCP server over HTTPS.

**curl:**
```bash
curl --cacert ~/.config/todo/certs/ca.crt https://localhost:8082/mcp
```

**System trust store (Fedora/RHEL):**
```bash
sudo cp ~/.config/todo/certs/ca.crt /etc/pki/ca-trust/source/anchors/todo-local-ca.crt
sudo update-ca-trust
```

**System trust store (Debian/Ubuntu):**
```bash
sudo cp ~/.config/todo/certs/ca.crt /usr/local/share/ca-certificates/todo-local-ca.crt
sudo update-ca-certificates
```

**Claude Code MCP (remote HTTP):**
Once the CA is in the system trust store, configure the MCP server as a remote HTTP endpoint. Alternatively, use stdio transport which bypasses TLS entirely.

### Deployment Notes

- PostgreSQL: The `pg-start-ssl.sh` wrapper copies certs to a postgres-owned directory at container startup to handle file permission requirements. The pgvector extension shares PostgreSQL's TLS configuration.
- MCP server: Uses `UserNS=keep-id` to map the host user to container uid 65534 (nobody), preserving the non-root runtime from the Containerfile while allowing cert key access.

## Configuration

Default config path: `~/.todo.yaml`. Override with `--config`.

All settings can also be set via environment variables with `TODO_` prefix:
`TODO_DB_DRIVER=postgres`, `TODO_LOGGING_LEVEL=debug`, etc.
