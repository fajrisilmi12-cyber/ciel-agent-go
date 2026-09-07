package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fajrisilmi12-cyber/hermes-agent-go/internal/config"
	"github.com/fajrisilmi12-cyber/hermes-agent-go/internal/memory"
	"github.com/fajrisilmi12-cyber/hermes-agent-go/internal/skills"
	"github.com/fajrisilmi12-cyber/hermes-agent-go/internal/state"
	"github.com/fajrisilmi12-cyber/hermes-agent-go/internal/tools"
	"github.com/spf13/cobra"
)

type checkResult struct {
	Name   string
	Status string // "ok", "warn", "fail"
	Detail string
}

func newDoctorCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Comprehensive health check for Ciel Agent",
		Long:  "Checks config, database, provider, skills, directories, memory, and tool registry.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDoctor()
		},
	}
}

func runDoctor() error {
	fmt.Println()
	fmt.Println("  Ciel Health Check")
	fmt.Println("  ───────────────────")
	fmt.Println()

	var results []checkResult

	// 1. Config
	results = append(results, checkConfig())
	// If config fails, remaining checks would panic — stop early
	if len(results) > 0 && results[0].Status == "fail" {
		printResults(results)
		return nil
	}

	cfg, _ := config.Load("config.yaml")

	// 2. Directories
	results = append(results, checkDirectories())

	// 3. Database — open once, share with tools check
	dbResult, db := checkDatabase(cfg)
	results = append(results, dbResult)

	// 4. Provider
	results = append(results, checkProvider(cfg))
	// 5. Skills
	results = append(results, checkSkills(cfg))
	// 6. Memory
	results = append(results, checkMemory(cfg))
	// 7. Tools
	if db != nil {
		results = append(results, checkTools(db))
		db.Close()
	} else {
		results = append(results, checkResult{"tools", "warn", "skipped (database unavailable)"})
	}

	printResults(results)
	return nil
}

func checkConfig() checkResult {
	cfg, err := config.Load("config.yaml")
	if err != nil {
		return checkResult{"config.yaml", "fail", fmt.Sprintf("parse error: %v", err)}
	}
	_ = cfg
	return checkResult{"config.yaml", "ok", "found & valid"}
}

func checkDirectories() checkResult {
	home, err := os.UserHomeDir()
	if err != nil {
		return checkResult{"~/.ciel/", "warn", "cannot determine home directory"}
	}
	hermesDir := filepath.Join(home, ".ciel")
	info, err := os.Stat(hermesDir)
	if err != nil || !info.IsDir() {
		return checkResult{"~/.ciel/", "warn", "directory missing (run 'ciel setup')"}
	}
	// Check writable
	testFile := filepath.Join(hermesDir, ".doctor_test")
	if f, err := os.Create(testFile); err != nil {
		return checkResult{"~/.ciel/", "warn", "directory not writable"}
	} else {
		f.Close()
		os.Remove(testFile)
	}
	return checkResult{"~/.ciel/", "ok", "exists & writable"}
}

func checkDatabase(cfg config.Config) (checkResult, *sql.DB) {
	dbPath := cfg.State.DBPath
	if dbPath == "" {
		return checkResult{"database", "warn", "no db_path configured"}, nil
	}
	info, err := os.Stat(dbPath)
	if err != nil {
		return checkResult{"database", "warn", fmt.Sprintf("%s (will be created on first use)", dbPath)}, nil
	}
	size := info.Size()
	sizeStr := fmt.Sprintf("%d bytes", size)
	if size > 1024*1024 {
		sizeStr = fmt.Sprintf("%.1f MB", float64(size)/(1024*1024))
	} else if size > 1024 {
		sizeStr = fmt.Sprintf("%.1f KB", float64(size)/1024)
	}

	// Try opening to verify integrity
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	db, err := state.Open(ctx, dbPath)
	if err != nil {
		return checkResult{"database", "fail", fmt.Sprintf("open failed: %v", err)}, nil
	}

	return checkResult{"database", "ok", fmt.Sprintf("%s (%s)", dbPath, sizeStr)}, db
}

func checkProvider(cfg config.Config) checkResult {
	provCfg, ok := cfg.Providers[cfg.DefaultProvider]
	if !ok {
		return checkResult{"provider", "warn", fmt.Sprintf("provider %q not configured", cfg.DefaultProvider)}
	}
	if strings.TrimSpace(provCfg.BaseURL) == "" {
		return checkResult{"provider", "warn", "no base_url configured"}
	}
	if strings.TrimSpace(provCfg.Model) == "" {
		return checkResult{"provider", "warn", "no model configured"}
	}
	masked := maskAPIKey(provCfg.APIKey)
	keyInfo := "no key"
	if masked != "" {
		keyInfo = masked
	}
	if provCfg.APIKey == "" && !isLocalProvider(provCfg.BaseURL) {
		return checkResult{"provider", "warn", fmt.Sprintf("%s / %s (API key: %s — needed for remote providers)", cfg.DefaultProvider, provCfg.Model, keyInfo)}
	}

	// Quick connectivity check
	client := &http.Client{Timeout: 8 * time.Second}
	url := strings.TrimRight(provCfg.BaseURL, "/") + "/models"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return checkResult{"provider", "warn", fmt.Sprintf("%s / %s (request error: %v)", cfg.DefaultProvider, provCfg.Model, err)}
	}
	if provCfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+provCfg.APIKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return checkResult{"provider", "warn", fmt.Sprintf("%s / %s (connect error: %v)", cfg.DefaultProvider, provCfg.Model, err)}
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return checkResult{"provider", "fail", fmt.Sprintf("%s / %s (auth failed — HTTP %d)", cfg.DefaultProvider, provCfg.Model, resp.StatusCode)}
	}
	if resp.StatusCode >= 500 {
		return checkResult{"provider", "warn", fmt.Sprintf("%s / %s (server error — HTTP %d)", cfg.DefaultProvider, provCfg.Model, resp.StatusCode)}
	}
	return checkResult{"provider", "ok", fmt.Sprintf("%s / %s (key: %s, HTTP %d)", cfg.DefaultProvider, provCfg.Model, keyInfo, resp.StatusCode)}
}

func checkSkills(cfg config.Config) checkResult {
	var loaded []skills.Skill
	totalBuiltin := 0
	totalUser := 0

	builtinSkills, err := skills.LoadDir(cfg.Skills.BuiltinPath)
	if err == nil {
		totalBuiltin = len(builtinSkills)
		loaded = append(loaded, builtinSkills...)
	}

	userSkills, err := skills.LoadDir(cfg.Skills.Path)
	if err == nil {
		totalUser = len(userSkills)
		loaded = append(loaded, userSkills...)
	}

	if len(loaded) == 0 {
		return checkResult{"skills", "warn", "no skills found"}
	}
	return checkResult{"skills", "ok", fmt.Sprintf("%d builtin, %d user", totalBuiltin, totalUser)}
}

func checkMemory(cfg config.Config) checkResult {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	db, err := state.Open(ctx, cfg.State.DBPath)
	if err != nil {
		return checkResult{"memory", "warn", "cannot open database"}
	}
	defer db.Close()

	provStore := memory.NewProvenanceStore(db)
	_ = provStore

	return checkResult{"memory", "ok", "ready"}
}

func checkTools(db *sql.DB) checkResult {
	registry := tools.NewRegistry()
	if err := tools.RegisterBuiltins(registry, "."); err != nil {
		return checkResult{"tools", "fail", fmt.Sprintf("register error: %v", err)}
	}
	_ = tools.RegisterSessionSearch(registry, db)
	_ = tools.RegisterRecommended(registry)

	count := len(registry.Tools())
	names := make([]string, 0, count)
	for _, t := range registry.Tools() {
		names = append(names, t.Name())
	}
	return checkResult{"tools", "ok", fmt.Sprintf("%d registered: %s", count, strings.Join(names, ", "))}
}

func printResults(results []checkResult) {
	for _, r := range results {
		var icon string
		switch r.Status {
		case "ok":
			icon = "✓"
		case "warn":
			icon = "⚠"
		case "fail":
			icon = "✗"
		}
		fmt.Printf("  %s %-16s %s\n", icon, r.Name, r.Detail)
	}
	fmt.Println()

	// Summary
	fails := 0
	warns := 0
	for _, r := range results {
		switch r.Status {
		case "fail":
			fails++
		case "warn":
			warns++
		}
	}
	if fails == 0 && warns == 0 {
		fmt.Println("  All checks passed! Run 'ciel chat' to start.")
	} else if fails == 0 {
		fmt.Printf("  %d warning(s). Ciel should work but review the warnings above.\n", warns)
	} else {
		fmt.Printf("  %d failure(s), %d warning(s). Fix the issues above before running Ciel.\n", fails, warns)
	}
	fmt.Println()
}

// RegisterRecommended is referenced in checkTools; import the package-level function.
// We call tools.RegisterRecommended directly — it's exported.
