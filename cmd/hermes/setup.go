package main

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fajrisilmi12-cyber/hermes-agent-go/internal/config"
	"github.com/fajrisilmi12-cyber/hermes-agent-go/internal/state"
	"github.com/spf13/cobra"
)

const setupBanner = `
  ╔══════════════════════════════════════════════╗
  ║           Ciel Agent  — Setup Wizard         ║
  ║         v%s                          ║
  ╚══════════════════════════════════════════════╝
`

// providerPreset holds default values for known LLM providers.
type providerPreset struct {
	Name        string
	BaseURL     string
	Model       string
	NeedsKey    bool
	KeyHint     string
}

var providerPresets = []providerPreset{
	{Name: "OpenAI", BaseURL: "https://api.openai.com/v1", Model: "gpt-4o-mini", NeedsKey: true, KeyHint: "https://platform.openai.com/api-keys"},
	{Name: "Custom OpenAI-compatible (Ollama, vLLM, LiteLLM, …)", BaseURL: "", Model: "", NeedsKey: false},
	{Name: "Skip (configure manually later)", BaseURL: "", Model: "", NeedsKey: false},
}

var modelOptions = []string{
	"gpt-4o-mini  (recommended — cheap & fast)",
	"gpt-4o       (smarter — more expensive)",
	"Custom model name",
}

func newSetupCommand() *cobra.Command {
	var force bool
	var nonInteractive bool
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Interactive setup wizard for Ciel Agent",
		Long:  "Walks you through creating config.yaml, directories, and initializing the database.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSetupWizard(force, nonInteractive)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite existing config.yaml")
	cmd.Flags().BoolVar(&nonInteractive, "non-interactive", false, "use environment variables and defaults (for CI/scripting)")
	return cmd
}

func runSetupWizard(force, nonInteractive bool) error {
	reader := bufio.NewScanner(os.Stdin)
	reader.Buffer(make([]byte, 0, 64*1024), 64*1024)

	fmt.Printf(setupBanner, version)

	// --- Check existing config ---
	configPath := "config.yaml"
	if !force {
		if _, err := os.Stat(configPath); err == nil {
			if nonInteractive {
				fmt.Println("config.yaml already exists. Use --force to overwrite.")
				return nil
			}
			fmt.Print("  config.yaml already exists. Overwrite? [y/N]: ")
			if !scanYesNo(reader, false) {
				fmt.Println("  Aborted.")
				return nil
			}
			// Backup existing
			backup := configPath + ".bak"
			if data, err := os.ReadFile(configPath); err == nil {
				_ = os.WriteFile(backup, data, 0o600)
				fmt.Printf("  ✓ Backed up to %s\n", backup)
			}
		}
	}

	cfg := config.Config{
		DefaultProvider: "openai",
		Agent:           config.AgentConfig{MaxIterations: 20},
		Persona:         "Be helpful, precise, honest about uncertainty, and concise unless the user asks for detail.",
		Memory:          config.MemoryConfig{Budget: 6000},
		Skills:          config.SkillsConfig{Path: "~/.ciel/skills", BuiltinPath: "./skills"},
		Gateway:         config.GatewayConfig{Discord: config.DiscordConfig{RequireMention: true, HistoryLimit: 20}},
		State:           config.StateConfig{DBPath: "~/.ciel/state.db"},
		Approval:        config.ApprovalConfig{ExpireHours: 24},
		Cron:            config.CronConfig{CheckIntervalSec: 30},
		Recovery:        config.RecoveryConfig{MaxRetries: 2},
	}

	if nonInteractive {
		return runNonInteractive(&cfg)
	}

	// === Step 1: Provider ===
	fmt.Println("  Step 1/6: Choose your AI provider")
	for i, p := range providerPresets {
		fmt.Printf("    %d. %s\n", i+1, p.Name)
	}
	choice := scanChoice(reader, 1, len(providerPresets))
	preset := providerPresets[choice-1]

	if choice == 3 {
		// Skip
		fmt.Println()
		fmt.Println("  Skipped. Edit config.yaml manually when ready.")
		if err := cfg.Save(configPath); err != nil {
			return err
		}
		fmt.Printf("  ✓ Created %s (minimal)\n", configPath)
		return nil
	}

	if choice == 1 {
		// OpenAI
		cfg.DefaultProvider = "openai"
		cfg.Providers = map[string]config.ProviderConfig{
			"openai": {BaseURL: preset.BaseURL, Model: preset.Model},
		}
	} else {
		// Custom
		fmt.Print("  Provider name [openai]: ")
		name := scanLine(reader, "openai")
		cfg.DefaultProvider = name
		fmt.Print("  Base URL: ")
		baseURL := scanLine(reader, "")
		if baseURL == "" {
			return fmt.Errorf("base URL is required for custom provider")
		}
		fmt.Print("  Model name [gpt-4o-mini]: ")
		model := scanLine(reader, "gpt-4o-mini")
		cfg.Providers = map[string]config.ProviderConfig{
			name: {BaseURL: baseURL, Model: model},
		}
		preset.NeedsKey = strings.Contains(strings.ToLower(baseURL), "openai") ||
			strings.Contains(strings.ToLower(baseURL), "api.")
	}

	// === Step 2: API Key ===
	fmt.Println()
	if preset.NeedsKey {
		fmt.Printf("  Step 2/6: Enter your API key\n")
		if preset.KeyHint != "" {
			fmt.Printf("  (get yours at %s)\n", preset.KeyHint)
		}
		fmt.Print("  API key: ")
		apiKey := scanLine(reader, "")
		if apiKey == "" {
			fmt.Println("  ⚠ No API key provided — you can set it later in config.yaml or via environment variable.")
			fmt.Print("  Or enter it now: ")
			apiKey = scanLine(reader, "")
		}
		provider := cfg.Providers[cfg.DefaultProvider]
		if apiKey != "" {
			// Store as env var reference if user wants
			provider.APIKey = apiKey
		}
		cfg.Providers[cfg.DefaultProvider] = provider
	} else {
		fmt.Println("  Step 2/6: API key (not required for local providers)")
		fmt.Print("  API key (optional, press Enter to skip): ")
		apiKey := scanLine(reader, "")
		if apiKey != "" {
			provider := cfg.Providers[cfg.DefaultProvider]
			provider.APIKey = apiKey
			cfg.Providers[cfg.DefaultProvider] = provider
		}
	}

	// === Step 3: Model ===
	fmt.Println()
	fmt.Println("  Step 3/6: Choose model")
	// If already set from preset or custom
	currentModel := cfg.Providers[cfg.DefaultProvider].Model
	if currentModel != "" {
		fmt.Printf("  Current: %s\n", currentModel)
	}
	for i, m := range modelOptions {
		fmt.Printf("    %d. %s\n", i+1, m)
	}
	fmt.Print("  Choice [1]: ")
	modelChoice := scanChoice(reader, 1, len(modelOptions))
	switch modelChoice {
	case 1:
		provider := cfg.Providers[cfg.DefaultProvider]
		provider.Model = "gpt-4o-mini"
		cfg.Providers[cfg.DefaultProvider] = provider
	case 2:
		provider := cfg.Providers[cfg.DefaultProvider]
		provider.Model = "gpt-4o"
		cfg.Providers[cfg.DefaultProvider] = provider
	case 3:
		fmt.Print("  Model name: ")
		customModel := scanLine(reader, "")
		if customModel != "" {
			provider := cfg.Providers[cfg.DefaultProvider]
			provider.Model = customModel
			cfg.Providers[cfg.DefaultProvider] = provider
		}
	}

	// === Step 4: Advanced features ===
	fmt.Println()
	fmt.Println("  Step 4/6: Advanced features")
	fmt.Print("    Enable exec approval binding?  [y/N]: ")
	cfg.Approval.Enabled = scanYesNo(reader, false)
	fmt.Print("    Enable session recovery?       [Y/n]: ")
	cfg.Recovery.Enabled = scanYesNo(reader, true)
	fmt.Print("    Enable egress filter?           [Y/n]: ")
	cfg.Secrets.EgressFilter = scanYesNo(reader, true)
	fmt.Print("    Enable cron/automation?         [y/N]: ")
	cfg.Cron.Enabled = scanYesNo(reader, false)

	// === Step 5: Discord ===
	fmt.Println()
	fmt.Println("  Step 5/6: Discord gateway (optional)")
	fmt.Print("    Enable Discord bot? [y/N]: ")
	if scanYesNo(reader, false) {
		fmt.Print("    Discord bot token: ")
		token := scanLine(reader, "")
		if token != "" {
			cfg.Gateway.Discord.Enabled = true
			cfg.Gateway.Discord.Token = token
		}
	}

	// === Step 6: Finalize ===
	fmt.Println()
	fmt.Println("  Step 6/6: Finalizing...")

	// Create directories
	home, _ := os.UserHomeDir()
	dirs := []string{
		filepath.Join(home, ".ciel"),
		filepath.Join(home, ".ciel", "skills"),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
		fmt.Printf("  ✓ Created %s/\n", dir)
	}

	// Write config
	if err := cfg.Save(configPath); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	fmt.Printf("  ✓ Wrote %s\n", configPath)

	// Init database
	dbPath := cfg.State.DBPath
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := state.Open(ctx, dbPath)
	if err != nil {
		fmt.Printf("  ⚠ Database init failed: %v\n", err)
	} else {
		db.Close()
		fmt.Printf("  ✓ Initialized database (%s)\n", dbPath)
	}

	// Validate provider connectivity
	provCfg := cfg.Providers[cfg.DefaultProvider]
	if provCfg.APIKey != "" && provCfg.BaseURL != "" {
		fmt.Print("  ✓ Testing provider connectivity... ")
		if err := testProvider(provCfg); err != nil {
			fmt.Printf("⚠ %v\n", err)
		} else {
			fmt.Println("connected!")
		}
	}

	// Print summary
	fmt.Println()
	maskedKey := maskAPIKey(cfg.Providers[cfg.DefaultProvider].APIKey)
	fmt.Printf("  Provider:   %s / %s\n", cfg.DefaultProvider, cfg.Providers[cfg.DefaultProvider].Model)
	if maskedKey != "" {
		fmt.Printf("  API key:    %s\n", maskedKey)
	}
	features := []string{}
	if cfg.Approval.Enabled {
		features = append(features, "approval")
	}
	if cfg.Recovery.Enabled {
		features = append(features, "recovery")
	}
	if cfg.Secrets.EgressFilter {
		features = append(features, "egress-filter")
	}
	if cfg.Cron.Enabled {
		features = append(features, "cron")
	}
	if len(features) > 0 {
		fmt.Printf("  Features:   %s\n", strings.Join(features, ", "))
	}
	fmt.Println()
	fmt.Println("  ─────────────────────────────────────────")
	fmt.Println("  Next steps:")
	fmt.Println("    ciel chat              Start chatting")
	fmt.Println("    ciel chat -q \"hello\"   Quick one-shot query")
	fmt.Println("    ciel doctor            Check health")
	fmt.Println("    ciel skills            List available skills")
	fmt.Println("  ─────────────────────────────────────────")
	fmt.Println()

	return nil
}

func runNonInteractive(cfg *config.Config) error {
	// Resolve API key from env
	apiKey := os.Getenv("CIEL_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("OPENAI_API_KEY")
	}
	if apiKey != "" {
		provider := cfg.Providers[cfg.DefaultProvider]
		provider.APIKey = apiKey
		cfg.Providers[cfg.DefaultProvider] = provider
	}

	model := os.Getenv("CIEL_MODEL")
	if model != "" {
		provider := cfg.Providers[cfg.DefaultProvider]
		provider.Model = model
		cfg.Providers[cfg.DefaultProvider] = provider
	}

	// Create directories
	home, _ := os.UserHomeDir()
	_ = os.MkdirAll(filepath.Join(home, ".ciel"), 0o700)
	_ = os.MkdirAll(filepath.Join(home, ".ciel", "skills"), 0o700)

	// Write config
	if err := cfg.Save("config.yaml"); err != nil {
		return err
	}
	fmt.Println("✓ config.yaml created (non-interactive)")

	// Init database
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := state.Open(ctx, cfg.State.DBPath)
	if err != nil {
		return fmt.Errorf("init database: %w", err)
	}
	db.Close()
	fmt.Printf("✓ database initialized (%s)\n", cfg.State.DBPath)
	return nil
}

// --- Input helpers ---

func scanLine(reader *bufio.Scanner, defaultVal string) string {
	if reader.Scan() {
		val := strings.TrimSpace(reader.Text())
		if val == "" {
			return defaultVal
		}
		return val
	}
	return defaultVal
}

func scanYesNo(reader *bufio.Scanner, defaultYes bool) bool {
	if reader.Scan() {
		input := strings.TrimSpace(strings.ToLower(reader.Text()))
		switch input {
		case "y", "yes":
			return true
		case "n", "no":
			return false
		case "":
			return defaultYes
		}
	}
	return defaultYes
}

func scanChoice(reader *bufio.Scanner, min, max int) int {
	for {
		fmt.Printf("  Choice [%d]: ", min)
		if reader.Scan() {
			input := strings.TrimSpace(reader.Text())
			if input == "" {
				return min
			}
			n := 0
			for _, c := range input {
				if c >= '0' && c <= '9' {
					n = n*10 + int(c-'0')
				} else {
					n = 0
					break
				}
			}
			if n >= min && n <= max {
				return n
			}
		}
		fmt.Printf("  Please enter a number between %d and %d.\n", min, max)
	}
}

// --- Utilities ---

func maskAPIKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + strings.Repeat("*", len(key)-8) + key[len(key)-4:]
}

func testProvider(cfg config.ProviderConfig) error {
	client := &http.Client{Timeout: 10 * time.Second}
	url := strings.TrimRight(cfg.BaseURL, "/") + "/models"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return fmt.Errorf("authentication failed (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("provider returned HTTP %d", resp.StatusCode)
	}
	return nil
}
