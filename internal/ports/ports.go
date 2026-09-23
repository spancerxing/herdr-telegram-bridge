// Package ports declares what the application layer needs from the outside
// world. Adapters implement these; the app never imports an adapter.
package ports

import (
	"context"
	"time"

	"github.com/spancerxing/herdr-telegram-bridge/internal/domain"
)

// Herdr is the subset of the Herdr socket API this plugin uses. Every
// method takes a context so a hung Herdr cannot wedge a Telegram reply.
type Herdr interface {
	// Ping reports the server version and protocol, so the daemon can warn
	// when it is talking to a protocol it was not written for.
	Ping(ctx context.Context) (domain.HerdrInfo, error)

	// ListAgents is the source of truth for which agents exist and their
	// current status. Everything the reconciler does starts here.
	ListAgents(ctx context.Context) ([]domain.Agent, error)
	ListWorkspaces(ctx context.Context) ([]domain.Workspace, error)

	// CreateTab opens an unfocused tab in a workspace and returns its root
	// pane, which is where a new agent is started.
	CreateTab(ctx context.Context, workspaceID string) (domain.Tab, error)
	RenameTab(ctx context.Context, tabID, label string) error
	ClosePane(ctx context.Context, paneID string) error

	// ReadScreen reads pane text. source matters: see domain.ScreenSource,
	// because agent.read refuses "recent" for a working alternate-screen
	// pane on protocol 22.
	ReadScreen(ctx context.Context, paneID string, source domain.ScreenSource, lines int) (domain.Screen, error)

	// ReadForDialog reads a blocked pane across the blocked sources and
	// parses its dialog; when no source has one, the first readable screen
	// is returned for the plain-text fallback.
	ReadForDialog(ctx context.Context, paneID string, kind domain.Kind, lines int) (domain.Screen, domain.ScreenSource, domain.Dialog, error)

	// Prompt types text into the agent and submits it. Implementations must
	// reach the terminal even for an agent the server still considers
	// launching: agent.prompt refuses those with agent_not_ready while the
	// pane accepts input normally.
	Prompt(ctx context.Context, paneID, text string) error
	// TypeAndSubmit answers an already-open text input through the pane.
	// Unlike Prompt, it can reach a blocked agent's interactive composer.
	TypeAndSubmit(ctx context.Context, paneID, text string) error
	// SendKeys sends raw key names, for answering dialogs and for /keys.
	SendKeys(ctx context.Context, paneID string, keys []string) error
	Focus(ctx context.Context, paneID string) error
	// Rename sets the agent's custom name; nil clears it.
	Rename(ctx context.Context, paneID string, name *string) error

	// StartAgent starts an agent of a kind in an existing pane and waits
	// for Herdr to detect it.
	StartAgent(ctx context.Context, name, kind, paneID string, timeout time.Duration) (domain.Agent, error)

	// WaitStatus blocks until the pane reaches one of the wanted statuses
	// or the timeout expires. This replaces polling: a status change is a
	// event the server itself waits on.
	WaitStatus(ctx context.Context, paneID string, until []domain.Status, timeout time.Duration) (domain.Status, error)

	// Subscribe streams status and lifecycle events. The returned channel is
	// closed when ctx ends or the connection cannot be re-established.
	// Implementations must reconnect internally and re-arm their
	// subscriptions, and must not lose the initial status snapshot.
	Subscribe(ctx context.Context, paneIDs []string) (<-chan domain.Event, error)

	// Notify raises a Herdr desktop notification, for the reverse direction:
	// something that happened in Telegram and is worth showing at the desk.
	Notify(ctx context.Context, title, body string, sound domain.NotifySound) error

	// IntegrationList reports which agent kinds have their state hook
	// installed on this machine, and where.
	IntegrationList(ctx context.Context) ([]domain.Integration, error)
	// InstallIntegration writes a kind's bundled hook. This modifies the
	// agent CLI's own config directory, so callers must ask first.
	InstallIntegration(ctx context.Context, target string) error
}

// Telegram is the subset of the Telegram Bot API this plugin uses. Calls are
// shaped as the plugin's own types so the app never sees the wire format.
type Telegram interface {
	// Send posts a message into a topic. A zero ThreadID targets the
	// General topic; a zero ThreadID together with a private ChatID targets
	// the operator's private chat.
	Send(ctx context.Context, out domain.Outgoing) (int, error)
	// EditText replaces a message's text, keeping its thread.
	EditText(ctx context.Context, chatID int64, messageID int, text string, markdown bool) error
	// EditKeyboard replaces only a message's buttons, which is how a dialog
	// post is retired once it is answered.
	EditKeyboard(ctx context.Context, chatID int64, messageID int, buttons []domain.Button) error
	// AnswerCallback closes the spinner on a button press.
	AnswerCallback(ctx context.Context, callbackID, text string, alert bool) error
	// Pin pins the dashboard message in General.
	Pin(ctx context.Context, chatID int64, messageID int) error
	// Delete removes a message, for the transient topic-icon notices.
	Delete(ctx context.Context, chatID int64, messageID int) error
	// CreateTopic opens a forum topic and returns its thread id.
	CreateTopic(ctx context.Context, chatID int64, name string, icon domain.TopicIcon) (int, error)
	// EditTopic renames a topic and/or changes its icon.
	EditTopic(ctx context.Context, chatID int64, threadID int, name string, icon domain.TopicIcon) error
	// CloseTopic closes a forum topic.
	CloseTopic(ctx context.Context, chatID int64, threadID int) error
	// DeleteTopic deletes a forum topic and all messages inside it.
	DeleteTopic(ctx context.Context, chatID int64, threadID int) error
	// ReopenTopic reopens a closed forum topic.
	ReopenTopic(ctx context.Context, chatID int64, threadID int) error
	// IconFor maps a status to the topic icon the group will show, from the
	// default forum-topic sticker pack; a zero TopicIcon leaves it unchanged.
	IconFor(status domain.Status) domain.TopicIcon

	// Stream polls updates and hands them to the handler until ctx ends.
	// Implementations must apply the operator check before dispatching.
	Stream(ctx context.Context, h UpdateHandler) error
}

// UpdateHandler receives normalised Telegram updates.
type UpdateHandler interface {
	// TopicMessage is a message written in an agent's topic.
	TopicMessage(ctx context.Context, m domain.TopicMessage)
	// GeneralMessage is a message written in the General topic.
	GeneralMessage(ctx context.Context, m domain.TopicMessage)
	// Button is a press on an inline keyboard the plugin posted.
	Button(ctx context.Context, b domain.ButtonPress)
	// TopicEdited is a forum topic rename or icon change made by hand.
	TopicEdited(ctx context.Context, e domain.TopicEdit)
	// BotRightsChanged reports a change in the bot's group rights.
	BotRightsChanged(ctx context.Context, r domain.BotRights)
}

// Clock is injectable so tests can drive debounce and TTL logic without
// sleeping.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

// Replier is the reverse path into whatever is showing Herdr's UI, used for
// action outcomes. Kept separate from Herdr so the app can be tested without
// a Herdr process.
type Replier interface {
	// Notify reports an outcome to the person who invoked an action.
	Notify(text string) error
}
