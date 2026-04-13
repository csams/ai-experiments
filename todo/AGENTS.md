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

- **`store.Store`** — 27-method interface for all task operations. Implemented by `gormstore.GormStore`.
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

### Available MCP Tools (26 core + 2 optional semantic)

**Tasks:** `create_task`, `create_subtask`, `list_tasks`, `get_task`, `update_task`, `set_task_state`, `add_blockers`, `remove_blockers`, `archive_task`, `unarchive_task`, `delete_task`, `set_parent`, `unparent`

**Notes:** `add_note`, `update_note`, `list_notes`, `delete_note`

**Links:** `add_link`, `list_links`, `delete_link`

**Tags:** `add_tags`, `remove_tags`

**Bulk:** `bulk_update_state`, `bulk_update_priority`, `bulk_add_tags`, `bulk_remove_tags`

**Semantic (when vector enabled):** `semantic_search`, `semantic_search_context`

Semantic search excludes archived items by default. Pass `include_archived: true` (MCP) or `--include-archived` (CLI) to include them.

## Task States

`New` -> `Progressing` -> `Done`

A task can be `Blocked` (via `add_blockers`) or `Unblocked` (auto-transition when all blockers complete).

- Use `set_task_state` for New, Progressing, Unblocked, Done.
- Use `add_blockers` / `remove_blockers` to manage Blocked state.
- Setting Done auto-unblocks dependents with no remaining blockers.

## Priority

Lower number = higher importance. P0 > P1 > P2. Negative priorities are valid.

Blockers are automatically promoted to at least match the priority of tasks they block.

## Validation Rules

- **Title**: required, max 512 characters
- **Description**: optional, no length limit
- **Tags**: alphanumeric, hyphens, and underscores only (`[a-zA-Z0-9_-]+`), max 100 chars per tag, max 50 tags per task
- **Notes**: required non-empty text, no length limit
- **Links**: URL required, max 2000 characters
- **Search queries**: max 500 characters
- **Bulk operations**: max 100 IDs per call

## Subtask Hierarchy

Tasks can be organized in parent-child trees. Use `set_parent` / `unparent`.

- `list_tasks` shows top-level by default. Use `include_subtasks: true` or `parent_id` filter.
- Deleting a parent promotes children. Use `recursive: true` to delete the entire subtree.
- Archiving always cascades to the subtask tree.

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

Requires Ollama running and PostgreSQL with the pgvector extension (`pgvector/pgvector:pg16` image). Use `todo vector reindex` to build/rebuild the index.

Vector search is only available with the PostgreSQL backend. When using SQLite, vector search is automatically disabled.

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
