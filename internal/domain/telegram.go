package domain

import "time"

// Outgoing is one message to post. Which chat and topic it lands in depends
// on ChatID and ThreadID: a zero ThreadID with the configured group id is
// the General topic, and a zero ThreadID with a private chat id is the
// pager message.
type Outgoing struct {
	ChatID   int64
	ThreadID int
	Text     string
	// Markdown asks for HTML parse mode (Telegram's HTML subset, which is
	// safer to generate than MarkdownV2).
	Markdown bool
	// Notify rings. Screen posts are silent by default so a busy agent
	// cannot spam a phone; only a question rings.
	Notify bool
	// Buttons becomes an inline keyboard, one button per row so long labels
	// stay readable.
	Buttons []Button
	// Footer is a trailing line rendered outside the code block, used for
	// the turn summary under a done post.
	Footer string
	// Fold collapses a long reply behind an expandable blockquote.
	Fold int
	// MaxParts caps how many messages a long text is split into; 0 means
	// one message only.
	MaxParts int
	// Silent disables the notification but keeps the message.
	Silent bool
}

// Button is one inline keyboard button. Data is what the callback carries
// back; the plugin keeps it short and uses its own vocabulary (a digit, a
// key name, or a namespaced verb such as "t:4" for the free-text row).
type Button struct {
	Text string
	Data string
}

// Button verb prefixes. Callback data is capped at 64 bytes by Telegram, so
// these stay short and the payload is an index rather than a pane id.
const (
	// CallbackSubmit is the "✔ Submit" row of a multi-select dialog.
	CallbackSubmit = "sub"
	// CallbackTextEntryPrefix marks the free-text row; the rest is the
	// option number the agent expects.
	CallbackTextEntryPrefix = "t:"
	// CallbackTogglePrefix marks a multi-select option; the rest is the
	// option number.
	CallbackTogglePrefix = "tg:"
	// CallbackYes and CallbackNo answer a yes/no dialog with a key rather
	// than a digit.
	CallbackYes = "y"
	CallbackNo  = "n"
)

// TopicIcon names a Telegram forum topic icon from its built-in pack. The
// plugin maps a status to an icon id; 0 means "leave as is".
type TopicIcon struct {
	// CustomEmojiID is the pack id, e.g. the ⚡ sticker id.
	CustomEmojiID string
}

// TopicMessage is a message an operator wrote.
type TopicMessage struct {
	ChatID    int64
	ThreadID  int
	MessageID int
	FromID    int64
	Text      string
	// Caption is the text under an attachment.
	Caption string
	// Attachment is set when the message carried a file.
	Attachment *Attachment
}

// AttachmentKind is the sort of file an operator sent.
type AttachmentKind string

const (
	AttachmentPhoto    AttachmentKind = "photo"
	AttachmentDocument AttachmentKind = "document"
	AttachmentVoice    AttachmentKind = "voice"
	AttachmentAudio    AttachmentKind = "audio"
	AttachmentVideo    AttachmentKind = "video"
)

// Attachment is a file sent into a topic, to be downloaded off the message
// loop and handed to the agent as an absolute path.
type Attachment struct {
	Kind   AttachmentKind
	FileID string
	Name   string
	MIME   string
	Size   int64
	// GroupID ties the parts of an album together, so an album becomes one
	// prompt rather than five.
	GroupID string
}

// ButtonPress is a press on an inline keyboard.
type ButtonPress struct {
	ChatID     int64
	ThreadID   int
	MessageID  int
	FromID     int64
	CallbackID string
	Data       string
}

// Verb returns the callback's verb, i.e. everything before the first colon.
func (b ButtonPress) Verb() string {
	for i := 0; i < len(b.Data); i++ {
		if b.Data[i] == ':' {
			return b.Data[:i]
		}
	}
	return b.Data
}

// Arg returns the callback's payload after the first colon, or "".
func (b ButtonPress) Arg() string {
	for i := 0; i < len(b.Data); i++ {
		if b.Data[i] == ':' {
			return b.Data[i+1:]
		}
	}
	return ""
}

// TopicEdit is a rename or icon change an operator made in Telegram. The
// plugin turns a rename into agent.rename and a close into a mute.
type TopicEdit struct {
	ChatID   int64
	ThreadID int
	FromID   int64
	Name     string
	Icon     TopicIcon
	Closed   bool
	Reopened bool
}

// BotRights is the bot's standing in the configured group.
type BotRights struct {
	ChatID            int64
	CanManageTopics   bool
	CanDeleteMessages bool
	CanPinMessages    bool
	// Status is "administrator", "member", "left", "kicked".
	Status string
}

// Operator can drive the agents.
type Operator struct {
	ID        int64
	Username  string
	FirstName string
	// Observer may read in General and use /status and /help, nothing else.
	Observer bool
	// Seen is the last time this id wrote in the group.
	Seen time.Time
}

// Allowed reports whether an id may act, and whether it is limited to
// reading.
func Allowed(id int64, operators []Operator) (ok bool) {
	for _, o := range operators {
		if o.ID == id {
			return true
		}
	}
	return false
}
