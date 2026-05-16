package config

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Mask is the redaction marker substituted for sensitive fields in
// String / LogValue output. Centralized so tests can pin the literal
// without scattering "***" across the codebase.
const Mask = "***"

type Config struct {
	DB      DBConfig     `yaml:"db" mapstructure:"db"`
	Vector  VectorConfig `yaml:"vector" mapstructure:"vector"`
	Logging LogConfig    `yaml:"logging" mapstructure:"logging"`
	MCP     MCPConfig    `yaml:"mcp" mapstructure:"mcp"`
}

// String renders the config in a form safe to log: sensitive fields
// (`db.postgres.password`, `mcp.api_key`) are masked. Implemented by
// formatting a `shadowConfig` type so we don't re-enter this method
// recursively. Nested struct fields whose types have their own Stringer
// (PostgresConfig, MCPConfig) still get their masked output because
// %+v honors Stringer on field values.
//
// Why this exists: every other field gets its zero-effort `%+v` dump
// from fmt's reflection, but a stray `log.Printf("%+v", cfg)` or a
// debugger snapshot would otherwise leak the DB password and MCP API
// key. Stringer + LogValuer close that gap whether the caller uses fmt
// or slog.
//
// IMPORTANT: this method relies on field-level Stringers to mask. If
// you add a sensitive field directly on Config (not nested under a
// type with its own masking Stringer), it will leak through the
// reflection walk in the shadowConfig formatter. Sensitive fields
// must live on a nested type that masks itself in its own String().
//
// Note: fmt verbs that bypass Stringer (`%#v` / GoStringer) are NOT
// masked — those verbs are debug-only and not used by slog or any
// logging path in this codebase. If that ever changes, add a
// GoString() method too.
func (c Config) String() string {
	return fmt.Sprintf("%+v", shadowConfig(c))
}

// LogValue routes slog through the same masked rendering used by
// fmt — both handlers (text and JSON) end up storing the redacted
// string instead of recursing into the struct fields. Keeps the
// implementation single-sourced.
func (c Config) LogValue() slog.Value {
	return slog.StringValue(c.String())
}

// shadowConfig has the same fields as Config but no methods, so
// formatting it doesn't recurse into our String/LogValue.
type shadowConfig Config

type DBConfig struct {
	Driver   string         `yaml:"driver" mapstructure:"driver"`
	DSN      string         `yaml:"dsn" mapstructure:"dsn"`
	Postgres PostgresConfig `yaml:"postgres" mapstructure:"postgres"`
}

type PostgresConfig struct {
	Host        string `yaml:"host" mapstructure:"host"`
	Port        int    `yaml:"port" mapstructure:"port"`
	DBName      string `yaml:"dbname" mapstructure:"dbname"`
	User        string `yaml:"user" mapstructure:"user"`
	Password    string `yaml:"password" mapstructure:"password" json:"-"`
	SSLMode     string `yaml:"sslmode" mapstructure:"sslmode"`
	SSLRootCert string `yaml:"sslrootcert" mapstructure:"sslrootcert"`
	SSLCert     string `yaml:"sslcert" mapstructure:"sslcert"`
	SSLKey      string `yaml:"sslkey" mapstructure:"sslkey"`
}

// PostgresDSN builds a connection string from the Postgres config fields.
// WARNING: The returned string contains the plaintext password. Do not log it.
func (p PostgresConfig) PostgresDSN() string {
	dsn := fmt.Sprintf(
		"host=%s port=%d user=%s password='%s' dbname=%s sslmode=%s",
		p.Host, p.Port, p.User, quoteLibpq(p.Password), p.DBName, p.SSLMode,
	)
	if p.SSLMode != "" && p.SSLMode != "disable" {
		if p.SSLRootCert != "" {
			dsn += fmt.Sprintf(" sslrootcert='%s'", quoteLibpq(p.SSLRootCert))
		}
		if p.SSLCert != "" {
			dsn += fmt.Sprintf(" sslcert='%s'", quoteLibpq(p.SSLCert))
		}
		if p.SSLKey != "" {
			dsn += fmt.Sprintf(" sslkey='%s'", quoteLibpq(p.SSLKey))
		}
	}
	return dsn
}

// quoteLibpq escapes a value for use inside single quotes in a libpq connection string.
func quoteLibpq(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	return s
}

// String returns the DSN with the password masked, suitable for logging.
func (p PostgresConfig) String() string {
	masked := Mask
	if p.Password == "" {
		masked = ""
	}
	s := fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		p.Host, p.Port, p.User, masked, p.DBName, p.SSLMode,
	)
	if p.SSLMode != "" && p.SSLMode != "disable" {
		if p.SSLRootCert != "" {
			s += fmt.Sprintf(" sslrootcert=%s", p.SSLRootCert)
		}
		if p.SSLCert != "" {
			s += fmt.Sprintf(" sslcert=%s", p.SSLCert)
		}
		if p.SSLKey != "" {
			s += fmt.Sprintf(" sslkey=%s", p.SSLKey)
		}
	}
	return s
}

// LogValue mirrors String for slog. Returns the same masked DSN string.
func (p PostgresConfig) LogValue() slog.Value {
	return slog.StringValue(p.String())
}

type VectorConfig struct {
	Enabled  bool           `yaml:"enabled" mapstructure:"enabled"`
	Embedder string         `yaml:"embedder" mapstructure:"embedder"`
	Store    string         `yaml:"store" mapstructure:"store"`
	Ollama   OllamaConfig   `yaml:"ollama" mapstructure:"ollama"`
	OpenAI   OpenAIConfig   `yaml:"openai" mapstructure:"openai"`
	PgVector PgVectorConfig `yaml:"pgvector" mapstructure:"pgvector"`

	// Reconciler — applies only under `todo mcp` (the long-lived process).
	// On each tick the reconciler drains up to ReconcileBatchSize dirty
	// entities (tasks + notes) and re-embeds them. The dirty flag is
	// cleared on successful re-embed.
	ReconcileInterval  time.Duration `yaml:"reconcile_interval" mapstructure:"reconcile_interval"`
	ReconcileBatchSize int           `yaml:"reconcile_batch_size" mapstructure:"reconcile_batch_size"`
}

type OllamaConfig struct {
	Model string `yaml:"model" mapstructure:"model"`
	URL   string `yaml:"url" mapstructure:"url"`
}

type OpenAIConfig struct {
	Model string `yaml:"model" mapstructure:"model"`
}

type PgVectorConfig struct {
	// Reserved for future pgvector-specific tuning (e.g., index type, HNSW params).
}

type LogConfig struct {
	Level  string `yaml:"level" mapstructure:"level"`
	Format string `yaml:"format" mapstructure:"format"`
	Output string `yaml:"output" mapstructure:"output"`
	Audit  bool   `yaml:"audit" mapstructure:"audit"`

	// AuditValueCap is the per-string-value rune cap applied to
	// Change.Old / Change.New entries in audit log records. 0 falls
	// back to audit.DefaultValueCap (256). Has no effect when
	// AuditFullValues is true. Non-string values (ints, due-date
	// pointers, task ID slices) pass through regardless of this cap.
	AuditValueCap int `yaml:"audit_value_cap" mapstructure:"audit_value_cap"`

	// AuditFullValues disables truncation entirely. Off by default —
	// audit logs capture structural changes (state, priority, IDs)
	// plus truncated previews of free-text fields. Set true if the
	// audit log is your source of truth for content changes and you
	// accept multi-MB log lines for large description edits.
	AuditFullValues bool `yaml:"audit_full_values" mapstructure:"audit_full_values"`
}

type MCPConfig struct {
	Transport string `yaml:"transport" mapstructure:"transport"`
	Addr      string `yaml:"addr" mapstructure:"addr"`

	// APIKey is the legacy single-key form (one key, attributed in audit
	// logs as actor="default"). When both APIKey and APIKeys are set,
	// startup is refused — pick one. Prefer APIKeys for any new config.
	APIKey string `yaml:"api_key" mapstructure:"api_key" json:"-"`

	// APIKeys maps a label → key. The bearer-auth middleware accepts
	// any matching key and stamps the corresponding label onto every
	// authenticated request's audit events (see store.SetActorContext).
	// Use labels like client / user names to attribute mutations in a
	// multi-tenant deployment.
	//
	// Env-var configuration is awkward for maps; for single-key
	// deployments stay with TODO_MCP_API_KEY (which still populates the
	// legacy APIKey field). For multi-key deployments use a config file.
	APIKeys map[string]string `yaml:"api_keys" mapstructure:"api_keys" json:"-"`

	TLSCert string `yaml:"tls_cert" mapstructure:"tls_cert"`
	TLSKey  string `yaml:"tls_key" mapstructure:"tls_key"`

	// HTTP transport hardening knobs. All apply only when Transport == "http".

	// MaxBodyBytes caps the request body size in bytes. 0 disables the cap (not
	// recommended on a public-facing server — an unauthenticated or malicious
	// client can otherwise force unbounded buffering via io.ReadAll on the
	// MCP request body). Default 8 MiB.
	MaxBodyBytes int64 `yaml:"max_body_bytes" mapstructure:"max_body_bytes"`

	// ReadTimeout bounds the full request read (headers + body). 0 disables.
	// Default 30s — generous for sane JSON-RPC requests; bounds Slowloris-style
	// trickle-body attacks. ReadHeaderTimeout is held at 10s independently.
	ReadTimeout time.Duration `yaml:"read_timeout" mapstructure:"read_timeout"`

	// WriteTimeout bounds the full response write. 0 disables — that is the
	// default because the MCP streamable-HTTP transport keeps SSE streams
	// open across multi-second tool calls and a tight WriteTimeout would cut
	// them off mid-flight. Set explicitly only if you understand the trade.
	WriteTimeout time.Duration `yaml:"write_timeout" mapstructure:"write_timeout"`
}

// ResolveAPIKeys returns the effective label→key map after applying
// the legacy/single-tenant back-compat: an APIKey-only config resolves
// to {"default": APIKey}, an APIKeys-only config resolves to itself
// (a copy, to keep callers from mutating m), and a config setting
// both at once is rejected so the operator picks an intent. Returns
// (nil, nil) when neither is set — caller treats that as no auth.
func (m MCPConfig) ResolveAPIKeys() (map[string]string, error) {
	if m.APIKey != "" && len(m.APIKeys) > 0 {
		return nil, fmt.Errorf("mcp.api_key and mcp.api_keys are mutually exclusive (api_key is the legacy single-tenant form; pick one)")
	}
	if m.APIKey != "" {
		return map[string]string{"default": m.APIKey}, nil
	}
	if len(m.APIKeys) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(m.APIKeys))
	for label, key := range m.APIKeys {
		out[label] = key
	}
	return out, nil
}

// String returns the config with APIKey / APIKeys masked, suitable for
// logging. Adding a new sensitive field to MCPConfig requires updating
// this method (mask the field on the shadow before formatting).
func (m MCPConfig) String() string {
	masked := shadowMCP(m)
	if masked.APIKey != "" {
		masked.APIKey = Mask
	}
	if len(masked.APIKeys) > 0 {
		// Keep the label keys (operationally useful — tells the
		// operator which clients are configured) but mask each value.
		redacted := make(map[string]string, len(masked.APIKeys))
		for label := range masked.APIKeys {
			redacted[label] = Mask
		}
		masked.APIKeys = redacted
	}
	return fmt.Sprintf("%+v", masked)
}

// LogValue mirrors String for slog.
func (m MCPConfig) LogValue() slog.Value {
	return slog.StringValue(m.String())
}

// shadowMCP exists so MCPConfig.String can fmt.%+v without re-entering
// itself via the Stringer method set.
type shadowMCP MCPConfig

// Load reads config from a YAML file, environment variables, and applies defaults.
// configPath may be empty to use the default path (~/.todo.yaml).
func Load(configPath string) (*Config, error) {
	v := viper.New()

	// Defaults
	v.SetDefault("db.driver", "sqlite")
	v.SetDefault("db.dsn", "~/.todo.db")
	v.SetDefault("db.postgres.host", "localhost")
	v.SetDefault("db.postgres.port", 5432)
	v.SetDefault("db.postgres.dbname", "todo")
	v.SetDefault("db.postgres.user", "todo")
	v.SetDefault("db.postgres.sslmode", "disable")

	v.SetDefault("vector.enabled", false)
	v.SetDefault("vector.embedder", "ollama")
	v.SetDefault("vector.store", "pgvector")
	v.SetDefault("vector.ollama.model", "nomic-embed-text")
	v.SetDefault("vector.ollama.url", "http://localhost:11434")
	v.SetDefault("vector.openai.model", "text-embedding-3-small")
	v.SetDefault("vector.reconcile_interval", "30s")
	v.SetDefault("vector.reconcile_batch_size", 100)

	v.SetDefault("logging.level", "info")
	v.SetDefault("logging.format", "json")
	v.SetDefault("logging.output", "stderr")
	v.SetDefault("logging.audit", true)
	v.SetDefault("logging.audit_value_cap", 256)
	v.SetDefault("logging.audit_full_values", false)

	v.SetDefault("mcp.transport", "stdio")
	v.SetDefault("mcp.addr", ":8080")
	v.SetDefault("mcp.max_body_bytes", 8*1024*1024) // 8 MiB
	v.SetDefault("mcp.read_timeout", "30s")
	v.SetDefault("mcp.write_timeout", "0s") // disabled: SSE streams must not be capped

	// Config file
	if configPath != "" {
		v.SetConfigFile(configPath)
	} else {
		v.SetConfigName(".todo")
		v.SetConfigType("yaml")
		v.AddConfigPath("$HOME")
	}

	// Environment variables: TODO_DB_DRIVER, TODO_LOGGING_LEVEL, etc.
	v.SetEnvPrefix("TODO")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Read config file (ignore "not found" — defaults are sufficient)
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("reading config: %w", err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	return &cfg, nil
}
