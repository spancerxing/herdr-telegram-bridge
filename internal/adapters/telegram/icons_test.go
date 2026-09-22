package telegram

import (
	"context"
	"testing"

	"github.com/spancerxing/herdr-telegram-bridge/internal/domain"
)

func TestIconSetIndexesAndNormalises(t *testing.T) {
	set := NewIconSet([]sticker{
		{CustomEmojiID: "5", Emoji: "⚡"},
		{CustomEmojiID: "6", Emoji: "❓️"}, // variation selector
		{CustomEmojiID: "7", Emoji: "⚡"},       // duplicate emoji: first wins
		{CustomEmojiID: "", Emoji: "x"},         // incomplete entry
	})
	if got := set.For("⚡"); got.CustomEmojiID != "5" {
		t.Errorf("⚡ -> %+v", got)
	}
	if got := set.For("❓"); got.CustomEmojiID != "6" {
		t.Errorf("❓ (selector-stripped) -> %+v", got)
	}
	if got := set.For("🚀"); got != (domain.TopicIcon{}) {
		t.Errorf("missing emoji should be a zero icon, got %+v", got)
	}
	if got := set.For(""); got != (domain.TopicIcon{}) {
		t.Errorf("empty emoji should be a zero icon, got %+v", got)
	}
}

func TestLoadIcons(t *testing.T) {
	f := newFakeAPI(t)
	f.respond("getForumTopicIconStickers",
		`{"ok":true,"result":[{"custom_emoji_id":"5","emoji":"⚡"},{"custom_emoji_id":"6","emoji":"✅"}]}`)
	set, err := LoadIcons(context.Background(), NewClient(f.server.URL, "T", nil), nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := set.For(domain.StatusWorking.Emoji()); got.CustomEmojiID != "5" {
		t.Errorf("working icon = %+v", got)
	}
	if got := set.For(domain.StatusIdle.Emoji()); got.CustomEmojiID != "6" {
		t.Errorf("idle icon = %+v", got)
	}
}
