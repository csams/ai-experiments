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
	Host     string `yaml:"host" mapstructure:"host"`
	Port     int    `yaml:"port" mapstructure:"port"`
	DBName   string `yaml:"dbname" mapstructure:"dbname"`
	User     string `yaml:"user" mapstructure:"user"`
	Password string `yaml:"password" mapstructure:"password"`
	SSLMode  string `yaml:"sslmode" mapstructure:"sslmode"`
}

// PostgresDSN builds a connection string from the Postgres config fields.
func (p PostgresConfig) PostgresDSN() string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		p.Host, p.Port, p.User, p.Password, p.DBName, p.SSLMode,
	)
}

type VectorConfig struct {
	Enabled  bool         `yaml:"enabled" mapstructure:"enabled"`
	Embedder string       `yaml:"embedder" mapstructure:"embedder"`
	Store    string       `yaml:"store" mapstructure:"store"`
	Ollama   OllamaConfig `yaml:"ollama" mapstructure:"ollama"`
	OpenAI   OpenAIConfig `yaml:"openai" mapstructure:"openai"`
	ChromaDB ChromaConfig `yaml:"chromadb" mapstructure:"chromadb"`
}

type OllamaConfig struct {
	Model string `yaml:"model" mapstructure:"model"`
	URL   string `yaml:"url" mapstructure:"url"`
}

type OpenAIConfig struct {
	Model string `yaml:"model" mapstructure:"model"`
}

type ChromaConfig struct {
	URL        string `yaml:"url" mapstructure:"url"`
	Collection string `yaml:"collection" mapstructure:"collection"`
	Tenant     string `yaml:"tenant" mapstructure:"tenant"`
	Database   string `yaml:"database" mapstructure:"database"`
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
	v.SetDefault("vector.store", "chromadb")
	v.SetDefault("vector.ollama.model", "nomic-embed-text")
	v.SetDefault("vector.ollama.url", "http://localhost:11434")
	v.SetDefault("vector.openai.model", "text-embedding-3-small")
	v.SetDefault("vector.chromadb.url", "http://localhost:8000")
	v.SetDefault("vector.chromadb.collection", "todo")
	v.SetDefault("vector.chromadb.tenant", "default_tenant")
	v.SetDefault("vector.chromadb.database", "default_database")

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
		v.AddConfigPath(".")
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
