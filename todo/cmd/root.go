package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/csams/todo/audit"
	"github.com/csams/todo/config"
	"github.com/csams/todo/embed"
	embedollama "github.com/csams/todo/embed/ollama"
	embedopenai "github.com/csams/todo/embed/openai"
	"github.com/csams/todo/store"
	"github.com/csams/todo/store/gormstore"
	"github.com/csams/todo/store/synced"
	"github.com/csams/todo/vectorstore/chromadb"
	"github.com/spf13/cobra"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

var (
	cfgFile       string
	dbPath        string
	noVector      bool
	jsonOutput    bool
	cfg           *config.Config
	logger        *slog.Logger
	vectorSyncer  *synced.VectorSyncer
	semanticSearch store.SemanticSearcher
)

var rootCmd = &cobra.Command{
	Use:   "todo",
	Short: "A task tracking system with CLI and MCP server",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		var err error
		cfg, err = config.Load(cfgFile)
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		// CLI flag overrides
		if cmd.Flags().Changed("db") {
			cfg.DB.DSN = dbPath
		}

		// Setup logger
		logger = setupLogger(cfg.Logging)
		return nil
	},
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default: ~/.todo.yaml)")
	rootCmd.PersistentFlags().StringVar(&dbPath, "db", "", "SQLite database path (overrides config)")
	rootCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "output as JSON")
	rootCmd.PersistentFlags().BoolVar(&noVector, "no-vector", false, "disable vector sync even if configured")
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}

// openStore creates a GormStore from the current config, registers the audit logger,
// and returns the store. The caller must call Close() on the returned store.
func openStore() (store.Store, *gormstore.GormStore, error) {
	dsn := cfg.DB.DSN
	if strings.HasPrefix(dsn, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, nil, fmt.Errorf("resolving home directory: %w", err)
		}
		dsn = filepath.Join(home, dsn[2:])
	}

	var db *gorm.DB
	var err error

	gormCfg := &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	}

	switch cfg.DB.Driver {
	case "postgres":
		pgDSN := cfg.DB.Postgres.PostgresDSN()
		db, err = gorm.Open(postgres.Open(pgDSN), gormCfg)
		if err != nil {
			return nil, nil, fmt.Errorf("opening postgres: %w", err)
		}
		sqlDB, err := db.DB()
		if err != nil {
			return nil, nil, fmt.Errorf("getting postgres pool: %w", err)
		}
		sqlDB.SetMaxOpenConns(25)
		sqlDB.SetMaxIdleConns(5)
		sqlDB.SetConnMaxLifetime(5 * time.Minute)
	default: // sqlite
		db, err = gorm.Open(sqlite.Open(dsn+"?_journal_mode=WAL&_busy_timeout=5000"), gormCfg)
		if err != nil {
			return nil, nil, fmt.Errorf("opening sqlite: %w", err)
		}
		sqlDB, err := db.DB()
		if err != nil {
			return nil, nil, fmt.Errorf("getting underlying DB: %w", err)
		}
		sqlDB.SetMaxOpenConns(1)
		if _, err := sqlDB.Exec("PRAGMA foreign_keys = ON"); err != nil {
			return nil, nil, fmt.Errorf("enabling foreign keys: %w", err)
		}
	}

	gs, err := gormstore.New(db)
	if err != nil {
		return nil, nil, fmt.Errorf("initializing store: %w", err)
	}

	// Register audit logger if enabled
	if cfg.Logging.Audit {
		gs.AddObserver(audit.NewLogger(logger))
	}

	// Setup vector sync if enabled and not disabled
	if cfg.Vector.Enabled && !noVector {
		if err := setupVector(gs); err != nil {
			logger.Warn("vector sync disabled", "error", err)
		}
	}

	return gs, gs, nil
}

func setupVector(gs *gormstore.GormStore) error {
	// Create embedder
	var emb embed.Embedder
	var err error
	switch cfg.Vector.Embedder {
	case "openai":
		emb, err = embedopenai.New(cfg.Vector.OpenAI.Model)
	default: // ollama
		emb, err = embedollama.New(cfg.Vector.Ollama.URL, cfg.Vector.Ollama.Model)
	}
	if err != nil {
		return fmt.Errorf("embedder: %w", err)
	}

	// Create vector store
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	vs, err := chromadb.New(ctx,
		cfg.Vector.ChromaDB.URL,
		cfg.Vector.ChromaDB.Collection,
		cfg.Vector.ChromaDB.Tenant,
		cfg.Vector.ChromaDB.Database,
		cfg.Vector.ChromaDB.AuthToken,
	)
	if err != nil {
		return fmt.Errorf("vector store: %w", err)
	}

	// Check dimension mismatch
	storedModel, storedDims, _ := vs.CollectionInfo(ctx)
	if storedModel != "" && storedDims > 0 {
		if storedDims != emb.Dimensions() {
			logger.Warn("vector store dimension mismatch",
				"stored_model", storedModel,
				"stored_dims", storedDims,
				"current_model", emb.ModelName(),
				"current_dims", emb.Dimensions(),
			)
			return fmt.Errorf("dimension mismatch: stored %s (%d dims) vs current %s (%d dims) — run 'todo vector reindex --clear'",
				storedModel, storedDims, emb.ModelName(), emb.Dimensions())
		}
	}

	vs2 := synced.New(vs, emb, gs, logger)
	gs.AddObserver(vs2)
	vectorSyncer = vs2
	semanticSearch = vs2

	return nil
}

// getSemanticSearcher returns the semantic searcher, or nil if not configured.
func getSemanticSearcher() store.SemanticSearcher {
	return semanticSearch
}

// getVectorSyncer returns the vector syncer, or nil if not configured.
func getVectorSyncer() *synced.VectorSyncer {
	return vectorSyncer
}

func setupLogger(logCfg config.LogConfig) *slog.Logger {
	var level slog.Level
	switch strings.ToLower(logCfg.Level) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler

	output := os.Stderr
	if logCfg.Output != "" && logCfg.Output != "stderr" {
		// File handle intentionally not closed — it remains open for the process
		// lifetime and is reclaimed by the OS on exit.
		f, err := os.OpenFile(logCfg.Output, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err == nil {
			output = f
		} else {
			fmt.Fprintf(os.Stderr, "WARNING: could not open log file %q: %v (falling back to stderr)\n", logCfg.Output, err)
		}
	}

	if strings.ToLower(logCfg.Format) == "text" {
		handler = slog.NewTextHandler(output, opts)
	} else {
		handler = slog.NewJSONHandler(output, opts)
	}

	return slog.New(handler)
}
