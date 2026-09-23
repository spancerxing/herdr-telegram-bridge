package app

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/spancerxing/herdr-telegram-bridge/internal/domain"
	"github.com/spancerxing/herdr-telegram-bridge/internal/ports"
)

// fakes for the two ports, plus guards that they keep satisfying them.

type promptReq struct{ pane, text string }

type keyReq struct {
	pane string
	keys []string
}

type fakeHerdr struct {
	mu sync.Mutex

	agents    []domain.Agent
	dialog    domain.Dialog
	dialogAfterKeys *domain.Dialog
	screen    domain.Screen
	readErr   error
	promptErr error
	typeErr   error
	listErr   error

	prompts       []promptReq
	typed         []promptReq
	keys          []keyReq
	subPanes      [][]string
	events        chan domain.Event
	subscriptions chan []string
}

var _ ports.Herdr = (*fakeHerdr)(nil)

func (f *fakeHerdr) Ping(context.Context) (domain.HerdrInfo, error) {
	return domain.HerdrInfo{Version: "test"}, nil
}

func (f *fakeHerdr) ListAgents(context.Context) ([]domain.Agent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.agents), f.listErr
}

func (f *fakeHerdr) ListWorkspaces(context.Context) ([]domain.Workspace, error) { return nil, nil }

func (f *fakeHerdr) CreateTab(context.Context, string) (domain.Tab, error) { return domain.Tab{}, nil }

func (f *fakeHerdr) RenameTab(context.Context, string, string) error { return nil }

func (f *fakeHerdr) ClosePane(context.Context, string) error { return nil }

func (f *fakeHerdr) ReadScreen(context.Context, string, domain.ScreenSource, int) (domain.Screen, error) {
	return f.screen, nil
}

func (f *fakeHerdr) ReadForDialog(context.Context, string, domain.Kind, int) (domain.Screen, domain.ScreenSource, domain.Dialog, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.readErr != nil {
		return domain.Screen{}, "", domain.Dialog{}, f.readErr
	}
	return f.screen, domain.ScreenDetection, f.dialog, nil
}

func (f *fakeHerdr) Prompt(_ context.Context, pane, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.promptErr != nil {
		return f.promptErr
	}
	f.prompts = append(f.prompts, promptReq{pane, text})
	return nil
}

func (f *fakeHerdr) TypeAndSubmit(_ context.Context, pane, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.typeErr != nil {
		return f.typeErr
	}
	f.typed = append(f.typed, promptReq{pane, text})
	return nil
}

func (f *fakeHerdr) SendKeys(_ context.Context, pane string, keys []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keys = append(f.keys, keyReq{pane, slices.Clone(keys)})
	if f.dialogAfterKeys != nil {
		f.dialog = *f.dialogAfterKeys
	}
	return nil
}

func (f *fakeHerdr) Focus(context.Context, string) error           { return nil }
func (f *fakeHerdr) Rename(context.Context, string, *string) error { return nil }

func (f *fakeHerdr) StartAgent(context.Context, string, string, string, time.Duration) (domain.Agent, error) {
	return domain.Agent{}, nil
}

func (f *fakeHerdr) WaitStatus(_ context.Context, _ string, _ []domain.Status, _ time.Duration) (domain.Status, error) {
	return domain.StatusUnknown, nil
}

func (f *fakeHerdr) Subscribe(_ context.Context, paneIDs []string) (<-chan domain.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subPanes = append(f.subPanes, slices.Clone(paneIDs))
	if f.subscriptions != nil {
		f.subscriptions <- slices.Clone(paneIDs)
	}
	return f.events, nil
}

func (f *fakeHerdr) Notify(context.Context, string, string, domain.NotifySound) error { return nil }

func (f *fakeHerdr) IntegrationList(context.Context) ([]domain.Integration, error) { return nil, nil }

func (f *fakeHerdr) InstallIntegration(context.Context, string) error { return nil }

func (f *fakeHerdr) keyLog() []keyReq {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.keys)
}

func (f *fakeHerdr) promptLog() []promptReq {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.prompts)
}

type keyboardEdit struct {
	messageID int
	buttons   []domain.Button
}

type answerReq struct{ callbackID, text string }

type createReq struct {
	name string
	icon domain.TopicIcon
}

type editTopicReq struct {
	thread int
	name   string
	icon   domain.TopicIcon
}

type editTextReq struct {
	messageID int
	text      string
}

type fakeTelegram struct {
	mu sync.Mutex

	sent           []domain.Outgoing
	keyboards      []keyboardEdit
	answers        []answerReq
	created        []createReq
	edited         []editTopicReq
	editedText     []editTextReq
	pinned         []int
	closed         []int
	deleted        []int
	reopened       []int
	sendErr        error
	deleteTopicErr error
	closeTopicErr  error
	nextThread     int
	nextMessage    int

	icons map[domain.Status]domain.TopicIcon
}

var _ ports.Telegram = (*fakeTelegram)(nil)

func (f *fakeTelegram) Send(_ context.Context, out domain.Outgoing) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sendErr != nil {
		return 0, f.sendErr
	}
	f.sent = append(f.sent, out)
	f.nextMessage++
	return f.nextMessage, nil
}

func (f *fakeTelegram) EditText(_ context.Context, _ int64, messageID int, text string, _ bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.editedText = append(f.editedText, editTextReq{messageID, text})
	return nil
}

func (f *fakeTelegram) EditKeyboard(_ context.Context, _ int64, messageID int, buttons []domain.Button) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keyboards = append(f.keyboards, keyboardEdit{messageID, slices.Clone(buttons)})
	return nil
}

func (f *fakeTelegram) AnswerCallback(_ context.Context, callbackID, text string, _ bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers = append(f.answers, answerReq{callbackID, text})
	return nil
}

func (f *fakeTelegram) Pin(_ context.Context, _ int64, messageID int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pinned = append(f.pinned, messageID)
	return nil
}
func (f *fakeTelegram) Delete(context.Context, int64, int) error { return nil }

func (f *fakeTelegram) CreateTopic(_ context.Context, _ int64, name string, icon domain.TopicIcon) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, createReq{name, icon})
	f.nextThread++
	return 100 + f.nextThread, nil
}

func (f *fakeTelegram) EditTopic(_ context.Context, _ int64, threadID int, name string, icon domain.TopicIcon) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.edited = append(f.edited, editTopicReq{threadID, name, icon})
	return nil
}

func (f *fakeTelegram) CloseTopic(_ context.Context, _ int64, threadID int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = append(f.closed, threadID)
	return f.closeTopicErr
}

func (f *fakeTelegram) DeleteTopic(_ context.Context, _ int64, threadID int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deleteTopicErr != nil {
		return f.deleteTopicErr
	}
	f.deleted = append(f.deleted, threadID)
	return nil
}

func (f *fakeTelegram) ReopenTopic(_ context.Context, _ int64, threadID int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reopened = append(f.reopened, threadID)
	return nil
}

func (f *fakeTelegram) IconFor(s domain.Status) domain.TopicIcon {
	return f.icons[s]
}

func (f *fakeTelegram) Stream(ctx context.Context, _ ports.UpdateHandler) error {
	<-ctx.Done()
	return nil
}

func (f *fakeTelegram) sentLog() []domain.Outgoing {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.sent)
}

func (f *fakeTelegram) keyboardLog() []keyboardEdit {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.keyboards)
}

func (f *fakeTelegram) answerLog() []answerReq {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.answers)
}

type fakeIdleChecker struct {
	idle time.Duration
	err  error
}

func (f fakeIdleChecker) IdleFor(context.Context) (time.Duration, error) {
	return f.idle, f.err
}
