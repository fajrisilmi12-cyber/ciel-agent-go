package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Providers       map[string]ProviderConfig `yaml:"providers"`
	DefaultProvider string                    `yaml:"default_provider"`
	Agent           AgentConfig               `yaml:"agent"`
	Persona         string                    `yaml:"persona"`
	Memory          MemoryConfig              `yaml:"memory"`
	Skills          SkillsConfig              `yaml:"skills"`
	Gateway         GatewayConfig             `yaml:"gateway"`
	Social          SocialConfig              `yaml:"social"`
	State           StateConfig               `yaml:"state"`
	Approval        ApprovalConfig            `yaml:"approval"`
	Cron            CronConfig                `yaml:"cron"`
	Recovery        RecoveryConfig            `yaml:"recovery"`
	Secrets         SecretsConfig             `yaml:"secrets"`
}

type ProviderConfig struct {
	APIKey  string `yaml:"api_key"`
	BaseURL string `yaml:"base_url"`
	Model   string `yaml:"model"`
}

type AgentConfig struct {
	MaxIterations int `yaml:"max_iterations"`
}

type MemoryConfig struct {
	Budget int `yaml:"budget"`
}

type SkillsConfig struct {
	Path        string `yaml:"path"`
	BuiltinPath string `yaml:"builtin_path"`
}

type StateConfig struct {
	DBPath string `yaml:"db_path"`
}

type GatewayConfig struct {
	Discord DiscordConfig `yaml:"discord"`
}

type DiscordConfig struct {
	Enabled          bool     `yaml:"enabled"`
	Token            string   `yaml:"token"`
	RequireMention   bool     `yaml:"require_mention"`
	HistoryLimit     int      `yaml:"history_limit"`
	GuildAllowlist   []string `yaml:"guild_allowlist"`
	ChannelAllowlist []string `yaml:"channel_allowlist"`
	UserAllowlist    []string `yaml:"user_allowlist"`
	AllowedTools     []string `yaml:"allowed_tools"`
}

type SocialConfig struct {
	Zernio ZernioConfig `yaml:"zernio"`
}

type ZernioConfig struct {
	APIKey       string `yaml:"api_key"`
	BaseURL      string `yaml:"base_url"`
	AccountID    string `yaml:"account_id"`
	AllowPublish bool   `yaml:"allow_publish"`
}

// ApprovalConfig controls exec-approval binding.
type ApprovalConfig struct {
	Enabled          bool `yaml:"enabled"`
	ExpireHours      int  `yaml:"expire_hours"`
	AutoApproveSafe  bool `yaml:"auto_approve_safe"`
}

// CronConfig controls the cron/automation scheduler.
type CronConfig struct {
	Enabled          bool `yaml:"enabled"`
	CheckIntervalSec int  `yaml:"check_interval_sec"`
}

// RecoveryConfig controls session crash recovery.
type RecoveryConfig struct {
	Enabled    bool `yaml:"enabled"`
	MaxRetries int  `yaml:"max_retries"`
}

// SecretsConfig controls secret management.
type SecretsConfig struct {
	EgressFilter bool `yaml:"egress_filter"`
}

func Load(path string) (Config, error) {
	cfg := Config{
		DefaultProvider: "openai",
		Agent:           AgentConfig{MaxIterations: 20},
		Persona:         "Be helpful, precise, honest about uncertainty, and concise unless the user asks for detail.",
		Memory:          MemoryConfig{Budget: 6000},
		Skills:          SkillsConfig{Path: "~/.ciel/skills", BuiltinPath: "./skills"},
		Gateway:         GatewayConfig{Discord: DiscordConfig{RequireMention: true, HistoryLimit: 20}},
		State:           StateConfig{DBPath: "~/.ciel/state.db"},
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			cfg.State.DBPath = expandHome(cfg.State.DBPath)
			return cfg, nil
		}
		return Config{}, err
	}

	data = []byte(os.ExpandEnv(string(data)))
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	if strings.TrimSpace(cfg.State.DBPath) == "" {
		cfg.State.DBPath = "~/.ciel/state.db"
	}
	if cfg.Agent.MaxIterations <= 0 {
		cfg.Agent.MaxIterations = 20
	}
	if strings.TrimSpace(cfg.Persona) == "" {
		cfg.Persona = "Be helpful, precise, honest about uncertainty, and concise unless the user asks for detail."
	}
	if cfg.Memory.Budget <= 0 {
		cfg.Memory.Budget = 6000
	}
	if strings.TrimSpace(cfg.Skills.Path) == "" {
		cfg.Skills.Path = "~/.ciel/skills"
	}
	if strings.TrimSpace(cfg.Skills.BuiltinPath) == "" {
		cfg.Skills.BuiltinPath = "./skills"
	}
	if strings.TrimSpace(cfg.Gateway.Discord.Token) != "" {
		cfg.Gateway.Discord.Enabled = true
	}
	if cfg.Gateway.Discord.HistoryLimit <= 0 {
		cfg.Gateway.Discord.HistoryLimit = 20
	}
	cfg.Skills.Path = expandHome(cfg.Skills.Path)
	cfg.State.DBPath = expandHome(cfg.State.DBPath)
	return cfg, nil
}

func expandHome(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~"))
}

// Save writes the config to the given path as YAML with restricted permissions.
// Sensitive fields (API keys) are stored as-is; users should prefer env-var
// references (${VAR}) in the file.
func (cfg Config) Save(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return os.WriteFile(path, data, 0o600)
}
