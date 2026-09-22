package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/spancerxing/herdr-telegram-bridge/internal/domain"
	"github.com/spancerxing/herdr-telegram-bridge/internal/ports"
)

// Config is everything the gateway needs beyond the token.
type Config struct {
	ChatID    int64
	Operators []domain.Operator
	// BaseURL overrides https://api.telegram.org; tests point it at a fake.
	BaseURL string
}

// Gateway implements ports.Telegram over the hand-written client.
type Gateway struct {
	cli   *Client
	cfg   Config
	botID int64
	icons IconSet
	log   *slog.Logger
}

var _ ports.Telegram = (*Gateway)(nil)

// New connects the bot: getMe identifies it, deleteWebhook clears the way
// for long polling, and the topic-icon pack is loaded once.
func New(ctx context.Context, token string, cfg Config, log *slog.Logger) (*Gateway, error) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	cli := NewClient(cfg.BaseURL, token, log)
	var me userResult
	if err := cli.call(ctx, "getMe", struct{}{}, &me); err != nil {
		return nil, fmt.Errorf("getMe: %w", err)
	}
	if !me.IsBot {
		return nil, fmt.Errorf("token is not a bot token")
	}
	if err := cli.call(ctx, "deleteWebhook", deleteWebhookParams{DropPendingUpdates: true}, nil); err != nil {
		return nil, fmt.Errorf("deleteWebhook: %w", err)
	}
	icons, err := LoadIcons(ctx, cli, log)
	if err != nil {
		log.Warn("topic icons unavailable; topics keep their default icon", slog.String("err", err.Error()))
	}
	return &Gateway{cli: cli, cfg: cfg, botID: me.ID, icons: icons, log: log}, nil
}

// IconFor maps a status to its topic icon; zero leaves the icon alone.
func (g *Gateway) IconFor(status domain.Status) domain.TopicIcon {
	return g.icons.For(status.Emoji())
}

func keyboard(buttons []domain.Button) *replyMarkup {
	// One button per row: long labels stay readable.
	rows := make([][]inlineButton, len(buttons))
	for i, b := range buttons {
		rows[i] = []inlineButton{{Text: b.Text, CallbackData: b.Data}}
	}
	return &replyMarkup{InlineKeyboard: rows}
}

func (g *Gateway) Send(ctx context.Context, out domain.Outgoing) (int, error) {
	if out.Markdown {
		// ponytail: plain text only; a parse mode would require escaping
		// every screen tail. Add HTML when formatted posts are wanted.
		g.log.Debug("send: markdown requested but not applied")
	}
	p := sendMessageParams{
		ChatID:              out.ChatID,
		Text:                truncate(out.Text, textMax),
		DisableNotification: !out.Notify || out.Silent,
	}
	if out.ThreadID != 0 {
		p.MessageThreadID = out.ThreadID
	}
	if len(out.Buttons) > 0 {
		p.ReplyMarkup = keyboard(out.Buttons)
	}
	var res messageIDResult
	if err := g.cli.call(ctx, "sendMessage", &p, &res); err != nil {
		return 0, err
	}
	return res.MessageID, nil
}

func (g *Gateway) EditText(ctx context.Context, chatID int64, messageID int, text string, markdown bool) error {
	p := editMessageTextParams{ChatID: chatID, MessageID: messageID, Text: truncate(text, textMax)}
	return g.cli.call(ctx, "editMessageText", &p, nil)
}

func (g *Gateway) EditKeyboard(ctx context.Context, chatID int64, messageID int, buttons []domain.Button) error {
	p := editMessageReplyMarkupParams{ChatID: chatID, MessageID: messageID, ReplyMarkup: keyboard(buttons)}
	return g.cli.call(ctx, "editMessageReplyMarkup", &p, nil)
}

func (g *Gateway) AnswerCallback(ctx context.Context, callbackID, text string, alert bool) error {
	p := answerCallbackQueryParams{CallbackQueryID: callbackID, Text: text, ShowAlert: alert}
	return g.cli.call(ctx, "answerCallbackQuery", &p, nil)
}

func (g *Gateway) Pin(ctx context.Context, chatID int64, messageID int) error {
	p := pinChatMessageParams{ChatID: chatID, MessageID: messageID, DisableNotification: true}
	return g.cli.call(ctx, "pinChatMessage", &p, nil)
}

func (g *Gateway) Delete(ctx context.Context, chatID int64, messageID int) error {
	p := deleteMessageParams{ChatID: chatID, MessageID: messageID}
	return g.cli.call(ctx, "deleteMessage", &p, nil)
}

func (g *Gateway) CreateTopic(ctx context.Context, chatID int64, name string, icon domain.TopicIcon) (int, error) {
	p := createForumTopicParams{ChatID: chatID, Name: truncateName(name)}
	if icon.CustomEmojiID != "" {
		p.IconCustomEmojiID = icon.CustomEmojiID
	}
	var res threadIDResult
	if err := g.cli.call(ctx, "createForumTopic", &p, &res); err != nil {
		return 0, err
	}
	return res.MessageThreadID, nil
}

// EditTopic changes only what is set: an empty name or icon is left alone,
// and an entirely empty patch skips the call.
func (g *Gateway) EditTopic(ctx context.Context, chatID int64, threadID int, name string, icon domain.TopicIcon) error {
	if name == "" && icon.CustomEmojiID == "" {
		return nil
	}
	p := editForumTopicParams{ChatID: chatID, MessageThreadID: threadID}
	if name != "" {
		p.Name = truncateName(name)
	}
	if icon.CustomEmojiID != "" {
		p.IconCustomEmojiID = icon.CustomEmojiID
	}
	return g.cli.call(ctx, "editForumTopic", &p, nil)
}

func (g *Gateway) CloseTopic(ctx context.Context, chatID int64, threadID int) error {
	p := closeForumTopicParams{ChatID: chatID, MessageThreadID: threadID}
	return g.cli.call(ctx, "closeForumTopic", &p, nil)
}

func (g *Gateway) DeleteTopic(ctx context.Context, chatID int64, threadID int) error {
	p := closeForumTopicParams{ChatID: chatID, MessageThreadID: threadID}
	return g.cli.call(ctx, "deleteForumTopic", &p, nil)
}

func (g *Gateway) ReopenTopic(ctx context.Context, chatID int64, threadID int) error {
	p := closeForumTopicParams{ChatID: chatID, MessageThreadID: threadID}
	return g.cli.call(ctx, "reopenForumTopic", &p, nil)
}

const pollTimeout = 50

// noticeDelay is how long the bot's own service messages stay visible
// before deletion. A var so tests can shorten it.
var noticeDelay = 10 * time.Second

// Stream long-polls updates until ctx ends or a fatal error surfaces, and
// hands each one to the handler already filtered to the configured chat and
// the configured operators.
func (g *Gateway) Stream(ctx context.Context, h ports.UpdateHandler) error {
	offset := 0
	for {
		var ups []update
		err := g.cli.poll(ctx, &getUpdatesParams{
			Offset:         offset,
			Timeout:        pollTimeout,
			AllowedUpdates: []string{"message", "callback_query", "my_chat_member"},
		}, &ups)
		if err == nil {
			for i := range ups {
				offset = ups[i].UpdateID + 1
				g.dispatch(ctx, h, &ups[i])
			}
			continue
		}
		if ctx.Err() != nil {
			return nil
		}
		if IsFatal(err) {
			return err
		}
		g.log.Warn("poll failed", slog.String("err", err.Error()))
		select {
		case <-time.After(3 * time.Second):
		case <-ctx.Done():
			return nil
		}
	}
}

// AwaitChat polls until the bot lands in a group — its administrator
// promotion, or any message written there — and returns the chat and the
// operator behind it. Setup uses it; the daemon uses Stream.
func (g *Gateway) AwaitChat(ctx context.Context) (chatID, fromID int64, err error) {
	offset := 0
	for {
		var ups []update
		err = g.cli.poll(ctx, &getUpdatesParams{
			Offset:         offset,
			Timeout:        pollTimeout,
			AllowedUpdates: []string{"message", "my_chat_member"},
		}, &ups)
		if err == nil {
			for i := range ups {
				offset = ups[i].UpdateID + 1
				if chat, from, ok := groupOf(&ups[i]); ok {
					return chat, from, nil
				}
			}
			continue
		}
		if ctx.Err() != nil {
			return 0, 0, ctx.Err()
		}
		if IsFatal(err) {
			return 0, 0, err
		}
		select {
		case <-time.After(3 * time.Second):
		case <-ctx.Done():
			return 0, 0, ctx.Err()
		}
	}
}

// groupOf picks the group chat and the human behind it out of one update.
func groupOf(u *update) (chatID, fromID int64, ok bool) {
	switch {
	case u.Message != nil && u.Message.Chat.ID < 0:
		if u.Message.From != nil && !u.Message.From.IsBot {
			return u.Message.Chat.ID, u.Message.From.ID, true
		}
	case u.MyChatMember != nil && u.MyChatMember.Chat.ID < 0:
		return u.MyChatMember.Chat.ID, u.MyChatMember.From.ID, true
	}
	return 0, 0, false
}

func (g *Gateway) dispatch(ctx context.Context, h ports.UpdateHandler, u *update) {
	switch {
	case u.CallbackQuery != nil:
		g.dispatchButton(ctx, h, u.CallbackQuery)
	case u.Message != nil:
		g.dispatchMessage(ctx, h, u.Message)
	case u.MyChatMember != nil && u.MyChatMember.Chat.ID == g.cfg.ChatID:
		h.BotRightsChanged(ctx, domain.BotRights{
			ChatID:             g.cfg.ChatID,
			CanManageTopics:    u.MyChatMember.NewChatMember.CanManageTopics,
			CanDeleteMessages:  u.MyChatMember.NewChatMember.CanDeleteMessages,
			CanPinMessages:     u.MyChatMember.NewChatMember.CanPinMessages,
			Status:             u.MyChatMember.NewChatMember.Status,
		})
	}
}

func (g *Gateway) dispatchButton(ctx context.Context, h ports.UpdateHandler, q *callbackQuery) {
	if q.Message == nil || q.Message.Chat.ID != g.cfg.ChatID {
		return
	}
	if !domain.Allowed(q.From.ID, g.cfg.Operators) {
		_ = g.AnswerCallback(ctx, q.ID, "not allowed", false)
		return
	}
	h.Button(ctx, domain.ButtonPress{
		ChatID:     q.Message.Chat.ID,
		ThreadID:   q.Message.MessageThreadID,
		MessageID:  q.Message.MessageID,
		FromID:     q.From.ID,
		CallbackID: q.ID,
		Data:       q.Data,
	})
}

func (g *Gateway) dispatchMessage(ctx context.Context, h ports.UpdateHandler, m *message) {
	if m.Chat.ID != g.cfg.ChatID {
		return
	}
	// The bot never receives its own posts as updates; what does arrive
	// from it is the service message one of its own topic calls generated.
	// Telegram refuses deleting the topic-created one, absorbed by client.
	if m.From != nil && m.From.ID == g.botID {
		if m.ForumTopicCreated != nil || m.ForumTopicEdited != nil ||
			m.ForumTopicClosed != nil || m.ForumTopicReopened != nil || m.PinnedMessage != nil {
			g.deleteLater(ctx, m.MessageID)
		}
		return
	}
	fromID := int64(0)
	if m.From != nil {
		fromID = m.From.ID
	}
	switch {
	case m.ForumTopicEdited != nil:
		if m.ForumTopicEdited.Name != "" {
			h.TopicEdited(ctx, domain.TopicEdit{
				ChatID:   m.Chat.ID,
				ThreadID: m.MessageThreadID,
				FromID:   fromID,
				Name:     m.ForumTopicEdited.Name,
				Icon:     domain.TopicIcon{CustomEmojiID: m.ForumTopicEdited.IconCustomEmojiID},
			})
		}
	case m.ForumTopicClosed != nil:
		h.TopicEdited(ctx, domain.TopicEdit{ChatID: m.Chat.ID, ThreadID: m.MessageThreadID, FromID: fromID, Closed: true})
	case m.ForumTopicReopened != nil:
		h.TopicEdited(ctx, domain.TopicEdit{ChatID: m.Chat.ID, ThreadID: m.MessageThreadID, FromID: fromID, Reopened: true})
	case m.Text != "":
		if !domain.Allowed(fromID, g.cfg.Operators) {
			return
		}
		tm := domain.TopicMessage{
			ChatID:    m.Chat.ID,
			ThreadID:  m.MessageThreadID,
			MessageID: m.MessageID,
			FromID:    fromID,
			Text:      m.Text,
		}
		if m.MessageThreadID != 0 {
			h.TopicMessage(ctx, tm)
		} else {
			h.GeneralMessage(ctx, tm)
		}
	default:
		if at := attachmentOf(m); at != nil && domain.Allowed(fromID, g.cfg.Operators) {
			h.TopicMessage(ctx, domain.TopicMessage{
				ChatID:     m.Chat.ID,
				ThreadID:   m.MessageThreadID,
				MessageID:  m.MessageID,
				FromID:     fromID,
				Caption:    m.Caption,
				Attachment: at,
			})
		}
	}
}

// deleteLater removes the group-notice service message the bot's own topic
// calls produce, late enough for the operator to see it flash by.
// ponytail: fixed delay; make it configurable when anyone asks.
func (g *Gateway) deleteLater(ctx context.Context, messageID int) {
	go func() {
		select {
		case <-time.After(noticeDelay):
		case <-ctx.Done():
			return
		}
		if err := g.Delete(ctx, g.cfg.ChatID, messageID); err != nil {
			g.log.Debug("delete service message",
				slog.Int("message_id", messageID), slog.String("err", err.Error()))
		}
	}()
}

// attachmentOf picks the file out of a message; the largest photo size is
// the last array entry.
func attachmentOf(m *message) *domain.Attachment {
	switch {
	case m.Document != nil:
		return &domain.Attachment{Kind: domain.AttachmentDocument, FileID: m.Document.FileID, Name: m.Document.FileName, MIME: m.Document.MIMEType, Size: m.Document.FileSize, GroupID: m.MediaGroupID}
	case len(m.Photo) > 0:
		p := m.Photo[len(m.Photo)-1]
		return &domain.Attachment{Kind: domain.AttachmentPhoto, FileID: p.FileID, Size: p.FileSize, GroupID: m.MediaGroupID}
	case m.Voice != nil:
		return &domain.Attachment{Kind: domain.AttachmentVoice, FileID: m.Voice.FileID, Size: m.Voice.FileSize, GroupID: m.MediaGroupID}
	case m.Audio != nil:
		return &domain.Attachment{Kind: domain.AttachmentAudio, FileID: m.Audio.FileID, MIME: m.Audio.MIMEType, Size: m.Audio.FileSize, GroupID: m.MediaGroupID}
	case m.Video != nil:
		return &domain.Attachment{Kind: domain.AttachmentVideo, FileID: m.Video.FileID, Name: m.Video.FileName, MIME: m.Video.MIMEType, Size: m.Video.FileSize, GroupID: m.MediaGroupID}
	}
	return nil
}
