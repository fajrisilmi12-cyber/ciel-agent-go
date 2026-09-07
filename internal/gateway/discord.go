package gateway

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/fajrisilmi12-cyber/hermes-agent-go/internal/agent"
	"github.com/fajrisilmi12-cyber/hermes-agent-go/internal/state"
)

const discordMessageLimit = 2000

const discordAgentTimeout = 5 * time.Minute

type DiscordConnector struct {
	token  string
	db     *sql.DB
	runner *agent.Runner
	model  string
	policy DiscordPolicy

	mu      sync.Mutex
	session *discordgo.Session
}

type DiscordPolicy struct {
	RequireMention   bool
	HistoryLimit     int
	GuildAllowlist   []string
	ChannelAllowlist []string
	UserAllowlist    []string
}

func NewDiscordConnector(token, model string, db *sql.DB, runner *agent.Runner) *DiscordConnector {
	return &DiscordConnector{token: token, model: model, db: db, runner: runner, policy: DiscordPolicy{RequireMention: true, HistoryLimit: 20}}
}

func (connector *DiscordConnector) SetPolicy(policy DiscordPolicy) {
	connector.policy = policy
	if connector.policy.HistoryLimit <= 0 {
		connector.policy.HistoryLimit = 20
	}
}

func (connector *DiscordConnector) Start(ctx context.Context) error {
	if strings.TrimSpace(connector.token) == "" {
		return fmt.Errorf("discord token is not configured")
	}
	if connector.runner == nil || connector.db == nil {
		return fmt.Errorf("discord connector requires an agent runner and database")
	}
	session, err := discordgo.New("Bot " + connector.token)
	if err != nil {
		return fmt.Errorf("create Discord session: %w", err)
	}
	session.Identify.Intents = discordgo.IntentsGuildMessages | discordgo.IntentsDirectMessages | discordgo.IntentsMessageContent
	session.AddHandler(connector.handleMessage)
	if err := session.Open(); err != nil {
		return fmt.Errorf("open Discord gateway: %w", err)
	}
	connector.mu.Lock()
	connector.session = session
	connector.mu.Unlock()
	go func() {
		<-ctx.Done()
		_ = connector.Stop()
	}()
	return nil
}

func (connector *DiscordConnector) Stop() error {
	connector.mu.Lock()
	defer connector.mu.Unlock()
	if connector.session == nil {
		return nil
	}
	err := connector.session.Close()
	connector.session = nil
	return err
}

func (connector *DiscordConnector) handleMessage(session *discordgo.Session, event *discordgo.MessageCreate) {
	if event.Author == nil || event.Author.Bot || strings.TrimSpace(event.Content) == "" {
		return
	}
	if !containsOrEmpty(connector.policy.UserAllowlist, event.Author.ID) ||
		!containsOrEmpty(connector.policy.ChannelAllowlist, event.ChannelID) ||
		(event.GuildID != "" && !containsOrEmpty(connector.policy.GuildAllowlist, event.GuildID)) {
		return
	}
	botID := ""
	if session.State != nil && session.State.User != nil {
		botID = session.State.User.ID
	}
	if connector.policy.RequireMention && !hasBotMention(event, botID) {
		return
	}
	content := stripBotMention(event.Content, botID)
	if content == "" {
		content = "hello"
	}
	ctx, cancel := context.WithTimeout(context.Background(), discordAgentTimeout)
	defer cancel()
	profile := "discord:" + event.ChannelID + ":user:" + event.Author.ID
	storedSession, err := state.FindLatestSessionByProfile(ctx, connector.db, profile)
	if errors.Is(err, sql.ErrNoRows) {
		storedID, createErr := state.CreateSession(ctx, connector.db, profile, connector.model)
		if createErr != nil {
			connector.sendError(session, event.ChannelID, createErr)
			return
		}
		storedSession.ID = storedID
	} else if err != nil {
		connector.sendError(session, event.ChannelID, err)
		return
	}
	_, response, err := connector.runner.Run(ctx, storedSession.ID, content)
	if err != nil {
		connector.sendError(session, event.ChannelID, err)
		return
	}
	for _, part := range splitDiscordMessage(response) {
		if _, err := session.ChannelMessageSend(event.ChannelID, part); err != nil {
			return
		}
	}
}

func containsOrEmpty(values []string, value string) bool {
	if len(values) == 0 {
		return true
	}
	for _, candidate := range values {
		if candidate == value || candidate == "*" {
			return true
		}
	}
	return false
}

func (connector *DiscordConnector) sendError(session *discordgo.Session, channelID string, err error) {
	_, _ = session.ChannelMessageSend(channelID, "Ciel error: "+err.Error())
}

func splitDiscordMessage(content string) []string {
	content = strings.TrimSpace(content)
	if content == "" {
		return []string{"(empty response)"}
	}
	var parts []string
	for len(content) > discordMessageLimit {
		cut := strings.LastIndex(content[:discordMessageLimit], "\n")
		if cut < discordMessageLimit/2 {
			cut = discordMessageLimit
		}
		parts = append(parts, content[:cut])
		content = strings.TrimSpace(content[cut:])
	}
	return append(parts, content)
}

func hasBotMention(event *discordgo.MessageCreate, botID string) bool {
	if event == nil || botID == "" {
		return false
	}
	for _, user := range event.Mentions {
		if user != nil && user.ID == botID {
			return true
		}
	}
	return strings.Contains(event.Content, "<@"+botID+">") || strings.Contains(event.Content, "<@!"+botID+">")
}

func stripBotMention(content, botID string) string {
	content = strings.ReplaceAll(content, "<@"+botID+">", "")
	content = strings.ReplaceAll(content, "<@!"+botID+">", "")
	return strings.TrimSpace(content)
}
