# Todo Task Tracker — Agent Instructions

## Build & Run

```bash
# Build
go build -o todo .

# Run tests
go test ./... -count=1

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
vectorstore/chromadb/ # ChromaDB implementation
audit/           # Structured audit logger (StoreObserver)
cmd/             # Cobra CLI commands
mcp/             # MCP server and tool definitions
deploy/          # Systemd quadlet files and production config
```

## Key Interfaces

- **`store.Store`** — 26-method interface for all task operations. Implemented by `gormstore.GormStore`.
- **`store.StoreObserver`** — receives `StoreEvent` after mutations. Used by `audit.Logger` and `synced.VectorSyncer`.
- **`store.SemanticSearcher`** — vector similarity search. Implemented by `synced.VectorSyncer`.
- **`embed.Embedder`** — generates vector embeddings. Implementations: `ollama.Embedder`, `openai.Embedder`.
- **`vectorstore.VectorStore`** — vector storage and search. Implementation: `chromadb.Store`.

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

### Available MCP Tools (27 core + 2 optional semantic)

**Tasks:** `create_task`, `list_tasks`, `get_task`, `update_task`, `set_task_state`, `add_blockers`, `remove_blockers`, `archive_task`, `unarchive_task`, `delete_task`, `set_parent`, `unparent`

**Notes:** `add_note`, `update_note`, `list_notes`, `delete_note`

**Links:** `add_link`, `list_links`, `delete_link`

**Tags:** `add_tags`, `remove_tags`

**Bulk:** `bulk_update_state`, `bulk_update_priority`, `bulk_add_tags`, `bulk_remove_tags`

**Search:** `search_tasks`, `search_notes`

**Semantic (when vector enabled):** `semantic_search`, `semantic_search_context`

## Task States

`New` -> `Progressing` -> `Done`

A task can be `Blocked` (via `add_blockers`) or `Unblocked` (auto-transition when all blockers complete).

- Use `set_task_state` for New, Progressing, Unblocked, Done.
- Use `add_blockers` / `remove_blockers` to manage Blocked state.
- Setting Done auto-unblocks dependents with no remaining blockers.

## Priority

Lower number = higher importance. P0 > P1 > P2. Negative priorities are valid.

Blockers are automatically promoted to at least match the priority of tasks they block.

## Subtask Hierarchy

Tasks can be organized in parent-child trees. Use `set_parent` / `unparent`.

- `list_tasks` shows top-level by default. Use `include_subtasks: true` or `parent_id` filter.
- Deleting a parent promotes children. Use `recursive: true` to delete the entire subtree.
- Archiving always cascades to the subtask tree.

## Vector / RAG Setup

Enable in config:

```yaml
vector:
  enabled: true
  embedder: ollama
  store: chromadb
  ollama:
    model: nomic-embed-text
    url: http://localhost:11434
  chromadb:
    url: http://localhost:8000
    collection: todo
```

Requires Ollama and ChromaDB running. Use `todo vector reindex` to build/rebuild the index.

## Configuration

Default config path: `~/.todo.yaml`. Override with `--config`.

All settings can also be set via environment variables with `TODO_` prefix:
`TODO_DB_DRIVER=postgres`, `TODO_LOGGING_LEVEL=debug`, etc.
