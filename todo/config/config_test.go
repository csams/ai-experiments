package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

const secretPassword = "PG-PASSWORD-SHOULD-NEVER-LEAK"
const secretAPIKey = "MCP-API-KEY-SHOULD-NEVER-LEAK"

// populatedConfig builds a Config with every sensitive field set to a
// distinctive sentinel so tests can grep the rendered output for any
// leaks.
func populatedConfig() Config {
	return Config{
		DB: DBConfig{
			Driver: "postgres",
			DSN:    "/tmp/todo.db",
			Postgres: PostgresConfig{
				Host:        "localhost",
				Port:        5432,
				DBName:      "todo",
				User:        "todo",
				Password:    secretPassword,
				SSLMode:     "verify-ca",
				SSLRootCert: "/etc/todo/certs/ca.crt",
			},
		},
		MCP: MCPConfig{
			Transport: "http",
			Addr:      ":8080",
			APIKey:    secretAPIKey,
			TLSCert:   "/etc/todo/certs/mcp/server.crt",
			TLSKey:    "/etc/todo/certs/mcp/server.key",
		},
	}
}

// assertNoSecrets fails the test if any sensitive literal appears in s.
func assertNoSecrets(t *testing.T, label, s string) {
	t.Helper()
	if strings.Contains(s, secretPassword) {
		t.Errorf("%s leaked DB password: %q", label, s)
	}
	if strings.Contains(s, secretAPIKey) {
		t.Errorf("%s leaked MCP API key: %q", label, s)
	}
}

func TestConfig_StringMasksSecrets(t *testing.T) {
	cfg := populatedConfig()
	// `%s` and `%v` invoke Stringer.
	assertNoSecrets(t, "fmt.%s", fmt.Sprintf("%s", cfg))
	assertNoSecrets(t, "fmt.%v", fmt.Sprintf("%v", cfg))
	// `%+v` honors Stringer too (calls String() and prefixes nothing — the
	// field-name expansion only kicks in when Stringer is NOT implemented).
	assertNoSecrets(t, "fmt.%+v", fmt.Sprintf("%+v", cfg))
}

func TestConfig_StringContainsMaskMarker(t *testing.T) {
	cfg := populatedConfig()
	out := cfg.String()
	if !strings.Contains(out, Mask) {
		t.Errorf("String output missing mask marker %q; got %q", Mask, out)
	}
}

func TestPostgresConfig_StringMasksPassword(t *testing.T) {
	pg := populatedConfig().DB.Postgres
	s := pg.String()
	if strings.Contains(s, secretPassword) {
		t.Errorf("PostgresConfig.String leaked password: %q", s)
	}
	if !strings.Contains(s, "password="+Mask) {
		t.Errorf("expected `password=***` in masked DSN, got %q", s)
	}
}

func TestPostgresConfig_StringEmptyPasswordRendersEmpty(t *testing.T) {
	// When no password is configured the Stringer should emit
	// `password=` rather than `password=***` — the existing contract.
	pg := PostgresConfig{Host: "h", Port: 5432, DBName: "d", User: "u", SSLMode: "disable"}
	s := pg.String()
	if strings.Contains(s, Mask) {
		t.Errorf("empty password should not render the mask marker; got %q", s)
	}
}

func TestMCPConfig_StringMasksAPIKey(t *testing.T) {
	m := populatedConfig().MCP
	s := m.String()
	if strings.Contains(s, secretAPIKey) {
		t.Errorf("MCPConfig.String leaked APIKey: %q", s)
	}
	if !strings.Contains(s, Mask) {
		t.Errorf("expected mask marker in MCPConfig.String, got %q", s)
	}
}

func TestMCPConfig_StringEmptyAPIKeyOmitsMask(t *testing.T) {
	m := MCPConfig{Transport: "stdio"}
	if strings.Contains(m.String(), Mask) {
		t.Errorf("empty APIKey should not render the mask marker; got %q", m.String())
	}
}

func TestConfig_LogValuerWithSlog(t *testing.T) {
	cfg := populatedConfig()

	// Text handler — sensitive literals must not appear in the rendered
	// log line.
	var textBuf bytes.Buffer
	tlog := slog.New(slog.NewTextHandler(&textBuf, nil))
	tlog.Info("config loaded", "cfg", cfg)
	assertNoSecrets(t, "slog text handler", textBuf.String())

	// JSON handler — sensitive literals must not appear in any JSON
	// field value.
	var jsonBuf bytes.Buffer
	jlog := slog.New(slog.NewJSONHandler(&jsonBuf, nil))
	jlog.Info("config loaded", "cfg", cfg)
	assertNoSecrets(t, "slog JSON handler", jsonBuf.String())

	// Confirm the JSON output is well-formed and the masked Config is
	// stored as a string value (not a missing field, not an empty
	// object — that would silently hide leaks behind a serializer
	// quirk).
	var entry map[string]any
	if err := json.Unmarshal(jsonBuf.Bytes(), &entry); err != nil {
		t.Fatalf("JSON log line not parseable: %v\nraw: %s", err, jsonBuf.String())
	}
	cfgField, ok := entry["cfg"].(string)
	if !ok {
		t.Fatalf("cfg field missing or wrong type in JSON log; entry=%v", entry)
	}
	if !strings.Contains(cfgField, Mask) {
		t.Errorf("masked cfg JSON value did not contain mask marker; got %q", cfgField)
	}
}

// TestConfig_LeakRegressionAcrossNestedTypes — the top-level Config
// String renders via shadowConfig (no Stringer) so nested struct types
// (DBConfig, etc.) are formatted by reflection. Their *fields* must
// still get the masked Stringer treatment. Walk through MCP and
// DB.Postgres explicitly to confirm the recursion does the right
// thing.
func TestConfig_LeakRegressionAcrossNestedTypes(t *testing.T) {
	cfg := populatedConfig()
	out := cfg.String()

	// Both sentinel strings should be entirely absent.
	if strings.Contains(out, secretPassword) {
		t.Errorf("DB password leaked in Config.String: %q", out)
	}
	if strings.Contains(out, secretAPIKey) {
		t.Errorf("MCP APIKey leaked in Config.String: %q", out)
	}
	// And both should be replaced by the mask.
	maskCount := strings.Count(out, Mask)
	if maskCount < 2 {
		t.Errorf("expected at least 2 mask markers in Config.String (one per leaked field), got %d: %q", maskCount, out)
	}
}
