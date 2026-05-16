package cmd

import (
	"strings"
	"testing"
)

// TestNoteListFlags_AllIsDeprecatedButPresent — the --all flag is kept
// for one release with cobra's MarkDeprecated so existing scripts don't
// break overnight. It is hidden from help but still resolvable. After
// the deprecation window it should be removed.
func TestNoteListFlags_AllIsDeprecatedButPresent(t *testing.T) {
	flag := noteListCmd.Flags().Lookup("all")
	if flag == nil {
		t.Fatal("--all flag missing; deprecation window not yet expired so it should still be present")
	}
	if flag.Deprecated == "" {
		t.Error("--all should carry a Deprecated message")
	}
	if !flag.Hidden {
		t.Error("deprecated --all should be hidden from help output")
	}
	// The deprecation message should redirect users to the real
	// narrowing flags — otherwise the warning is "this flag is
	// deprecated, good luck."
	for _, want := range []string{"--standalone", "--attached"} {
		if !strings.Contains(flag.Deprecated, want) {
			t.Errorf("deprecation message should point at %q; got %q", want, flag.Deprecated)
		}
	}
}

// TestNoteListFlags_StandaloneAndAttachedRegistered — the two narrowing
// flags should both be registered and visible in help output.
func TestNoteListFlags_StandaloneAndAttachedRegistered(t *testing.T) {
	for _, name := range []string{"standalone", "attached"} {
		f := noteListCmd.Flags().Lookup(name)
		if f == nil {
			t.Errorf("--%s flag missing", name)
			continue
		}
		if f.Hidden {
			t.Errorf("--%s should be visible in help", name)
		}
		if f.Deprecated != "" {
			t.Errorf("--%s should not be deprecated", name)
		}
	}
}
