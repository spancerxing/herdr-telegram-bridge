package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/spancerxing/herdr-telegram-bridge/internal/domain"
)

// emojiKey normalises an emoji for lookups by dropping variation selectors.
func emojiKey(e string) string {
	return strings.ReplaceAll(e, "️", "")
}

type sticker struct {
	CustomEmojiID string `json:"custom_emoji_id"`
	Emoji         string `json:"emoji"`
}

// IconSet resolves an emoji to the custom emoji id of a forum topic icon,
// from one getForumTopicIconStickers result. The zero set resolves nothing
// and topics keep whatever icon Telegram gave them.
type IconSet struct {
	byEmoji map[string]string
}

func NewIconSet(stickers []sticker) IconSet {
	m := make(map[string]string, len(stickers))
	for _, s := range stickers {
		if s.CustomEmojiID == "" || s.Emoji == "" {
			continue
		}
		key := emojiKey(s.Emoji)
		if _, dup := m[key]; !dup {
			m[key] = s.CustomEmojiID
		}
	}
	return IconSet{byEmoji: m}
}

// For returns the topic icon for an emoji; a zero TopicIcon leaves the
// icon unchanged, which is what an emoji the pack lacks becomes.
func (s IconSet) For(emoji string) domain.TopicIcon {
	id, ok := s.byEmoji[emojiKey(emoji)]
	if !ok {
		return domain.TopicIcon{}
	}
	return domain.TopicIcon{CustomEmojiID: id}
}

// LoadIcons fetches the default topic-icon pack once. Failure is degraded,
// not fatal: without ids the daemon still works and every topic keeps its
// default icon. ponytail: no icon_color fallback at creation; add a Color
// field to domain.TopicIcon if the pack ever fails live.
func LoadIcons(ctx context.Context, c *Client, log *slog.Logger) (IconSet, error) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	var stickers []sticker
	if err := c.call(ctx, "getForumTopicIconStickers", struct{}{}, &stickers); err != nil {
		return IconSet{}, fmt.Errorf("getForumTopicIconStickers: %w", err)
	}
	set := NewIconSet(stickers)
	log.Info("telegram topic icons loaded",
		slog.Int("stickers", len(stickers)),
		slog.Int("emoji", len(set.byEmoji)))
	return set, nil
}
