package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/fajrisilmi12-cyber/hermes-agent-go/internal/agent"
	cronpkg "github.com/fajrisilmi12-cyber/hermes-agent-go/internal/cron"
	"github.com/fajrisilmi12-cyber/hermes-agent-go/internal/config"
	"github.com/fajrisilmi12-cyber/hermes-agent-go/internal/gateway"
	"github.com/fajrisilmi12-cyber/hermes-agent-go/internal/memory"
	"github.com/fajrisilmi12-cyber/hermes-agent-go/internal/providers"
	"github.com/fajrisilmi12-cyber/hermes-agent-go/internal/secrets"
	"github.com/fajrisilmi12-cyber/hermes-agent-go/internal/skills"
	"github.com/fajrisilmi12-cyber/hermes-agent-go/internal/state"
	"github.com/fajrisilmi12-cyber/hermes-agent-go/internal/tools"
	"github.com/spf13/cobra"
)

const version = "0.1.0-dev"

func main() {
	root := &cobra.Command{
		Use:   "ciel",
		Short: "Ciel Agent Go",
	}

	root.AddCommand(
		&cobra.Command{
			Use:   "version",
			Short: "Print the Ciel version",
			Run: func(cmd *cobra.Command, args []string) {
				fmt.Println(version)
			},
		},
		newDoctorCommand(),
		newSessionsCommand(),
		newChatCommand(),
		newSetupCommand(),
		newMemoryCommand(),
		newSkillsCommand(),
		newGatewayCommand(),
	)

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newGatewayCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "gateway",
		Short: "Start configured messaging gateways",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			cfg, err := config.Load("config.yaml")
			if err != nil {
				return err
			}
			if !cfg.Gateway.Discord.Enabled || strings.TrimSpace(cfg.Gateway.Discord.Token) == "" {
				return fmt.Errorf("Discord gateway is not configured; set gateway.discord.token in config.yaml")
			}
			providerConfig, ok := cfg.Providers[cfg.DefaultProvider]
			if !ok || strings.TrimSpace(providerConfig.Model) == "" {
				return fmt.Errorf("provider %q is not configured", cfg.DefaultProvider)
			}
			if strings.TrimSpace(providerConfig.APIKey) == "" && !isLocalProvider(providerConfig.BaseURL) {
				return fmt.Errorf("provider %q has no API key; set it in config.yaml or the environment", cfg.DefaultProvider)
			}
			db, err := state.Open(ctx, cfg.State.DBPath)
			if err != nil {
				return err
			}
			defer db.Close()
			provider := &providers.OpenAICompatible{APIKey: providerConfig.APIKey, BaseURL: providerConfig.BaseURL, Model: providerConfig.Model}
			registry := tools.NewRegistry()
			if err := tools.RegisterBuiltins(registry, "."); err != nil {
				return err
			}
			if err := tools.RegisterSessionSearch(registry, db); err != nil {
				return err
			}
			toolNames := make([]string, 0, len(registry.Tools()))
			for _, tool := range registry.Tools() {
				toolNames = append(toolNames, tool.Name())
			}
			runner := agent.NewRunner(db, provider, providerConfig.Model, cfg.Agent.MaxIterations, registry)
			runner.SetHistoryLimit(cfg.Gateway.Discord.HistoryLimit)
			if len(cfg.Gateway.Discord.AllowedTools) > 0 {
				runner.SetAllowedTools(cfg.Gateway.Discord.AllowedTools)
			} else {
				runner.SetAllowedTools([]string{"file", "search_files", "web_fetch", "web_search", "todo", "process", "session_search", "clarify", "zernio_post"})
			}

			// --- Wire OpenClaw-inspired patterns ---
			if err := tools.RegisterAdvancedTools(registry, db); err != nil {
				return err
			}
			// Session Recovery
			if cfg.Recovery.Enabled {
				runner.SetRecoveryStore(state.NewRecoveryStore(db, cfg.Recovery.MaxRetries))
			}
			// Memory Provenance
			runner.SetProvenanceStore(memory.NewProvenanceStore(db))
			// Exec Approval Binding on terminal tool
			if cfg.Approval.Enabled {
				approvalStore := tools.GetApprovalStore(db)
				tools.ConfigureTerminalTool(registry, approvalStore, nil)
			}
			// Egress filter (redact secrets from tool output)
			if cfg.Secrets.EgressFilter {
				secretStore := secrets.NewStore(db)
				runner.AddPostHook(func(ctx context.Context, sessionID, toolName string, args json.RawMessage, result *string) {
					if result != nil && *result != "" {
						filtered, _ := secretStore.RedactSensitive(ctx, *result)
						*result = filtered
					}
				})
			}
			// Cron/Automation scheduler
			if cfg.Cron.Enabled {
				scheduler := cronpkg.NewScheduler(db, func(ctx context.Context, prompt, toolPolicy string) (string, error) {
					// Execute a cron turn with restricted tools
					toolPolicyMap := make(map[string]bool)
					if toolPolicy != "*" {
						for _, name := range strings.Split(toolPolicy, ",") {
							if n := strings.TrimSpace(name); n != "" {
								toolPolicyMap[n] = true
							}
						}
					}
					localRunner := agent.NewRunner(db, provider, providerConfig.Model, cfg.Agent.MaxIterations, registry)
					if len(toolPolicyMap) > 0 {
						names := make([]string, 0, len(toolPolicyMap))
						for n := range toolPolicyMap {
							names = append(names, n)
						}
						localRunner.SetAllowedTools(names)
					}
					localRunner.SetSystemPrompt(agent.BuildSystemPrompt("You are Ciel Agent (cron).", cfg.Persona, "", ""))
					_, response, err := localRunner.Run(ctx, "", prompt)
					return response, err
				})
				scheduler.Start(ctx)
				if err := tools.RegisterCronTool(registry, scheduler); err != nil {
					return err
				}
			}
			memoryContext, err := memory.NewStore(db, cfg.Memory.Budget).Context(ctx)
			if err != nil {
				return err
			}
			loadedSkills, err := skills.LoadDir(cfg.Skills.Path)
			if err != nil {
				return err
			}
			builtinSkills, err := skills.LoadDir(cfg.Skills.BuiltinPath)
			if err != nil {
				return err
			}
			skillContext := skills.BuildContext(append(loadedSkills, builtinSkills...), 12000)
			runner.SetSystemPrompt(agent.BuildSystemPrompt("You are Ciel Agent, a helpful autonomous AI assistant.", cfg.Persona, memoryContext, skillContext))
			connector := gateway.NewDiscordConnector(cfg.Gateway.Discord.Token, providerConfig.Model, db, runner)
			connector.SetPolicy(gateway.DiscordPolicy{
				RequireMention:   cfg.Gateway.Discord.RequireMention,
				HistoryLimit:     cfg.Gateway.Discord.HistoryLimit,
				GuildAllowlist:   cfg.Gateway.Discord.GuildAllowlist,
				ChannelAllowlist: cfg.Gateway.Discord.ChannelAllowlist,
				UserAllowlist:    cfg.Gateway.Discord.UserAllowlist,
			})
			if err := connector.Start(ctx); err != nil {
				return err
			}
			fmt.Printf("Discord gateway started. Tools: %s. Press Ctrl+C to stop.\n", strings.Join(toolNames, ", "))
			<-ctx.Done()
			return connector.Stop()
		},
	}
}

func newMemoryCommand() *cobra.Command {
	command := &cobra.Command{Use: "memory", Short: "Inspect or update persistent memory"}
	command.AddCommand(
		&cobra.Command{
			Use:   "get [user|memory]",
			Args:  cobra.ExactArgs(1),
			Short: "Print a memory target",
			RunE: func(cmd *cobra.Command, args []string) error {
				ctx := cmd.Context()
				cfg, err := config.Load("config.yaml")
				if err != nil {
					return err
				}
				db, err := state.Open(ctx, cfg.State.DBPath)
				if err != nil {
					return err
				}
				defer db.Close()
				value, err := memory.NewStore(db, cfg.Memory.Budget).Get(ctx, args[0])
				if err != nil {
					return err
				}
				fmt.Println(value)
				return nil
			},
		},
	)
	var value string
	setCommand := &cobra.Command{
		Use:   "set [user|memory]",
		Args:  cobra.ExactArgs(1),
		Short: "Replace a memory target",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cfg, err := config.Load("config.yaml")
			if err != nil {
				return err
			}
			db, err := state.Open(ctx, cfg.State.DBPath)
			if err != nil {
				return err
			}
			defer db.Close()
			if err := memory.NewStore(db, cfg.Memory.Budget).Set(ctx, args[0], value); err != nil {
				return err
			}
			fmt.Printf("updated memory target %s\n", args[0])
			return nil
		},
	}
	setCommand.Flags().StringVarP(&value, "value", "v", "", "memory content")
	_ = setCommand.MarkFlagRequired("value")
	command.AddCommand(setCommand)
	return command
}

func newSkillsCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "skills",
		Short: "List discovered skills",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load("config.yaml")
			if err != nil {
				return err
			}
			for _, root := range []string{cfg.Skills.Path, cfg.Skills.BuiltinPath} {
				loaded, err := skills.LoadDir(root)
				if err != nil {
					return err
				}
				for _, skill := range loaded {
					fmt.Printf("%s\t%s\t%s\n", skill.Name, skill.Description, skill.Path)
				}
			}
			return nil
		},
	}
}

// newSetupCommand is defined in setup.go

func newChatCommand() *cobra.Command {
	var query string
	var continueSession bool
	command := &cobra.Command{
		Use:   "chat",
		Short: "Chat with the configured AI provider",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cfg, err := config.Load("config.yaml")
			if err != nil {
				return err
			}
			providerConfig, ok := cfg.Providers[cfg.DefaultProvider]
			if !ok {
				return fmt.Errorf("provider %q is not configured", cfg.DefaultProvider)
			}
			if strings.TrimSpace(providerConfig.Model) == "" {
				return fmt.Errorf("provider %q has no model configured", cfg.DefaultProvider)
			}
			if strings.TrimSpace(providerConfig.APIKey) == "" && !isLocalProvider(providerConfig.BaseURL) {
				return fmt.Errorf("provider %q has no API key; set it in config.yaml or the environment", cfg.DefaultProvider)
			}
			db, err := state.Open(ctx, cfg.State.DBPath)
			if err != nil {
				return err
			}
			defer db.Close()
			provider := &providers.OpenAICompatible{
				APIKey:  providerConfig.APIKey,
				BaseURL: providerConfig.BaseURL,
				Model:   providerConfig.Model,
			}
			registry := tools.NewRegistry()
			if err := tools.RegisterBuiltins(registry, "."); err != nil {
				return err
			}
			if err := tools.RegisterSessionSearch(registry, db); err != nil {
				return err
			}
			runner := agent.NewRunner(db, provider, providerConfig.Model, cfg.Agent.MaxIterations, registry)
			// Wire OpenClaw patterns for chat mode too
			if err := tools.RegisterAdvancedTools(registry, db); err != nil {
				return err
			}
			if cfg.Recovery.Enabled {
				runner.SetRecoveryStore(state.NewRecoveryStore(db, cfg.Recovery.MaxRetries))
			}
			runner.SetProvenanceStore(memory.NewProvenanceStore(db))
			if cfg.Approval.Enabled {
				tools.ConfigureTerminalTool(registry, tools.GetApprovalStore(db), nil)
			}
			if cfg.Secrets.EgressFilter {
				secretStore := secrets.NewStore(db)
				runner.AddPostHook(func(ctx context.Context, sessionID, toolName string, args json.RawMessage, result *string) {
					if result != nil && *result != "" {
						filtered, _ := secretStore.RedactSensitive(ctx, *result)
						*result = filtered
					}
				})
			}
			memoryContext, err := memory.NewStore(db, cfg.Memory.Budget).Context(ctx)
			if err != nil {
				return err
			}
			loadedSkills, err := skills.LoadDir(cfg.Skills.Path)
			if err != nil {
				return err
			}
			builtinSkills, err := skills.LoadDir(cfg.Skills.BuiltinPath)
			if err != nil {
				return err
			}
			loadedSkills = append(loadedSkills, builtinSkills...)
			skillContext := skills.BuildContext(loadedSkills, 12000)
			runner.SetSystemPrompt(agent.BuildSystemPrompt("You are Ciel Agent, a helpful autonomous AI assistant.", cfg.Persona, memoryContext, skillContext))
			sessionID := ""
			if continueSession {
				session, err := state.LatestSession(ctx, db)
				if err != nil {
					return err
				}
				sessionID = session.ID
			}
			if strings.TrimSpace(query) != "" {
				_, response, err := runner.Run(ctx, sessionID, query)
				if err != nil {
					return err
				}
				fmt.Println(response)
				return nil
			}
			return runInteractiveChat(ctx, runner, sessionID)
		},
	}
	command.Flags().StringVarP(&query, "query", "q", "", "send a single message")
	command.Flags().BoolVar(&continueSession, "continue", false, "continue the most recent session")
	return command
}

func isLocalProvider(baseURL string) bool {
	baseURL = strings.ToLower(baseURL)
	return strings.Contains(baseURL, "localhost") || strings.Contains(baseURL, "127.0.0.1") || strings.Contains(baseURL, "::1")
}

func runInteractiveChat(ctx context.Context, runner *agent.Runner, sessionID string) error {
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Println("Ciel Agent. Type /exit to quit.")
	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			return scanner.Err()
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "/exit" || input == "/quit" {
			return nil
		}
		if input == "" {
			continue
		}
		newSessionID, response, err := runner.Run(ctx, sessionID, input)
		if err != nil {
			return err
		}
		sessionID = newSessionID
		fmt.Println(response)
	}
}

func newSessionsCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "sessions",
		Short: "List stored sessions",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			cfg, err := config.Load("config.yaml")
			if err != nil {
				return err
			}
			db, err := state.Open(ctx, cfg.State.DBPath)
			if err != nil {
				return err
			}
			defer db.Close()

			sessions, err := state.ListSessions(ctx, db)
			if err != nil {
				return err
			}
			for _, session := range sessions {
				fmt.Printf("%s\t%s\t%s\n", session.ID, session.Profile, session.UpdatedAt)
			}
			return nil
		},
	}
}

// newDoctorCommand is defined in doctor.go
