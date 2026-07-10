package cli

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestCommandSurface(t *testing.T) {
	e := &env{out: &bytes.Buffer{}, err: &bytes.Buffer{}}
	root := newRootCmd(e, "0.1.0-test")
	root.InitDefaultHelpCmd()
	want := []string{"status", "ask", "run", "project", "dev", "fleet", "admin", "auth", "self"}
	var got []string
	validGroups := map[string]bool{"start": true, "manage": true, "develop": true, "operate": true, "local": true}
	for _, cmd := range root.Commands() {
		if cmd.Name() == "help" || cmd.Hidden {
			continue
		}
		got = append(got, cmd.Name())
		if !validGroups[cmd.GroupID] {
			t.Errorf("root %q has invalid help group %q", cmd.Name(), cmd.GroupID)
		}
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("visible roots = %v, want %v", got, want)
	}
	for _, retired := range []string{"runs", "projects", "login", "config", "mailbox", "brain", "routes", "upgrade"} {
		if cmd, _, err := root.Find([]string{retired}); err == nil && cmd.Name() == retired {
			t.Errorf("retired root %q is still registered", retired)
		}
	}
	if cmd, _, err := root.Find([]string{"self", "completion"}); err != nil || cmd.Name() != "completion" {
		t.Fatalf("self completion missing: cmd=%v err=%v", cmd, err)
	}
	var help bytes.Buffer
	root.SetOut(&help)
	if err := root.Help(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(help.String(), "\n  help ") || strings.Contains(help.String(), "\n  completion ") {
		t.Fatalf("root help exposes framework commands:\n%s", help.String())
	}
	if cmd, _, err := root.Find([]string{"help", "project"}); err != nil || cmd.Name() != "help" {
		t.Fatalf("rc help must remain callable: cmd=%v err=%v", cmd, err)
	}

	for path, want := range map[string]string{
		"project repo ls":       "List repos",
		"project repo add":      "Create a repo",
		"project connection ls": "List connections",
		"project tenant ls":     "List tenants",
		"project database ls":   "List databases",
	} {
		cmd, _, err := root.Find(strings.Fields(path))
		if err != nil {
			t.Fatalf("find %s: %v", path, err)
		}
		if cmd.Short != want {
			t.Errorf("%s help = %q, want %q", path, cmd.Short, want)
		}
	}
}

func TestRecursiveHelpAndREADMEInventoryFresh(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/rootcause-cli-help-test")
	e := &env{out: &bytes.Buffer{}, err: &bytes.Buffer{}}
	root := newRootCmd(e, "VERSION")
	help := recursiveHelp(t, root)
	helpPath := filepath.Join("..", "..", "docs", "cli-help.txt")
	assertOrUpdateFile(t, helpPath, help)

	readmePath := filepath.Join("..", "..", "README.md")
	b, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatal(err)
	}
	want := replaceGeneratedInventory(t, string(b), commandInventory(root))
	if *update {
		if err := os.WriteFile(readmePath, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
	} else if string(b) != want {
		t.Fatal("README command inventory is stale; run `go test ./internal/cli -update`")
	}
}

func TestStatusUsesCompactPage(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/runs", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("limit"); got != "5" {
			t.Errorf("status limit = %q, want 5", got)
		}
		_, _ = w.Write([]byte(`{"runs":[],"summary":{"healthy":true}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	e, _, _ := newTestEnv(t, srv, "table")
	if err := run(t, e, "status"); err != nil {
		t.Fatal(err)
	}
}

func recursiveHelp(t *testing.T, root *cobra.Command) string {
	t.Helper()
	var out strings.Builder
	var walk func(*cobra.Command)
	walk = func(cmd *cobra.Command) {
		if cmd.Name() == "help" || cmd.Hidden {
			return
		}
		var b bytes.Buffer
		cmd.SetOut(&b)
		if err := cmd.Help(); err != nil {
			t.Fatalf("help %s: %v", cmd.CommandPath(), err)
		}
		fmt.Fprintf(&out, "$ %s --help\n%s\n", cmd.CommandPath(), b.String())
		for _, child := range cmd.Commands() {
			walk(child)
		}
	}
	walk(root)
	return out.String()
}

func commandInventory(root *cobra.Command) string {
	var lines []string
	var walk func(*cobra.Command)
	walk = func(cmd *cobra.Command) {
		for _, child := range cmd.Commands() {
			if child.Name() == "help" || child.Hidden {
				continue
			}
			lines = append(lines, fmt.Sprintf("| `%s` | %s |", child.CommandPath(), child.Short))
			walk(child)
		}
	}
	walk(root)
	sort.Strings(lines)
	return "| Command | Purpose |\n|---|---|\n" + strings.Join(lines, "\n") + "\n"
}

const inventoryStart = "<!-- BEGIN GENERATED COMMAND INVENTORY -->"
const inventoryEnd = "<!-- END GENERATED COMMAND INVENTORY -->"

func replaceGeneratedInventory(t *testing.T, readme, inventory string) string {
	t.Helper()
	start := strings.Index(readme, inventoryStart)
	end := strings.Index(readme, inventoryEnd)
	if start < 0 || end < start {
		t.Fatalf("README must contain %s / %s markers", inventoryStart, inventoryEnd)
	}
	start += len(inventoryStart)
	return readme[:start] + "\n" + inventory + readme[end:]
}

func assertOrUpdateFile(t *testing.T, path, got string) {
	t.Helper()
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != got {
		t.Fatalf("%s is stale; run `go test ./internal/cli -update`", path)
	}
}
