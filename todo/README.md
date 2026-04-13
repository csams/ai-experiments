# Todo — Task Tracking System

A task tracking system designed for both CLI use and AI agent access via MCP (Model Context Protocol). Features prioritized tasks with states, blocking dependencies, subtask hierarchies, notes, links, tags, and optional vector-based semantic search.

## Features

- **Task lifecycle**: New, Progressing, Blocked, Unblocked, Done
- **Blocking dependencies**: many-to-many, cycle detection, automatic priority propagation
- **Subtask hierarchy**: parent/child trees with recursive operations
- **Notes**: arbitrary text annotations on tasks
- **Links**: JIRA issues, Git PRs, and arbitrary URLs
- **Tags**: categorization with AND-logic filtering
- **Due dates**: with overdue filtering and sorting
- **Archive**: soft-delete with subtree cascade
- **Bulk operations**: state, priority, and tag changes across multiple tasks
- **Full-text search**: tasks and notes
- **Semantic search** (optional): vector similarity via Ollama/OpenAI + pgvector (PostgreSQL)
- **MCP server**: 28 tools for AI agent access (26 core + 2 semantic search; stdio + HTTP transports)
- **Structured audit logging**: all mutations logged with before/after state
- **YAML configuration**: with env var overrides
- **Pluggable storage**: SQLite (default) or PostgreSQL via GORM
- **Container deployment**: Containerfile, podman compose, systemd quadlets

## Quick Start

```bash
# Build
go build -o todo .

# Create tasks
./todo task create "Fix login bug" --priority 1 --due 2026-04-20 --tag backend --tag urgent
./todo task create "Update documentation" --priority 3
./todo task create "Refactor auth module" --priority 2 -d "Extract auth into separate package"

# List tasks (top-level, sorted by priority)
./todo task list

# Show full task detail
./todo task show 1

# State management
./todo task state 1 progressing
./todo task state 1 done

# Blocking
./todo task block 2 --by 1,3         # task 2 blocked by tasks 1 and 3
./todo task unblock 2 --by 1         # remove blocker

# Subtasks
./todo task parent 4 1               # task 4 becomes subtask of task 1
./todo task unparent 4               # make top-level again

# Notes
./todo note add 1 "Checked logs, token expires after 5 min"
./todo note update 1 1 "Updated: token TTL is 5 min, should be 30"
./todo note list 1
./todo note search "token"

# Links (type auto-detected)
./todo link add 1 "AUTH-456"                                    # jira
./todo link add 1 "https://github.com/org/repo/pull/42"        # pr
./todo link add 1 "https://wiki.example.com/auth-design" --type url

# Tags
./todo task tag 1 backend urgent
./todo task untag 1 urgent

# Archive / unarchive (cascades to subtree)
./todo task archive 1
./todo task unarchive 1

# Delete
./todo task delete 1                  # promotes subtasks
./todo task delete 1 --recursive      # deletes subtask tree

# Search
./todo task search "auth"
./todo note search "token"

# Filtering
./todo task list --state blocked
./todo task list --tag backend --tag urgent
./todo task list --overdue
./todo task list --parent 1 --state blocked   # blocked tasks in subtree
./todo task list --all                        # include archived
./todo task list --sort due

# Bulk operations
./todo task bulk-state --state progressing 1 2 3
./todo task bulk-priority --priority 0 1 2 3
./todo task bulk-add-tags --tags backend,v2 1 2 3
./todo task bulk-remove-tags --tags wip 1 2 3

# JSON output
./todo task list --json
./todo task show 1 --json
```

## MCP Server

The MCP server exposes all task tracking operations as tools that AI agents (like Claude) can call.

### stdio transport (Claude Code / Claude Desktop)

```bash
./todo mcp --db ~/.todo.db
```

Add to Claude Code MCP settings:

```json
{
  "mcpServers": {
    "todo": {
      "command": "/path/to/todo",
      "args": ["mcp", "--db", "/home/you/.todo.db"]
    }
  }
}
```

### HTTP transport (multi-client)

```bash
./todo mcp --transport http --addr :8080
```

## Configuration

Default config path: `~/.todo.yaml`. Override with `--config`.

```yaml
db:
  driver: sqlite                          # sqlite or postgres
  dsn: "~/.todo.db"
  postgres:
    host: localhost
    port: 5432
    dbname: todo
    user: todo
    password: ""                          # prefer TODO_DB_POSTGRES_PASSWORD env var
    sslmode: disable

vector:
  enabled: false                          # opt-in; requires PostgreSQL with pgvector
  embedder: ollama                        # ollama or openai
  store: pgvector
  ollama:
    model: nomic-embed-text
    url: http://localhost:11434
  openai:
    model: text-embedding-3-small         # requires OPENAI_API_KEY env

logging:
  level: info                             # debug, info, warn, error
  format: json                            # json or text
  output: stderr                          # stderr or file path
  audit: true                             # log all mutations

mcp:
  transport: stdio                        # stdio or http
  addr: ":8080"
  api_key: ""                             # optional; set via TODO_MCP_API_KEY
  tls_cert: ""                            # path to TLS certificate
  tls_key: ""                             # path to TLS key
```

All settings can be set via environment variables with `TODO_` prefix and `_` separators:

```bash
TODO_DB_DRIVER=postgres TODO_DB_POSTGRES_HOST=db.example.com ./todo task list
```

## Semantic Search (Optional)

Requires [Ollama](https://ollama.ai) and PostgreSQL with the [pgvector](https://github.com/pgvector/pgvector) extension (use the `pgvector/pgvector:pg16` container image).

```bash
# Start dependencies
ollama pull nomic-embed-text

# Enable in config or via env (requires db.driver=postgres)
TODO_VECTOR_ENABLED=true ./todo task list

# Build/rebuild vector index
./todo vector reindex
./todo vector reindex --clear    # required when switching embedding model

# Semantic search
./todo search semantic "authentication issues"
./todo search semantic "auth" --type note
./todo search context 1          # find items related to task 1
```

## Deployment

### Podman Compose

```bash
# Create .env with secrets
cat > .env <<EOF
PG_PASSWORD=changeme
MCP_API_KEY=changeme
EOF

# Start all services (postgres, todo-mcp)
podman compose up -d

# MCP HTTP server available at http://localhost:8080
```

### Systemd Quadlets

For production deployment on systemd hosts:

```bash
# Create podman secrets
echo -n 'changeme' | podman secret create todo-pg-password -
echo -n 'changeme' | podman secret create todo-mcp-api-key -

# Deploy (builds image, generates TLS certs, copies quadlet files, starts services)
make deploy

# Start
systemctl --user daemon-reload
systemctl --user start todo-mcp
systemctl --user status todo-mcp

# MCP HTTP server available at http://localhost:8082
journalctl --user -u todo-mcp -f
```

## Development

```bash
# Run tests
go test ./... -count=1

# Run tests verbose
go test ./store/gormstore/... -v

# Build
go build -o todo .

# Lint
go vet ./...
```

## Architecture

```
model/              Task, Note, Link structs + error types
config/             YAML config with viper
store/              Store interface (27 methods)
store/gormstore/    GORM implementation (SQLite + PostgreSQL)
store/synced/       VectorSyncer (StoreObserver + SemanticSearcher)
embed/              Embedder interface + Ollama/OpenAI implementations
vectorstore/        VectorStore interface + pgvector implementation
audit/              Structured audit logger (StoreObserver)
cmd/                Cobra CLI (20 command files)
mcp/                MCP server (8 files, 28 tools)
deploy/             Container + quadlet deployment files
```

The store layer uses an **observer pattern**: `GormStore` emits `StoreEvent` after each mutation. Observers (`audit.Logger`, `synced.VectorSyncer`) handle logging and vector sync without wrapping the store interface.
