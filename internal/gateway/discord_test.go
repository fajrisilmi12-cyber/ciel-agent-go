package gateway

import (
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestHasBotMention(t *testing.T) {
	message := &discordgo.MessageCreate{Message: &discordgo.Message{Content: "<@123> hello"}}
	if !hasBotMention(message, "123") {
		t.Fatal("expected mention to be detected")
	}
	message.Content = "hello without mention"
	if hasBotMention(message, "123") {
		t.Fatal("did not expect mention to be detected")
	}
}

func TestStripBotMention(t *testing.T) {
	if got := stripBotMention("<@!123> hello Hermes", "123"); got != "hello Hermes" {
		t.Fatalf("content = %q, want %q", got, "hello Hermes")
	}
}

func TestContainsOrEmpty(t *testing.T) {
	if !containsOrEmpty(nil, "user-1") {
		t.Fatal("empty allowlist should allow")
	}
	if !containsOrEmpty([]string{"user-1"}, "user-1") {
		t.Fatal("listed user should be allowed")
	}
	if containsOrEmpty([]string{"user-1"}, "user-2") {
		t.Fatal("unlisted user should be denied")
	}
	if !containsOrEmpty([]string{"*"}, "user-2") {
		t.Fatal("wildcard should allow")
	}
}

func TestSplitDiscordMessage(t *testing.T) {
	content := make([]byte, discordMessageLimit*2+10)
	for index := range content {
		content[index] = 'x'
	}
	parts := splitDiscordMessage(string(content))
	if len(parts) != 3 {
		t.Fatalf("parts = %d, want 3", len(parts))
	}
	for _, part := range parts {
		if len(part) > discordMessageLimit {
			t.Fatalf("part length = %d, exceeds Discord limit", len(part))
		}
	}
}
