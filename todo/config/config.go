package config

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

type Config struct {
	DB      DBConfig      `yaml:"db" mapstructure:"db"`
	Vector  VectorConfig  `yaml:"vector" mapstructure:"vector"`
	Logging LogConfig     `yaml:"logging" mapstructure:"logging"`
	MCP     MCPConfig     `yaml:"mcp" mapstructure:"mcp"`
}

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
	masked := "***"
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

type VectorConfig struct {
	Enabled  bool           `yaml:"enabled" mapstructure:"enabled"`
	Embedder string         `yaml:"embedder" mapstructure:"embedder"`
	Store    string         `yaml:"store" mapstructure:"store"`
	Ollama   OllamaConfig   `yaml:"ollama" mapstructure:"ollama"`
	OpenAI   OpenAIConfig   `yaml:"openai" mapstructure:"openai"`
	PgVector PgVectorConfig `yaml:"pgvector" mapstructure:"pgvector"`
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
}

type MCPConfig struct {
	Transport string `yaml:"transport" mapstructure:"transport"`
	Addr      string `yaml:"addr" mapstructure:"addr"`
	APIKey    string `yaml:"api_key" mapstructure:"api_key" json:"-"`
	TLSCert   string `yaml:"tls_cert" mapstructure:"tls_cert"`
	TLSKey    string `yaml:"tls_key" mapstructure:"tls_key"`
}

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

	v.SetDefault("logging.level", "info")
	v.SetDefault("logging.format", "json")
	v.SetDefault("logging.output", "stderr")
	v.SetDefault("logging.audit", true)

	v.SetDefault("mcp.transport", "stdio")
	v.SetDefault("mcp.addr", ":8080")

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
