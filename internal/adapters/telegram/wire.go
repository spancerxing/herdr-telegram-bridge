package telegram

import "encoding/json"

// The wire types: only the fields this plugin reads or sends. Method
// params carry json tags on every field, because Telegram rejects Go field
// names.

type tgResponse struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	ErrorCode   int             `json:"error_code"`
	Description string          `json:"description"`
	Parameters  *struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

type messageIDResult struct {
	MessageID int `json:"message_id"`
}

type threadIDResult struct {
	MessageThreadID int `json:"message_thread_id"`
}

type userResult struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	IsBot    bool   `json:"is_bot"`
}

type replyMarkup struct {
	InlineKeyboard [][]inlineButton `json:"inline_keyboard"`
}

type inlineButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data"`
}

type sendMessageParams struct {
	ChatID              int64        `json:"chat_id"`
	MessageThreadID     int          `json:"message_thread_id,omitempty"`
	Text                string       `json:"text"`
	ParseMode           string       `json:"parse_mode,omitempty"`
	DisableNotification bool         `json:"disable_notification"`
	ReplyMarkup         *replyMarkup `json:"reply_markup,omitempty"`
}

type editMessageTextParams struct {
	ChatID    int64  `json:"chat_id"`
	MessageID int    `json:"message_id"`
	Text      string `json:"text"`
	ParseMode string `json:"parse_mode,omitempty"`
}

type editMessageReplyMarkupParams struct {
	ChatID      int64        `json:"chat_id"`
	MessageID   int          `json:"message_id"`
	ReplyMarkup *replyMarkup `json:"reply_markup"` // never omitted: empty means "remove the keyboard"
}

type answerCallbackQueryParams struct {
	CallbackQueryID string `json:"callback_query_id"`
	Text            string `json:"text,omitempty"`
	ShowAlert       bool   `json:"show_alert,omitempty"`
}

type pinChatMessageParams struct {
	ChatID              int64 `json:"chat_id"`
	MessageID           int   `json:"message_id"`
	DisableNotification bool  `json:"disable_notification"`
}

type deleteMessageParams struct {
	ChatID    int64 `json:"chat_id"`
	MessageID int   `json:"message_id"`
}

type createForumTopicParams struct {
	ChatID            int64  `json:"chat_id"`
	Name              string `json:"name"`
	IconCustomEmojiID string `json:"icon_custom_emoji_id,omitempty"`
}

type editForumTopicParams struct {
	ChatID            int64  `json:"chat_id"`
	MessageThreadID   int    `json:"message_thread_id"`
	Name              string `json:"name,omitempty"`
	IconCustomEmojiID string `json:"icon_custom_emoji_id,omitempty"`
}

type closeForumTopicParams struct {
	ChatID          int64 `json:"chat_id"`
	MessageThreadID int   `json:"message_thread_id"`
}

type deleteWebhookParams struct {
	DropPendingUpdates bool `json:"drop_pending_updates"`
}

type getUpdatesParams struct {
	Offset         int      `json:"offset"`
	Timeout        int      `json:"timeout"`
	AllowedUpdates []string `json:"allowed_updates"`
}

// ---- inbound updates ----

type update struct {
	UpdateID      int                `json:"update_id"`
	Message       *message           `json:"message"`
	CallbackQuery *callbackQuery     `json:"callback_query"`
	MyChatMember  *chatMemberUpdated `json:"my_chat_member"`
}

type chat struct {
	ID int64 `json:"id"`
}

type user struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	IsBot    bool   `json:"is_bot"`
}

type message struct {
	MessageID       int    `json:"message_id"`
	From            *user  `json:"from"`
	Chat            chat   `json:"chat"`
	MessageThreadID int    `json:"message_thread_id"`
	Text            string `json:"text"`
	Caption         string `json:"caption"`
	MediaGroupID    string `json:"media_group_id"`

	ForumTopicCreated   *struct{}         `json:"forum_topic_created"`
	ForumTopicEdited    *forumTopicEdited `json:"forum_topic_edited"`
	ForumTopicClosed    *struct{}         `json:"forum_topic_closed"`
	ForumTopicReopened  *struct{}         `json:"forum_topic_reopened"`
	PinnedMessage       *struct{}         `json:"pinned_message"`
	Document            *document         `json:"document"`
	Photo               []photo           `json:"photo"`
	Voice               *file             `json:"voice"`
	Audio               *document         `json:"audio"`
	Video               *document         `json:"video"`
}

type forumTopicEdited struct {
	Name              string `json:"name"`
	IconCustomEmojiID string `json:"icon_custom_emoji_id"`
}

type document struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
	MIMEType string `json:"mime_type"`
	FileSize int64  `json:"file_size"`
}

type photo struct {
	FileID   string `json:"file_id"`
	FileSize int64  `json:"file_size"`
}

// file is the voice shape: no file name, only a size.
type file struct {
	FileID   string `json:"file_id"`
	FileSize int64  `json:"file_size"`
}

type callbackQuery struct {
	ID      string   `json:"id"`
	From    user     `json:"from"`
	Message *message `json:"message"`
	Data    string   `json:"data"`
}

type chatMemberUpdated struct {
	Chat          chat       `json:"chat"`
	From          user       `json:"from"`
	NewChatMember chatMember `json:"new_chat_member"`
}

type chatMember struct {
	Status            string `json:"status"`
	CanManageTopics   bool   `json:"can_manage_topics"`
	CanDeleteMessages bool   `json:"can_delete_messages"`
	CanPinMessages    bool   `json:"can_pin_messages"`
}
