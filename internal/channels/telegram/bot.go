// Package telegram provides the Telegram bot adapter for GoClaw.
package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	tele "gopkg.in/telebot.v4"

	"github.com/roelfdiedericks/goclaw/internal/acp"
	"github.com/roelfdiedericks/goclaw/internal/bus"
	"github.com/roelfdiedericks/goclaw/internal/channels/telegram/config"
	chtypes "github.com/roelfdiedericks/goclaw/internal/channels/types"
	"github.com/roelfdiedericks/goclaw/internal/commands"
	"github.com/roelfdiedericks/goclaw/internal/delegatedrun"
	"github.com/roelfdiedericks/goclaw/internal/delivery"
	"github.com/roelfdiedericks/goclaw/internal/gateway"
	"github.com/roelfdiedericks/goclaw/internal/llm"
	"github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/media"
	"github.com/roelfdiedericks/goclaw/internal/session"
	"github.com/roelfdiedericks/goclaw/internal/types"
	"github.com/roelfdiedericks/goclaw/internal/user"
)

// ChatPreferences stores per-chat preferences
type ChatPreferences struct {
	ShowThinking  bool   // Show tool calls and thinking output
	ThinkingLevel string // Thinking intensity: off/minimal/low/medium/high/xhigh
}

// Bot represents the Telegram bot
type Bot struct {
	bot     *tele.Bot
	gateway *gateway.Gateway
	users   *user.Registry
	config  *config.Config

	// Per-chat preferences
	chatPrefs sync.Map // chatID (int64) -> *ChatPreferences

	ctx    context.Context
	cancel context.CancelFunc

	// State tracking for ManagedChannel interface
	mu        sync.RWMutex
	running   bool
	startedAt time.Time
	lastError error

	delegatedSubs []bus.SubscriptionID
	delegatedMu   sync.Mutex
	delegatedLast map[string]time.Time

	interactiveMu     sync.Mutex
	interactiveSeq    int64
	interactiveStates map[string]*telegramInteractiveState
	interactivePolls  map[string]string
}

type telegramInteractiveState struct {
	ID             string
	SessionKey     string
	Driver         string
	Method         string
	ToolCallID     string
	ChatID         int64
	MessageID      int
	Question       acp.QuestionPayload
	Plan           acp.PlanRequestPayload
	Selected       map[string]map[string]bool
	OtherRequested bool
	PollID         string
	PollMessageID  int
	PollOptionIDs  []string
}

// getChatPrefs returns preferences for a chat, creating if needed.
// If user is provided and prefs don't exist, initializes from user preferences.
func (b *Bot) getChatPrefs(chatID int64, u *user.User) *ChatPreferences {
	if prefs, ok := b.chatPrefs.Load(chatID); ok {
		return prefs.(*ChatPreferences) //nolint:errcheck // type assertion safe - we only store *ChatPreferences
	}
	// Initialize from user preference if available
	showThinking := false
	thinkingLevel := ""
	if u != nil {
		showThinking = u.Thinking
		thinkingLevel = u.ThinkingLevel
	}
	prefs := &ChatPreferences{
		ShowThinking:  showThinking,
		ThinkingLevel: thinkingLevel,
	}
	b.chatPrefs.Store(chatID, prefs)
	return prefs
}

// New creates a new Telegram bot
func New(cfg *config.Config, gw *gateway.Gateway, users *user.Registry) (*Bot, error) {
	if cfg.BotToken == "" {
		return nil, fmt.Errorf("telegram bot token not configured")
	}

	pref := tele.Settings{
		Token:  cfg.BotToken,
		Poller: &tele.LongPoller{Timeout: 10 * time.Second},
	}

	logging.L_debug("telegram: creating bot", "tokenLength", len(cfg.BotToken))

	bot, err := tele.NewBot(pref)
	if err != nil {
		return nil, fmt.Errorf("failed to create telegram bot: %w", err)
	}

	// Log bot info - confirm connection worked and show identity
	logging.L_info("telegram: connected",
		"bot", "@"+bot.Me.Username,
		"name", bot.Me.FirstName,
		"id", bot.Me.ID,
		"canJoinGroups", bot.Me.CanJoinGroups,
	)

	ctx, cancel := context.WithCancel(context.Background())

	b := &Bot{
		bot:               bot,
		gateway:           gw,
		users:             users,
		config:            cfg,
		ctx:               ctx,
		cancel:            cancel,
		delegatedLast:     make(map[string]time.Time),
		interactiveStates: make(map[string]*telegramInteractiveState),
		interactivePolls:  make(map[string]string),
	}

	// Register handlers
	b.setupHandlers()
	b.subscribeDelegatedRunEvents()
	logging.L_debug("telegram: handlers registered")

	return b, nil
}

func (b *Bot) subscribeDelegatedRunEvents() {
	topics := []string{
		"delegated.run.started",
		"delegated.run.completed",
		"delegated.run.failed",
		"delegated.run.canceled",
	}
	for _, topic := range topics {
		topic := topic
		subID := bus.SubscribeEvent(topic, func(ev bus.Event) {
			b.onDelegatedRunEvent(topic, ev.Data)
		})
		b.delegatedSubs = append(b.delegatedSubs, subID)
	}
}

func (b *Bot) onDelegatedRunEvent(topic string, payload any) {
	owner := b.users.Owner()
	if owner == nil || owner.TelegramID == "" {
		return
	}

	var text string
	switch topic {
	case "delegated.run.started":
		ev, ok := payload.(delegatedrun.StartedEvent)
		if !ok {
			return
		}
		text = fmt.Sprintf("Runner started: %s (%s via %s)", shortRunID(ev.RunID), blankDefault(ev.Purpose, "unspecified"), ev.RequesterType)
	case "delegated.run.completed":
		ev, ok := payload.(delegatedrun.CompletedEvent)
		if !ok {
			return
		}
		elapsed := ev.FinishedAt.Sub(ev.StartedAt).Round(time.Second)
		text = fmt.Sprintf("Runner completed: %s in %s", shortRunID(ev.RunID), elapsed)
	case "delegated.run.failed":
		ev, ok := payload.(delegatedrun.FailedEvent)
		if !ok {
			return
		}
		text = fmt.Sprintf("Runner failed: %s (%s)", shortRunID(ev.RunID), truncate(strings.TrimSpace(ev.Error), 120))
	case "delegated.run.canceled":
		ev, ok := payload.(delegatedrun.CanceledEvent)
		if !ok {
			return
		}
		text = fmt.Sprintf("Runner canceled: %s", shortRunID(ev.RunID))
	default:
		return
	}

	// De-dupe rapid duplicate lifecycle emits to keep owner notifications concise.
	key := topic + ":" + text
	b.delegatedMu.Lock()
	lastAt, seen := b.delegatedLast[key]
	if seen && time.Since(lastAt) < 2*time.Second {
		b.delegatedMu.Unlock()
		return
	}
	b.delegatedLast[key] = time.Now()
	b.delegatedMu.Unlock()

	chatID := parseInt64OrZero(owner.TelegramID)
	if chatID <= 0 {
		return
	}
	if _, err := b.SendText(chatID, text); err != nil {
		logging.L_debug("telegram: delegated summary delivery failed", "topic", topic, "error", err)
	}
}

func shortRunID(runID string) string {
	runID = strings.TrimSpace(runID)
	if len(runID) <= 8 {
		return runID
	}
	return runID[:8]
}

func blankDefault(v, fallback string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return fallback
	}
	return v
}

func parseInt64OrZero(s string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func (b *Bot) nextInteractiveID() string {
	b.interactiveMu.Lock()
	defer b.interactiveMu.Unlock()
	b.interactiveSeq++
	return strconv.FormatInt(b.interactiveSeq, 36)
}

func (b *Bot) putInteractiveState(state *telegramInteractiveState) {
	if state == nil || state.ID == "" {
		return
	}
	b.interactiveMu.Lock()
	defer b.interactiveMu.Unlock()
	b.interactiveStates[state.ID] = state
	if strings.TrimSpace(state.PollID) != "" {
		b.interactivePolls[state.PollID] = state.ID
	}
}

func (b *Bot) getInteractiveState(id string) *telegramInteractiveState {
	b.interactiveMu.Lock()
	defer b.interactiveMu.Unlock()
	return b.interactiveStates[id]
}

func (b *Bot) popInteractiveState(id string) *telegramInteractiveState {
	b.interactiveMu.Lock()
	defer b.interactiveMu.Unlock()
	state := b.interactiveStates[id]
	delete(b.interactiveStates, id)
	if state != nil && strings.TrimSpace(state.PollID) != "" {
		delete(b.interactivePolls, state.PollID)
	}
	return state
}

func (b *Bot) popInteractiveStateByPollID(pollID string) *telegramInteractiveState {
	pollID = strings.TrimSpace(pollID)
	if pollID == "" {
		return nil
	}
	b.interactiveMu.Lock()
	defer b.interactiveMu.Unlock()
	stateID := b.interactivePolls[pollID]
	if stateID == "" {
		return nil
	}
	state := b.interactiveStates[stateID]
	delete(b.interactivePolls, pollID)
	delete(b.interactiveStates, stateID)
	return state
}

func (b *Bot) popInteractiveStatesForPending(sessionKey string, pending []acp.AttachmentPendingRequestInfo) []*telegramInteractiveState {
	if strings.TrimSpace(sessionKey) == "" || len(pending) == 0 {
		return nil
	}
	byKey := make(map[string]struct{}, len(pending))
	for _, item := range pending {
		key := strings.TrimSpace(item.Method) + "::" + strings.TrimSpace(item.ToolCallID)
		byKey[key] = struct{}{}
	}

	b.interactiveMu.Lock()
	defer b.interactiveMu.Unlock()

	var states []*telegramInteractiveState
	for id, state := range b.interactiveStates {
		if state == nil || state.SessionKey != sessionKey {
			continue
		}
		key := strings.TrimSpace(state.Method) + "::" + strings.TrimSpace(state.ToolCallID)
		if _, ok := byKey[key]; !ok {
			continue
		}
		states = append(states, state)
		if strings.TrimSpace(state.PollID) != "" {
			delete(b.interactivePolls, state.PollID)
		}
		delete(b.interactiveStates, id)
	}
	return states
}

func (b *Bot) telegramQuestionText(state *telegramInteractiveState, resolved string) string {
	var sb strings.Builder
	title := strings.TrimSpace(state.Question.Title)
	if title == "" {
		title = "Question"
	}
	sb.WriteString("❓ <b>" + escapeHTML(title) + "</b>\n")
	for _, question := range state.Question.Questions {
		sb.WriteString("\n<b>" + escapeHTML(question.Prompt) + "</b>\n")
		for _, option := range b.telegramQuestionOptions(question) {
			selected := false
			if state.Selected != nil {
				if byQuestion, ok := state.Selected[question.ID]; ok && byQuestion != nil {
					selected = byQuestion[option.ID]
				}
			}
			prefix := "▫️"
			if selected {
				prefix = "✅"
			}
			sb.WriteString(prefix + " " + escapeHTML(option.Label) + "\n")
		}
		if question.AllowMultiple {
			sb.WriteString("<i>Select one or more options, then tap Submit.</i>\n")
		}
	}
	if state.OtherRequested {
		sb.WriteString("\n<i>Continue in chat with your custom answer.</i>\n")
	}
	if resolved != "" {
		sb.WriteString("\n<b>" + escapeHTML(resolved) + "</b>")
	}
	return sb.String()
}

func (b *Bot) telegramQuestionMarkup(state *telegramInteractiveState) *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	rows := make([]tele.Row, 0, len(state.Question.Questions)+1)
	hasMulti := false
	for qi, question := range state.Question.Questions {
		btns := make([]tele.Btn, 0, len(question.Options))
		for oi, option := range b.telegramQuestionOptions(question) {
			label := option.Label
			if question.AllowMultiple && state.Selected != nil {
				if byQuestion, ok := state.Selected[question.ID]; ok && byQuestion != nil && byQuestion[option.ID] {
					label = "✅ " + label
				}
			}
			action := "single"
			if b.telegramQuestionOptionIsOther(option) {
				action = "other"
			} else if question.AllowMultiple {
				action = "toggle"
				hasMulti = true
			}
			btns = append(btns, menu.Data(label, "acp", state.ID, action, strconv.Itoa(qi), strconv.Itoa(oi)))
		}
		if len(btns) > 0 {
			rows = append(rows, menu.Split(2, btns)...)
		}
	}
	if hasMulti {
		rows = append(rows, menu.Row(menu.Data("Submit", "acp", state.ID, "submit")))
	}
	menu.Inline(rows...)
	return menu
}

func (b *Bot) telegramQuestionOptions(question acp.QuestionItem) []acp.QuestionOption {
	options := append([]acp.QuestionOption(nil), question.Options...)
	hasOther := false
	for _, option := range options {
		if b.telegramQuestionOptionIsOther(option) {
			hasOther = true
			break
		}
	}
	if !hasOther {
		options = append(options, acp.QuestionOption{ID: "__other__", Label: "Other..."})
	}
	return options
}

func (b *Bot) telegramQuestionOptionIsOther(option acp.QuestionOption) bool {
	id := strings.ToLower(strings.TrimSpace(option.ID))
	label := strings.ToLower(strings.TrimSpace(option.Label))
	return id == "other" || id == "__other__" || label == "other" || strings.HasPrefix(label, "other...")
}

func (b *Bot) shouldUseTelegramPollForQuestion(payload acp.QuestionPayload) bool {
	if len(payload.Questions) != 1 {
		return false
	}
	question := payload.Questions[0]
	if !question.AllowMultiple {
		return false
	}
	options := b.telegramQuestionOptions(question)
	return len(options) >= 2 && len(options) <= 12
}

func (b *Bot) telegramPlanText(payload acp.PlanRequestPayload, resolved string) string {
	var sb strings.Builder
	title := strings.TrimSpace(payload.Name)
	if title == "" {
		title = "Plan approval"
	}
	sb.WriteString("🗂 <b>" + escapeHTML(title) + "</b>\n")
	if strings.TrimSpace(payload.Overview) != "" {
		sb.WriteString("\n" + escapeHTML(payload.Overview) + "\n")
	}
	if len(payload.Todos) > 0 {
		sb.WriteString("\n<b>Todos</b>\n")
		limit := len(payload.Todos)
		if limit > 5 {
			limit = 5
		}
		for i := 0; i < limit; i++ {
			sb.WriteString("• " + escapeHTML(payload.Todos[i].Content) + "\n")
		}
	}
	if strings.TrimSpace(payload.Plan) != "" {
		sb.WriteString("\n<pre>" + escapeHTML(truncate(payload.Plan, 1200)) + "</pre>")
	}
	if resolved != "" {
		sb.WriteString("\n<b>" + escapeHTML(resolved) + "</b>")
	}
	return sb.String()
}

func (b *Bot) telegramPlanMarkup(state *telegramInteractiveState) *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	menu.Inline(
		menu.Row(
			menu.Data("Approve", "acp", state.ID, "approve"),
			menu.Data("Reject", "acp", state.ID, "reject"),
		),
	)
	return menu
}

func (b *Bot) markTelegramInteractiveStateCancelled(state *telegramInteractiveState, reason string) {
	if state == nil || state.ChatID == 0 || state.MessageID == 0 {
		return
	}
	reason = blankDefault(reason, "Cancelled because you continued in chat.")
	ref := b.telegramMessageRef(state)
	var text string
	switch state.Method {
	case "cursor/create_plan":
		text = b.telegramPlanText(state.Plan, reason)
	default:
		text = b.telegramQuestionText(state, reason)
	}
	if _, err := b.bot.Edit(ref, text, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: &tele.ReplyMarkup{},
	}); err != nil {
		logging.L_debug("telegram: failed to mark interaction cancelled", "method", state.Method, "toolCallID", state.ToolCallID, "error", err)
	}
}

func (b *Bot) telegramMessageRef(state *telegramInteractiveState) *tele.Message {
	if state == nil {
		return nil
	}
	return &tele.Message{
		ID:   state.MessageID,
		Chat: &tele.Chat{ID: state.ChatID},
	}
}

func (b *Bot) telegramQuestionAnswers(state *telegramInteractiveState) ([]acp.QuestionAnswer, error) {
	answers := make([]acp.QuestionAnswer, 0, len(state.Question.Questions))
	for _, question := range state.Question.Questions {
		var selected []string
		if byQuestion, ok := state.Selected[question.ID]; ok && byQuestion != nil {
			for _, option := range question.Options {
				if byQuestion[option.ID] {
					selected = append(selected, option.ID)
				}
			}
		}
		if len(selected) == 0 {
			return nil, fmt.Errorf("answer all questions before submitting")
		}
		answers = append(answers, acp.QuestionAnswer{
			QuestionID:        question.ID,
			SelectedOptionIDs: selected,
		})
	}
	return answers, nil
}

// setupHandlers registers message handlers
func (b *Bot) setupHandlers() {
	// Handle all text messages
	b.bot.Handle(tele.OnText, b.handleMessage)

	// Handle photo messages
	b.bot.Handle(tele.OnPhoto, b.handlePhoto)

	// Handle voice messages
	b.bot.Handle(tele.OnVoice, b.handleVoice)

	// Handle ACP interactive callback buttons.
	b.bot.Handle(&tele.Btn{Unique: "acp"}, b.handleACPCallback)
	b.bot.Handle(tele.OnPollAnswer, b.handleACPPollAnswer)

	// Handle /start command (Telegram-specific, not in global registry)
	b.bot.Handle("/start", func(c tele.Context) error {
		return c.Send("Hello! I'm GoClaw, your AI assistant. Send me a message to get started.")
	})

	// Handle /thinking command (channel-specific preference)
	b.bot.Handle("/thinking", func(c tele.Context) error {
		chatID := c.Chat().ID
		userID := fmt.Sprintf("%d", c.Sender().ID)
		u := b.users.FromIdentity("telegram", userID)

		// Check command permission
		if !b.canUserUseCommands(u) {
			logging.L_debug("telegram: commands disabled for user", "user", u.Name, "command", "/thinking")
			return nil // Silently ignore - treat as if they sent a message
		}

		prefs := b.getChatPrefs(chatID, u)

		// Parse subcommand
		arg := strings.ToLower(strings.TrimSpace(c.Message().Payload))

		var resultMsg string
		switch arg {
		case "on":
			prefs.ShowThinking = true
			if prefs.ThinkingLevel == "" || prefs.ThinkingLevel == "off" {
				prefs.ThinkingLevel = llm.DefaultThinkingLevel.String()
			}
			resultMsg = fmt.Sprintf("Thinking output enabled (level: %s).", prefs.ThinkingLevel)
		case "off":
			prefs.ShowThinking = false
			prefs.ThinkingLevel = "off"
			resultMsg = "Thinking output disabled. You'll only see final responses."
		case "toggle", "":
			prefs.ShowThinking = !prefs.ShowThinking
			if prefs.ShowThinking {
				if prefs.ThinkingLevel == "" || prefs.ThinkingLevel == "off" {
					prefs.ThinkingLevel = llm.DefaultThinkingLevel.String()
				}
				resultMsg = fmt.Sprintf("Thinking output enabled (level: %s).", prefs.ThinkingLevel)
			} else {
				resultMsg = "Thinking output disabled."
			}
		case "status":
			if prefs.ShowThinking {
				level := prefs.ThinkingLevel
				if level == "" {
					level = llm.DefaultThinkingLevel.String()
				}
				resultMsg = fmt.Sprintf("Thinking output: ON, level: %s", level)
			} else {
				resultMsg = "Thinking output: OFF"
			}
		default:
			// Check if arg is a valid thinking level
			if llm.IsValidThinkingLevel(arg) {
				prefs.ThinkingLevel = arg
				if arg == "off" {
					prefs.ShowThinking = false
					resultMsg = "Thinking disabled."
				} else {
					prefs.ShowThinking = true // Setting a level automatically enables thinking display
					resultMsg = fmt.Sprintf("Thinking level set to %s (output enabled).", arg)
				}
			} else {
				resultMsg = "Usage: /thinking [on|off|toggle|status|minimal|low|medium|high|xhigh]"
			}
		}

		return c.Send(resultMsg)
	})
}

func (b *Bot) handleACPCallback(c tele.Context) error {
	cb := c.Callback()
	if cb == nil {
		return nil
	}
	parts := strings.Split(cb.Data, "|")
	if len(parts) < 2 {
		return c.Respond(&tele.CallbackResponse{Text: "Invalid interaction payload.", ShowAlert: true})
	}
	stateID := strings.TrimSpace(parts[0])
	action := strings.TrimSpace(parts[1])
	state := b.getInteractiveState(stateID)
	if state == nil {
		return c.Respond(&tele.CallbackResponse{Text: "This interaction is no longer active.", ShowAlert: true})
	}
	switch state.Method {
	case "cursor/ask_question":
		return b.handleACPQuestionCallback(c, state, action, parts[2:])
	case "cursor/create_plan":
		return b.handleACPPlanCallback(c, state, action)
	default:
		return c.Respond(&tele.CallbackResponse{Text: "Unsupported interaction.", ShowAlert: true})
	}
}

func (b *Bot) handleACPQuestionCallback(c tele.Context, state *telegramInteractiveState, action string, args []string) error {
	if state.Selected == nil {
		state.Selected = make(map[string]map[string]bool)
	}
	parseIndexes := func() (int, int, error) {
		if len(args) < 2 {
			return 0, 0, fmt.Errorf("missing callback indexes")
		}
		qi, err := strconv.Atoi(args[0])
		if err != nil {
			return 0, 0, err
		}
		oi, err := strconv.Atoi(args[1])
		if err != nil {
			return 0, 0, err
		}
		return qi, oi, nil
	}
	switch action {
	case "toggle", "single":
		qi, oi, err := parseIndexes()
		if err != nil {
			return c.Respond(&tele.CallbackResponse{Text: "Invalid selection.", ShowAlert: true})
		}
		if qi < 0 || qi >= len(state.Question.Questions) {
			return c.Respond(&tele.CallbackResponse{Text: "Question no longer available.", ShowAlert: true})
		}
		question := state.Question.Questions[qi]
		options := b.telegramQuestionOptions(question)
		if oi < 0 || oi >= len(options) {
			return c.Respond(&tele.CallbackResponse{Text: "Option no longer available.", ShowAlert: true})
		}
		option := options[oi]
		if b.telegramQuestionOptionIsOther(option) {
			state.Selected = make(map[string]map[string]bool)
			state.OtherRequested = true
			_, _ = b.bot.Edit(b.telegramMessageRef(state), b.telegramQuestionText(state, "Continue in chat with your custom answer."), &tele.SendOptions{
				ParseMode:   tele.ModeHTML,
				ReplyMarkup: &tele.ReplyMarkup{},
			})
			return c.Respond(&tele.CallbackResponse{Text: "Continue in chat with your custom answer.", ShowAlert: false})
		}
		state.OtherRequested = false
		if state.Selected[question.ID] == nil {
			state.Selected[question.ID] = make(map[string]bool)
		}
		optionID := option.ID
		if action == "single" || !question.AllowMultiple {
			state.Selected[question.ID] = map[string]bool{optionID: true}
			answers, err := b.telegramQuestionAnswers(state)
			if err != nil {
				return c.Respond(&tele.CallbackResponse{Text: err.Error(), ShowAlert: true})
			}
			if err := b.gateway.ACPRespond(state.SessionKey, acp.ACPDriverExtensionResponse{
				Driver:          state.Driver,
				Method:          state.Method,
				ToolCallID:      state.ToolCallID,
				ResponsePayload: acp.BuildCursorAskQuestionAnsweredResponse(answers),
			}); err != nil {
				return c.Respond(&tele.CallbackResponse{Text: "Failed to submit answer.", ShowAlert: true})
			}
			b.popInteractiveState(state.ID)
			_, _ = b.bot.Edit(b.telegramMessageRef(state), b.telegramQuestionText(state, "Answered."), &tele.SendOptions{
				ParseMode:   tele.ModeHTML,
				ReplyMarkup: &tele.ReplyMarkup{},
			})
			return c.Respond(&tele.CallbackResponse{Text: "Answer submitted."})
		}
		state.Selected[question.ID][optionID] = !state.Selected[question.ID][optionID]
		_, _ = b.bot.Edit(b.telegramMessageRef(state), b.telegramQuestionText(state, ""), &tele.SendOptions{
			ParseMode:   tele.ModeHTML,
			ReplyMarkup: b.telegramQuestionMarkup(state),
		})
		return c.Respond(&tele.CallbackResponse{Text: "Selection updated."})
	case "other":
		state.Selected = make(map[string]map[string]bool)
		state.OtherRequested = true
		_, _ = b.bot.Edit(b.telegramMessageRef(state), b.telegramQuestionText(state, "Continue in chat with your custom answer."), &tele.SendOptions{
			ParseMode:   tele.ModeHTML,
			ReplyMarkup: &tele.ReplyMarkup{},
		})
		return c.Respond(&tele.CallbackResponse{Text: "Continue in chat with your custom answer.", ShowAlert: false})
	case "submit":
		if state.OtherRequested {
			_, _ = b.bot.Edit(b.telegramMessageRef(state), b.telegramQuestionText(state, "Continue in chat with your custom answer."), &tele.SendOptions{
				ParseMode:   tele.ModeHTML,
				ReplyMarkup: &tele.ReplyMarkup{},
			})
			return c.Respond(&tele.CallbackResponse{Text: "Continue in chat with your custom answer.", ShowAlert: false})
		}
		answers, err := b.telegramQuestionAnswers(state)
		if err != nil {
			return c.Respond(&tele.CallbackResponse{Text: err.Error(), ShowAlert: true})
		}
		if err := b.gateway.ACPRespond(state.SessionKey, acp.ACPDriverExtensionResponse{
			Driver:          state.Driver,
			Method:          state.Method,
			ToolCallID:      state.ToolCallID,
			ResponsePayload: acp.BuildCursorAskQuestionAnsweredResponse(answers),
		}); err != nil {
			return c.Respond(&tele.CallbackResponse{Text: "Failed to submit answer.", ShowAlert: true})
		}
		b.popInteractiveState(state.ID)
		_, _ = b.bot.Edit(b.telegramMessageRef(state), b.telegramQuestionText(state, "Answered."), &tele.SendOptions{
			ParseMode:   tele.ModeHTML,
			ReplyMarkup: &tele.ReplyMarkup{},
		})
		return c.Respond(&tele.CallbackResponse{Text: "Answer submitted."})
	default:
		return c.Respond(&tele.CallbackResponse{Text: "Unknown question action.", ShowAlert: true})
	}
}

func (b *Bot) handleACPPlanCallback(c tele.Context, state *telegramInteractiveState, action string) error {
	var payload json.RawMessage
	var resolved string
	switch action {
	case "approve":
		payload = acp.BuildCursorCreatePlanAcceptedResponse("")
		resolved = "Plan approved."
	case "reject":
		payload = acp.BuildCursorCreatePlanRejectedResponse("Rejected from Telegram.")
		resolved = "Plan rejected."
	default:
		return c.Respond(&tele.CallbackResponse{Text: "Unknown plan action.", ShowAlert: true})
	}
	if err := b.gateway.ACPRespond(state.SessionKey, acp.ACPDriverExtensionResponse{
		Driver:          state.Driver,
		Method:          state.Method,
		ToolCallID:      state.ToolCallID,
		ResponsePayload: payload,
	}); err != nil {
		return c.Respond(&tele.CallbackResponse{Text: "Failed to submit decision.", ShowAlert: true})
	}
	b.popInteractiveState(state.ID)
	_, _ = b.bot.Edit(b.telegramMessageRef(state), b.telegramPlanText(state.Plan, resolved), &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: &tele.ReplyMarkup{},
	})
	return c.Respond(&tele.CallbackResponse{Text: resolved})
}

func (b *Bot) handleACPPollAnswer(c tele.Context) error {
	answer := c.PollAnswer()
	if answer == nil {
		return nil
	}
	state := b.popInteractiveStateByPollID(answer.PollID)
	if state == nil {
		return nil
	}
	if len(state.Question.Questions) == 0 {
		return nil
	}
	question := state.Question.Questions[0]
	selected := make([]string, 0, len(answer.Options))
	selectedOther := false
	for _, idx := range answer.Options {
		if idx < 0 || idx >= len(state.PollOptionIDs) {
			continue
		}
		optionID := state.PollOptionIDs[idx]
		if b.telegramQuestionOptionIsOther(acp.QuestionOption{ID: optionID, Label: optionID}) {
			selectedOther = true
			continue
		}
		selected = append(selected, optionID)
	}

	pollRef := &tele.Message{ID: state.PollMessageID, Chat: &tele.Chat{ID: state.ChatID}}
	if state.PollMessageID != 0 && state.ChatID != 0 && (selectedOther || len(selected) > 0) {
		if err := b.bot.Delete(pollRef); err != nil {
			_, _ = b.bot.StopPoll(pollRef)
		}
	}

	if selectedOther {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, _ = b.gateway.ACPHandoffPending(ctx, state.SessionKey)
		cancel()
		_, _ = b.bot.Send(&tele.Chat{ID: state.ChatID}, "Continue in chat with your custom answer.")
		return nil
	}

	if len(selected) == 0 {
		_, _ = b.bot.Send(&tele.Chat{ID: state.ChatID}, "Pick at least one option, then vote again.")
		return nil
	}
	if err := b.gateway.ACPRespond(state.SessionKey, acp.ACPDriverExtensionResponse{
		Driver:     state.Driver,
		Method:     state.Method,
		ToolCallID: state.ToolCallID,
		ResponsePayload: acp.BuildCursorAskQuestionAnsweredResponse([]acp.QuestionAnswer{{
			QuestionID:        question.ID,
			SelectedOptionIDs: selected,
		}}),
	}); err != nil {
		logging.L_warn("telegram: failed to submit poll answer", "toolCallID", state.ToolCallID, "error", err)
		_, _ = b.bot.Send(&tele.Chat{ID: state.ChatID}, "Failed to submit answer. Please try again.")
		return nil
	}
	return nil
}

// getSessionKey returns the session key for the current user
func (b *Bot) getSessionKey(c tele.Context) (string, error) {
	userID := fmt.Sprintf("%d", c.Sender().ID)
	u := b.users.FromIdentity("telegram", userID)
	if u == nil {
		return "", fmt.Errorf("You're not authorized to use this bot.")
	}

	// Owner uses primary session (inherited from OpenClaw), others use user-specific
	if u.Role == "owner" {
		return session.PrimarySession, nil
	}
	return fmt.Sprintf("user:%s", u.ID), nil
}

// canUserUseCommands checks if the user has permission to use slash commands
func (b *Bot) canUserUseCommands(u *user.User) bool {
	if u == nil {
		return false
	}
	resolvedRole, err := b.users.ResolveUserRole(u)
	if err != nil {
		logging.L_warn("telegram: failed to resolve role for command permission check", "user", u.Name, "error", err)
		return false
	}
	return resolvedRole.CanUseCommands()
}

// handleCommand routes commands to the global command manager
func (b *Bot) handleCommand(c tele.Context, u *user.User) error {
	text := c.Text()

	// Check command permission
	if !b.canUserUseCommands(u) {
		logging.L_debug("telegram: commands disabled for user", "user", u.Name, "command", text)
		return nil // Silently ignore
	}

	sessionKey, err := b.getSessionKey(c)
	if err != nil {
		return c.Send(err.Error())
	}

	mgr := commands.GetManager()

	// Special handling for long-running commands
	cmdName := strings.Fields(text)[0]
	if cmdName == "/compact" {
		msg, _ := c.Bot().Send(c.Chat(), "Compacting session... (this may take a minute)")

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		result := mgr.Execute(ctx, text, sessionKey, u.ID)

		if msg != nil {
			c.Bot().Edit(msg, FormatMessage(result.Markdown), &tele.SendOptions{ParseMode: tele.ModeHTML}) //nolint:errcheck // fire-and-forget telegram edit
		} else {
			c.Send(FormatMessage(result.Markdown), &tele.SendOptions{ParseMode: tele.ModeHTML}) //nolint:errcheck // fire-and-forget telegram send
		}
		return nil
	}

	// Standard command handling
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result := mgr.Execute(ctx, text, sessionKey, u.ID)

	// Telegram has a 4096 char limit, truncate if needed
	msg := FormatMessage(result.Markdown)
	if len(msg) > 4000 {
		msg = msg[:4000] + "..."
	}

	return c.Send(msg, &tele.SendOptions{ParseMode: tele.ModeHTML})
}

// handleMessage handles incoming text messages
func (b *Bot) handleMessage(c tele.Context) error {
	// Get sender info
	sender := c.Sender()
	userID := fmt.Sprintf("%d", sender.ID)
	chatID := c.Chat().ID
	isGroup := c.Chat().Type != tele.ChatPrivate

	logging.L_debug("telegram message received",
		"userID", userID,
		"chatID", chatID,
		"isGroup", isGroup,
		"text", truncate(c.Text(), 50),
	)

	// Skip group messages for MVP
	if isGroup {
		logging.L_debug("ignoring group message")
		return nil
	}

	if tryHandlePairingMessage(b.config.BotToken, sender, c.Text()) {
		return c.Send("Pairing successful. Return to GoClaw setup to continue.")
	}

	// Look up user by Telegram identity
	logging.L_debug("telegram: looking up user", "provider", "telegram", "userID", userID)
	u := b.users.FromIdentity("telegram", userID)
	if u == nil {
		logging.L_warn("telegram: unknown user ignored", "userID", userID, "senderName", sender.FirstName+" "+sender.LastName)
		// Silently ignore unauthorized users
		return nil
	}

	logging.L_info("telegram: authenticated message", "user", u.Name, "role", u.Role, "userID", userID)

	// Check for shutdown phrase before panic/commands (owner-only).
	if commands.IsShutdownPhrase(c.Text()) {
		if err := b.gateway.RequestShutdown(u.ID); err != nil {
			return c.Send("Shutdown denied.")
		}
		return c.Send("Shutting down now.")
	}

	// Check for panic phrase (emergency stop) before anything else
	// Always attempt cancel and confirm - avoids race conditions where session just finished
	if commands.IsPanicPhrase(c.Text()) {
		b.gateway.StopAllUserSessions(u.ID) //nolint:errcheck // fire-and-forget panic stop
		return c.Send("Stopping all tasks. Send /resume to continue.")
	}

	// Check if this is a command - route to global command manager
	if commands.IsCommand(c.Text()) {
		return b.handleCommand(c, u)
	}

	// Show typing indicator
	_ = c.Notify(tele.Typing)

	// Get chat preferences for thinking level
	prefs := b.getChatPrefs(chatID, u)
	sessionKey, err := b.getSessionKey(c)
	if err != nil {
		return c.Send(err.Error())
	}
	handoffCtx, cancelHandoff := context.WithTimeout(context.Background(), 5*time.Second)
	cancelled, err := b.gateway.ACPHandoffPending(handoffCtx, sessionKey)
	cancelHandoff()
	if err != nil {
		logging.L_warn("telegram: failed to hand off pending ACP interaction; proceeding", "sessionKey", sessionKey, "error", err)
	} else {
		for _, state := range b.popInteractiveStatesForPending(sessionKey, cancelled) {
			b.markTelegramInteractiveStateCancelled(state, "Cancelled because you continued in chat.")
		}
	}

	// Create agent request with media callback
	req := gateway.AgentRequest{
		User:           u,
		Source:         "telegram",
		ChatID:         fmt.Sprintf("%d", chatID),
		IsGroup:        isGroup,
		UserMsg:        c.Text(),
		EnableThinking: prefs.ShowThinking,  // Extended thinking based on chat preference
		ThinkingLevel:  prefs.ThinkingLevel, // Thinking intensity level
		OnMediaToSend: func(path, caption string) error {
			return b.SendPhoto(chatID, path, caption)
		},
	}

	// Run agent with streaming
	events := make(chan gateway.AgentEvent, 100)

	go func() {
		if err := b.gateway.RunAgent(b.ctx, req, events); err != nil {
			logging.L_error("telegram agent error", "error", err)
		}
	}()

	// Process events and stream response
	return b.streamResponse(c, events)
}

// handlePhoto handles incoming photo messages
func (b *Bot) handlePhoto(c tele.Context) error {
	sender := c.Sender()
	userID := fmt.Sprintf("%d", sender.ID)
	chatID := c.Chat().ID
	isGroup := c.Chat().Type != tele.ChatPrivate

	logging.L_debug("telegram photo received",
		"userID", userID,
		"chatID", chatID,
		"isGroup", isGroup,
	)

	// Skip group messages for MVP
	if isGroup {
		logging.L_debug("ignoring group photo")
		return nil
	}

	// Look up user
	u := b.users.FromIdentity("telegram", userID)
	if u == nil {
		logging.L_warn("telegram: unknown user ignored (photo)", "userID", userID)
		return nil
	}

	logging.L_info("telegram: authenticated photo", "user", u.Name, "role", u.Role)

	// Show typing indicator
	_ = c.Notify(tele.Typing)

	// Get the photo (telebot gives us the largest size)
	photo := c.Message().Photo
	if photo == nil {
		logging.L_warn("telegram: photo message but no photo found")
		return nil
	}

	// Download and optimize the image
	logging.L_debug("telegram: downloading photo", "fileID", photo.FileID, "width", photo.Width, "height", photo.Height)
	imageData, err := media.DownloadAndOptimize(b.ctx, b.bot, photo)
	if err != nil {
		logging.L_error("telegram: failed to download/optimize photo", "error", err)
		return c.Send("Sorry, I couldn't process that image.")
	}

	logging.L_debug("telegram: photo optimized",
		"originalSize", photo.FileSize,
		"optimizedSize", len(imageData.Data),
		"dimensions", fmt.Sprintf("%dx%d", imageData.Width, imageData.Height),
	)

	// Save to media store for permanent reference
	var savedPath string
	if store := b.gateway.MediaStore(); store != nil {
		uploadCtx := media.UploadContext{
			Channel:       "telegram",
			User:          u,
			ChannelUserID: userID,
			ChatID:        fmt.Sprintf("%d", chatID),
			MediaType:     "image",
			Caption:       c.Message().Caption,
		}
		absPath, _, err := store.SaveUpload(imageData.Data, ".jpg", uploadCtx)
		if err != nil {
			logging.L_warn("telegram: failed to save uploaded photo", "error", err)
		} else {
			savedPath = absPath
			logging.L_debug("telegram: photo saved to media store", "path", absPath)
		}
	}

	// Create image content block
	imageBlock := types.ContentBlock{
		Type:     "image",
		Data:     imageData.Base64(),
		MimeType: imageData.MimeType,
		Source:   "telegram",
		FilePath: savedPath,
	}

	// Build content blocks - image first, then path info if saved
	contentBlocks := []types.ContentBlock{imageBlock}
	if savedPath != "" {
		pathBlock := types.ContentBlock{
			Type: "text",
			Text: fmt.Sprintf("[Image saved to: %s]", savedPath),
		}
		contentBlocks = append(contentBlocks, pathBlock)
	}

	// Get caption (if any) as the text message
	caption := c.Message().Caption
	if caption == "" {
		caption = "<media:image>" // Placeholder if no caption
	}

	// Get chat preferences for thinking level
	prefs := b.getChatPrefs(chatID, u)

	// Create agent request with image and media callback
	req := gateway.AgentRequest{
		User:           u,
		Source:         "telegram",
		ChatID:         fmt.Sprintf("%d", chatID),
		IsGroup:        isGroup,
		UserMsg:        caption,
		ContentBlocks:  contentBlocks,
		EnableThinking: prefs.ShowThinking,  // Extended thinking based on chat preference
		ThinkingLevel:  prefs.ThinkingLevel, // Thinking intensity level
		OnMediaToSend: func(path, caption string) error {
			return b.SendPhoto(chatID, path, caption)
		},
	}

	// Run agent with streaming
	events := make(chan gateway.AgentEvent, 100)

	go func() {
		if err := b.gateway.RunAgent(b.ctx, req, events); err != nil {
			logging.L_error("telegram agent error", "error", err)
		}
	}()

	return b.streamResponse(c, events)
}

// handleVoice handles incoming voice messages
func (b *Bot) handleVoice(c tele.Context) error {
	sender := c.Sender()
	userID := fmt.Sprintf("%d", sender.ID)
	chatID := c.Chat().ID
	isGroup := c.Chat().Type != tele.ChatPrivate

	logging.L_debug("telegram voice received",
		"userID", userID,
		"chatID", chatID,
		"isGroup", isGroup,
	)

	// Skip group messages for MVP
	if isGroup {
		logging.L_debug("ignoring group voice")
		return nil
	}

	// Look up user
	u := b.users.FromIdentity("telegram", userID)
	if u == nil {
		logging.L_warn("telegram: unknown user ignored (voice)", "userID", userID)
		return nil
	}

	logging.L_info("telegram: authenticated voice", "user", u.Name, "role", u.Role)

	// Show typing indicator
	_ = c.Notify(tele.Typing)

	// Get the voice message
	voice := c.Message().Voice
	if voice == nil {
		logging.L_warn("telegram: voice message but no voice found")
		return nil
	}

	logging.L_debug("telegram: downloading voice",
		"fileID", voice.FileID,
		"duration", voice.Duration,
		"mimeType", voice.MIME,
		"fileSize", voice.FileSize,
	)

	// Download voice file
	reader, err := b.bot.File(&voice.File)
	if err != nil {
		logging.L_error("telegram: failed to get voice file", "error", err)
		return c.Send("Sorry, I couldn't download that voice message.")
	}
	defer func() { _ = reader.Close() }()

	// Read all voice data
	voiceData, err := io.ReadAll(reader)
	if err != nil {
		logging.L_error("telegram: failed to read voice data", "error", err)
		return c.Send("Sorry, I couldn't process that voice message.")
	}

	logging.L_debug("telegram: voice downloaded", "size", len(voiceData))

	// Save voice file to disk for ephemeral resolution
	// Determine extension from MIME type
	ext := ".ogg"
	mimeType := voice.MIME
	if mimeType == "" {
		mimeType = "audio/ogg"
	}

	// Save to media store
	var absPath, relPath string
	if b.gateway != nil && b.gateway.MediaStore() != nil {
		var err error
		absPath, relPath, err = b.gateway.MediaStore().Save(voiceData, "voice", ext)
		if err != nil {
			logging.L_error("telegram: failed to save voice file", "error", err)
			return c.Send("Sorry, I couldn't save that voice message.")
		}
		logging.L_debug("telegram: voice saved", "path", absPath, "relPath", relPath)
	} else {
		logging.L_warn("telegram: no media store, voice will be stored inline")
	}

	// Create audio content block with file reference
	// Gateway's resolveMediaContent will handle STT transcription
	audioBlock := types.ContentBlock{
		Type:     "audio",
		FilePath: absPath,
		MimeType: mimeType,
		Duration: voice.Duration,
		Source:   "telegram",
	}

	// Get caption (if any) as additional context
	caption := c.Message().Caption
	if caption == "" {
		caption = "[Voice note received]"
	}

	// Get chat preferences for thinking level
	prefs := b.getChatPrefs(chatID, u)

	// Create agent request with audio content block
	// STT transcription happens in gateway's resolveMediaContent
	req := gateway.AgentRequest{
		User:           u,
		Source:         "telegram",
		ChatID:         fmt.Sprintf("%d", chatID),
		IsGroup:        isGroup,
		UserMsg:        caption,
		ContentBlocks:  []types.ContentBlock{audioBlock},
		EnableThinking: prefs.ShowThinking,
		ThinkingLevel:  prefs.ThinkingLevel,
		OnMediaToSend: func(path, caption string) error {
			return b.SendPhoto(chatID, path, caption)
		},
	}

	// Run agent with streaming
	events := make(chan gateway.AgentEvent, 100)

	go func() {
		if err := b.gateway.RunAgent(b.ctx, req, events); err != nil {
			logging.L_error("telegram agent error", "error", err)
		}
	}()

	return b.streamResponse(c, events)
}

// streamResponse handles streaming the response to Telegram
func (b *Bot) streamResponse(c tele.Context, events <-chan gateway.AgentEvent) error {
	var response strings.Builder
	var currentMsg *tele.Message
	var lastUpdate time.Time
	var startTime = time.Now()
	var editCount int
	updateInterval := 500 * time.Millisecond // Don't update too frequently

	// Thinking delta tracking
	var thinkingBuf strings.Builder
	var thinkingMsg *tele.Message
	var lastThinkingUpdate time.Time

	// Tool activity summary (Option A): one editable status message for all tools in this run.
	type toolRow struct {
		Name        string
		Status      string // running | completed | error
		DurationMs  int64
		ArgsLabel   string // "args" | "details"
		ArgsPreview string
		OutLabel    string // "result" | "error"
		OutPreview  string
	}
	var toolsMsg *tele.Message
	var toolRowsByID = map[string]*toolRow{}
	var toolOrder []string
	var lastToolsUpdate time.Time
	var runningTools int
	var todoMsgs = map[string]*tele.Message{}

	// Get thinking mode preference upfront
	userID := fmt.Sprintf("%d", c.Sender().ID)
	u := b.users.FromIdentity("telegram", userID)
	prefs := b.getChatPrefs(c.Chat().ID, u)
	sessionKey, _ := b.getSessionKey(c)
	// When thinking is ON, buffer text until tools are done to preserve timeline order.
	// Once text deltas start flowing (tools are finished), switch to streaming.
	bufferMode := prefs.ShowThinking
	toolsActive := false // true while a tool is running

	toolKey := func(toolID, toolName string) string {
		id := strings.TrimSpace(toolID)
		if id != "" {
			return id
		}
		return fmt.Sprintf("anon:%s:%d", strings.TrimSpace(toolName), len(toolOrder)+1)
	}
	findRunningKeyByName := func(toolName string) string {
		for _, id := range toolOrder {
			row := toolRowsByID[id]
			if row == nil {
				continue
			}
			if row.Name == toolName && row.Status == "running" {
				return id
			}
		}
		return ""
	}
	statusIcon := func(status string) string {
		switch status {
		case "completed":
			return "✅"
		case "error":
			return "❌"
		default:
			return "⏳"
		}
	}
	compactPreview := func(raw string, max int) string {
		s := strings.TrimSpace(strings.Join(strings.Fields(raw), " "))
		if s == "" {
			return ""
		}
		if max <= 0 {
			max = 120
		}
		if len(s) <= max {
			return s
		}
		if max <= 3 {
			return s[:max]
		}
		return s[:max-3] + "..."
	}
	toolArgsPreview := func(rawInput, kind string) (string, string) {
		raw := strings.TrimSpace(rawInput)
		switch raw {
		case "", "{}", "[]", "null":
			if strings.TrimSpace(kind) == "" {
				return "", ""
			}
			return "details", compactPreview("kind="+kind, 120)
		default:
			return "args", compactPreview(raw, 120)
		}
	}
	renderToolsSummary := func(finalCompact bool) string {
		if len(toolOrder) == 0 {
			return ""
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("🛠 <b>Tool Activity (%d)</b>\n", len(toolOrder)))
		for i, id := range toolOrder {
			row := toolRowsByID[id]
			if row == nil {
				continue
			}
			dur := ""
			if row.DurationMs > 0 {
				dur = fmt.Sprintf(" %dms", row.DurationMs)
			}
			sb.WriteString(fmt.Sprintf("%d) %s <b>%s</b>%s\n",
				i+1,
				statusIcon(row.Status),
				escapeHTML(row.Name),
				dur,
			))
			showDetails := !finalCompact || row.Status == "error"
			if showDetails && row.ArgsPreview != "" {
				label := row.ArgsLabel
				if label == "" {
					label = "args"
				}
				sb.WriteString(fmt.Sprintf("   • %s: <code>%s</code>\n", escapeHTML(label), escapeHTML(row.ArgsPreview)))
			}
			if showDetails && row.OutPreview != "" {
				label := row.OutLabel
				if label == "" {
					label = "result"
				}
				sb.WriteString(fmt.Sprintf("   • %s: <code>%s</code>\n", escapeHTML(label), escapeHTML(row.OutPreview)))
			}
		}
		out := strings.TrimSpace(sb.String())
		if len(out) > 3500 {
			out = out[:3500] + "\n..."
		}
		return out
	}
	flushToolsSummary := func(force bool, finalCompact bool) {
		if !prefs.ShowThinking || len(toolOrder) == 0 {
			return
		}
		if !force && time.Since(lastToolsUpdate) <= updateInterval {
			return
		}
		body := renderToolsSummary(finalCompact)
		if body == "" {
			return
		}
		if toolsMsg == nil {
			msg, err := b.bot.Send(c.Chat(), body, &tele.SendOptions{ParseMode: tele.ModeHTML})
			if err != nil {
				logging.L_trace("telegram: failed to send tool summary", "error", err)
				return
			}
			toolsMsg = msg
		} else {
			if _, err := b.bot.Edit(toolsMsg, body, &tele.SendOptions{ParseMode: tele.ModeHTML}); err != nil {
				logging.L_trace("telegram: failed to edit tool summary", "error", err)
				return
			}
		}
		lastToolsUpdate = time.Now()
	}

	logging.L_debug("telegram: starting response stream", "chatID", c.Chat().ID, "bufferMode", bufferMode)

	for event := range events {
		switch e := event.(type) {
		case gateway.EventTextDelta:
			response.WriteString(e.Delta)

			// Buffer text while tools are still active (preserves timeline order).
			// Once tools are done and text starts flowing, stream normally.
			if bufferMode && toolsActive {
				continue
			}
			bufferMode = false

			// Normal streaming mode (thinking OFF)
			// Update message periodically to show streaming
			// During streaming, send plain text (HTML formatting only on final)
			if time.Since(lastUpdate) > updateInterval {
				if currentMsg == nil {
					// Send initial message (plain text during streaming)
					msg, err := b.bot.Send(c.Chat(), response.String())
					if err != nil {
						logging.L_error("telegram: failed to send initial message", "error", err)
						continue
					}
					currentMsg = msg
					logging.L_debug("telegram: sent initial message", "msgID", msg.ID, "length", response.Len())
				} else {
					// Edit existing message (plain text during streaming)
					_, err := b.bot.Edit(currentMsg, response.String())
					if err != nil {
						// Edit can fail if content unchanged or rate limited
						logging.L_trace("telegram: edit failed", "error", err)
					} else {
						editCount++
					}
				}
				lastUpdate = time.Now()

				// Keep showing typing indicator
				_ = c.Notify(tele.Typing)
			}

		case gateway.EventToolStart:
			runningTools++
			toolsActive = runningTools > 0
			logging.L_debug("telegram: tool started", "tool", e.ToolName)
			_ = c.Notify(tele.Typing)

			// Flush thinking buffer before showing tool (ensures thinking is complete)
			if prefs.ShowThinking && thinkingBuf.Len() > 0 && thinkingMsg != nil {
				thinkingText := fmt.Sprintf("💭 <i>%s</i>", html.EscapeString(thinkingBuf.String()))
				_, err := b.bot.Edit(thinkingMsg, thinkingText, &tele.SendOptions{ParseMode: tele.ModeHTML})
				if err != nil {
					logging.L_trace("telegram: thinking flush on tool start failed", "error", err)
				}
			}

			// Update structured tool summary if thinking mode is on.
			if prefs.ShowThinking {
				id := toolKey(e.ToolID, e.ToolName)
				inputLabel, inputStr := toolArgsPreview(string(e.Input), e.Kind)
				if _, ok := toolRowsByID[id]; !ok {
					toolRowsByID[id] = &toolRow{
						Name:        e.ToolName,
						Status:      "running",
						ArgsLabel:   inputLabel,
						ArgsPreview: inputStr,
					}
					toolOrder = append(toolOrder, id)
				} else {
					toolRowsByID[id].Status = "running"
					if inputStr != "" {
						toolRowsByID[id].ArgsLabel = inputLabel
						toolRowsByID[id].ArgsPreview = inputStr
					}
				}
				flushToolsSummary(true, false)
			}

		case gateway.EventToolEnd:
			if runningTools > 0 {
				runningTools--
			}
			toolsActive = runningTools > 0
			logging.L_debug("telegram: tool ended", "tool", e.ToolName, "hasError", e.Error != "")

			if prefs.ShowThinking {
				id := strings.TrimSpace(e.ToolID)
				if id == "" {
					id = findRunningKeyByName(e.ToolName)
				}
				if id == "" {
					id = toolKey("", e.ToolName)
				}
				row, ok := toolRowsByID[id]
				if !ok || row == nil {
					row = &toolRow{Name: e.ToolName}
					toolRowsByID[id] = row
					toolOrder = append(toolOrder, id)
				}
				if row.Name == "" {
					row.Name = e.ToolName
				}
				if label, args := toolArgsPreview(string(e.Input), e.Kind); args != "" && row.ArgsPreview == "" {
					row.ArgsLabel = label
					row.ArgsPreview = args
				}
				if e.Error != "" {
					row.Status = "error"
					row.OutLabel = "error"
					row.OutPreview = compactPreview(e.Error, 140)
				} else {
					row.Status = "completed"
					row.OutLabel = "result"
					row.OutPreview = compactPreview(e.DisplayResult, 140)
					if row.OutPreview == "" {
						row.OutPreview = compactPreview(e.Result, 140)
					}
				}
				row.DurationMs = e.DurationMs
				flushToolsSummary(true, false)
			}

		case gateway.EventToolProgress:
			logging.L_debug("telegram: tool progress", "tool", e.ToolName, "status", e.Status)

			if prefs.ShowThinking {
				id := strings.TrimSpace(e.ToolID)
				if id == "" {
					id = findRunningKeyByName(e.ToolName)
				}
				if id == "" {
					id = toolKey("", e.ToolName)
				}
				row, ok := toolRowsByID[id]
				if !ok || row == nil {
					row = &toolRow{Name: e.ToolName}
					toolRowsByID[id] = row
					toolOrder = append(toolOrder, id)
				}
				if row.Name == "" {
					row.Name = e.ToolName
				}
				row.Status = "running"
				if label, args := toolArgsPreview(string(e.Input), e.Kind); args != "" && row.ArgsPreview == "" {
					row.ArgsLabel = label
					row.ArgsPreview = args
				}
				preview := compactPreview(e.DisplayResult, 140)
				if preview == "" {
					preview = compactPreview(e.Result, 140)
				}
				if preview != "" {
					row.OutLabel = "update"
					row.OutPreview = preview
				}
				flushToolsSummary(true, false)
			}

		case gateway.EventThinkingDelta:
			// Accumulate thinking deltas if thinking mode is on
			if prefs.ShowThinking {
				thinkingBuf.WriteString(e.Delta)

				// Update thinking message periodically
				if time.Since(lastThinkingUpdate) > updateInterval {
					thinkingText := fmt.Sprintf("💭 <i>%s</i>", html.EscapeString(thinkingBuf.String()))
					if thinkingMsg == nil {
						msg, err := b.bot.Send(c.Chat(), thinkingText, &tele.SendOptions{ParseMode: tele.ModeHTML})
						if err != nil {
							logging.L_error("telegram: failed to send thinking message", "error", err)
						} else {
							thinkingMsg = msg
						}
					} else {
						_, err := b.bot.Edit(thinkingMsg, thinkingText, &tele.SendOptions{ParseMode: tele.ModeHTML})
						if err != nil {
							logging.L_trace("telegram: thinking edit failed", "error", err)
						}
					}
					lastThinkingUpdate = time.Now()
				}
			}

		case gateway.EventThinking:
			logging.L_debug("telegram: thinking", "contentLen", len(e.Content))

			// Final thinking content - always update/send with complete content
			if prefs.ShowThinking && e.Content != "" {
				thinkingText := fmt.Sprintf("💭 <i>%s</i>", html.EscapeString(e.Content))
				if thinkingMsg != nil {
					// Update existing message with complete content
					_, err := b.bot.Edit(thinkingMsg, thinkingText, &tele.SendOptions{ParseMode: tele.ModeHTML})
					if err != nil {
						logging.L_trace("telegram: thinking final edit failed", "error", err)
					}
				} else {
					// No streaming message exists, send new one
					msg, err := b.bot.Send(c.Chat(), thinkingText, &tele.SendOptions{ParseMode: tele.ModeHTML})
					if err != nil {
						logging.L_trace("telegram: thinking final send failed", "error", err)
					} else {
						thinkingMsg = msg
					}
				}
			}

		case gateway.EventACPDriverExtension:
			switch e.Method {
			case "cursor/update_todos":
				var payload acp.TodoUpdatePayload
				if err := json.Unmarshal(e.Payload, &payload); err != nil {
					logging.L_warn("telegram: failed to decode todo extension payload", "error", err)
					continue
				}
				var sb strings.Builder
				sb.WriteString("📝 <b>Checklist</b>\n")
				for _, todo := range payload.Todos {
					prefix := "☐"
					switch todo.Status {
					case "completed":
						prefix = "☑"
					case "in_progress":
						prefix = "◐"
					}
					sb.WriteString(prefix + " " + escapeHTML(todo.Content) + "\n")
				}
				key := strings.TrimSpace(payload.ToolCallID)
				text := strings.TrimSpace(sb.String())
				if key != "" && todoMsgs[key] != nil {
					_, _ = b.bot.Edit(todoMsgs[key], text, &tele.SendOptions{ParseMode: tele.ModeHTML})
				} else {
					msg, err := b.bot.Send(c.Chat(), text, &tele.SendOptions{ParseMode: tele.ModeHTML})
					if err == nil && key != "" {
						todoMsgs[key] = msg
					}
				}
			case "cursor/task":
				var payload acp.TaskPayload
				if err := json.Unmarshal(e.Payload, &payload); err != nil {
					logging.L_warn("telegram: failed to decode task extension payload", "error", err)
					continue
				}
				taskText := fmt.Sprintf("🧩 <b>Task</b>: %s", escapeHTML(blankDefault(payload.Description, "completed")))
				var bits []string
				if payload.Model != "" {
					bits = append(bits, "model="+escapeHTML(payload.Model))
				}
				if payload.DurationMs > 0 {
					bits = append(bits, fmt.Sprintf("duration=%dms", payload.DurationMs))
				}
				if len(bits) > 0 {
					taskText += "\n" + strings.Join(bits, " | ")
				}
				_, _ = b.bot.Send(c.Chat(), taskText, &tele.SendOptions{ParseMode: tele.ModeHTML})
			case "cursor/generate_image":
				msgText := "🖼 <b>Generated image event</b>"
				if strings.TrimSpace(e.Summary) != "" {
					msgText += "\n" + escapeHTML(e.Summary)
				}
				_, _ = b.bot.Send(c.Chat(), msgText, &tele.SendOptions{ParseMode: tele.ModeHTML})
			case "cursor/ask_question":
				var payload acp.QuestionPayload
				if err := json.Unmarshal(e.Payload, &payload); err != nil {
					logging.L_warn("telegram: failed to decode question extension payload", "error", err)
					continue
				}
				state := &telegramInteractiveState{
					ID:         b.nextInteractiveID(),
					SessionKey: sessionKey,
					Driver:     e.Driver,
					Method:     e.Method,
					ToolCallID: e.ToolCallID,
					ChatID:     c.Chat().ID,
					Question:   payload,
					Selected:   make(map[string]map[string]bool),
				}
				if b.shouldUseTelegramPollForQuestion(payload) {
					question := payload.Questions[0]
					options := b.telegramQuestionOptions(question)
					poll := &tele.Poll{
						Type:            tele.PollRegular,
						Question:        truncate(question.Prompt, 300),
						Anonymous:       false,
						MultipleAnswers: true,
					}
					optionIDs := make([]string, 0, len(options))
					for _, option := range options {
						poll.AddOptions(truncate(option.Label, 100))
						optionIDs = append(optionIDs, option.ID)
					}
					pollMsg, err := b.bot.Send(c.Chat(), poll)
					if err != nil {
						logging.L_warn("telegram: failed to send poll interaction", "error", err)
						continue
					}
					state.PollMessageID = pollMsg.ID
					if pollMsg.Poll != nil {
						state.PollID = strings.TrimSpace(pollMsg.Poll.ID)
					}
					state.PollOptionIDs = optionIDs
					state.MessageID = 0
					b.putInteractiveState(state)
					continue
				}
				msg, err := b.bot.Send(c.Chat(), b.telegramQuestionText(state, ""), &tele.SendOptions{
					ParseMode:   tele.ModeHTML,
					ReplyMarkup: b.telegramQuestionMarkup(state),
				})
				if err != nil {
					logging.L_warn("telegram: failed to send question interaction", "error", err)
					continue
				}
				state.MessageID = msg.ID
				b.putInteractiveState(state)
			case "cursor/create_plan":
				var payload acp.PlanRequestPayload
				if err := json.Unmarshal(e.Payload, &payload); err != nil {
					logging.L_warn("telegram: failed to decode plan extension payload", "error", err)
					continue
				}
				state := &telegramInteractiveState{
					ID:         b.nextInteractiveID(),
					SessionKey: sessionKey,
					Driver:     e.Driver,
					Method:     e.Method,
					ToolCallID: e.ToolCallID,
					ChatID:     c.Chat().ID,
					Plan:       payload,
				}
				msg, err := b.bot.Send(c.Chat(), b.telegramPlanText(payload, ""), &tele.SendOptions{
					ParseMode:   tele.ModeHTML,
					ReplyMarkup: b.telegramPlanMarkup(state),
				})
				if err != nil {
					logging.L_warn("telegram: failed to send plan interaction", "error", err)
					continue
				}
				state.MessageID = msg.ID
				b.putInteractiveState(state)
			default:
				fallback := blankDefault(strings.TrimSpace(e.Summary), "ACP extension: "+e.Method)
				_, _ = b.bot.Send(c.Chat(), escapeHTML(fallback), &tele.SendOptions{ParseMode: tele.ModeHTML})
			}

		case gateway.EventAgentEnd:
			// Use enriched finalText from event (has media refs processed)
			// instead of accumulated response.String() which has raw refs
			finalText := e.FinalText
			if finalText == "" {
				finalText = response.String() // fallback to accumulated
			}
			if finalText == "" {
				finalText = "(No response)"
			}

			elapsed := time.Since(startTime)
			logging.L_debug("telegram: agent completed",
				"responseLength", len(finalText),
				"editCount", editCount,
				"elapsed", elapsed.Round(time.Millisecond),
			)
			flushToolsSummary(true, true)

			// Check for inline media references
			if containsMediaRefs(finalText) {
				logging.L_debug("telegram: response contains media refs, sending with media")
				// Delete the streaming message if we have one (we'll send fresh)
				if currentMsg != nil {
					_ = b.bot.Delete(currentMsg)
				}
				// Send text/media segments
				if err := b.sendWithMediaRefs(c.Chat(), finalText); err != nil {
					logging.L_error("telegram: failed to send with media", "error", err)
				}
			} else {
				// No media refs - send as regular text, splitting by formatted HTML length.
				if _, err := b.sendTextWithOptionalEdit(c.Chat(), finalText, currentMsg); err != nil {
					logging.L_error("telegram: failed to send final text", "error", err)
				}
			}

		case gateway.EventAgentError:
			logging.L_error("telegram: agent error", "error", e.Error)
			errMsg := fmt.Sprintf("Error: %s", e.Error)
			if currentMsg == nil {
				_, _ = b.bot.Send(c.Chat(), errMsg)
			} else {
				_, _ = b.bot.Edit(currentMsg, errMsg)
			}
		}
	}

	return nil
}

// Start starts the bot polling (implements ManagedChannel)
func (b *Bot) Start(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.running {
		return nil // Already running
	}

	logging.L_info("telegram: starting polling", "bot", "@"+b.bot.Me.Username)
	go b.bot.Start()

	b.running = true
	b.startedAt = time.Now()
	b.lastError = nil
	registerRuntimePairingBot(b)
	return nil
}

// RegisterOperationalCommands registers runtime commands for this bot instance.
func (b *Bot) RegisterOperationalCommands() {
	bus.RegisterCommand("telegram", "status", b.handleStatusCommand)
}

// handleStatusCommand returns the current bot status
func (b *Bot) handleStatusCommand(cmd bus.Command) bus.CommandResult {
	username := b.bot.Me.Username
	return bus.CommandResult{
		Success: true,
		Message: fmt.Sprintf("Telegram bot @%s is running", username),
		Data: map[string]any{
			"connected": true,
			"username":  username,
			"botID":     b.bot.Me.ID,
		},
	}
}

// Stop stops the bot (implements ManagedChannel)
func (b *Bot) Stop() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.running {
		return nil // Already stopped
	}

	logging.L_info("telegram: stopping bot")
	for _, sub := range b.delegatedSubs {
		bus.UnsubscribeEvent(sub)
	}
	b.delegatedSubs = nil
	unregisterRuntimePairingBot(b)
	b.cancel()
	b.bot.Stop()

	b.running = false
	return nil
}

// Reload applies new configuration (implements ManagedChannel)
func (b *Bot) Reload(cfg any) error {
	newCfg, ok := cfg.(*config.Config)
	if !ok {
		return fmt.Errorf("expected *telegram.Config, got %T", cfg)
	}

	b.mu.Lock()
	wasRunning := b.running
	b.mu.Unlock()

	// Stop if running
	if wasRunning {
		if err := b.Stop(); err != nil {
			return fmt.Errorf("failed to stop for reload: %w", err)
		}
	}

	// Update config
	b.config = newCfg

	// Restart if was running and still enabled
	if wasRunning && newCfg.Enabled && newCfg.BotToken != "" {
		// Need to recreate the underlying bot with new token
		pref := tele.Settings{
			Token:  newCfg.BotToken,
			Poller: &tele.LongPoller{Timeout: 10 * time.Second},
		}
		newBot, err := tele.NewBot(pref)
		if err != nil {
			b.mu.Lock()
			b.lastError = err
			b.mu.Unlock()
			return fmt.Errorf("failed to create bot with new config: %w", err)
		}
		b.bot = newBot
		b.ctx, b.cancel = context.WithCancel(context.Background())
		b.setupHandlers()
		return b.Start(b.ctx)
	}

	return nil
}

// Status returns current channel status (implements ManagedChannel)
func (b *Bot) Status() chtypes.ChannelStatus {
	b.mu.RLock()
	defer b.mu.RUnlock()

	info := ""
	if b.bot != nil && b.bot.Me != nil {
		info = "@" + b.bot.Me.Username
	}

	return chtypes.ChannelStatus{
		Running:   b.running,
		Connected: b.running, // If running, we're connected (bot.Start succeeded)
		Error:     b.lastError,
		StartedAt: b.startedAt,
		Info:      info,
	}
}

// Name returns the channel name (implements gateway.Channel)
func (b *Bot) Name() string {
	return "telegram"
}

// Send sends a message to the default chat (not implemented for Telegram)
func (b *Bot) Send(ctx context.Context, msg string) error {
	// Send to owner's chat
	owner := b.users.Owner()
	if owner == nil || owner.TelegramID == "" {
		return nil
	}
	var chatID int64
	if _, err := fmt.Sscanf(owner.TelegramID, "%d", &chatID); err != nil {
		logging.L_warn("telegram: invalid owner telegram ID", "telegramID", owner.TelegramID, "error", err)
		return nil
	}
	_, err := b.SendText(chatID, msg)
	return err
}

// sendWithHTMLFallback sends a message with HTML formatting, falling back to plain text
func (b *Bot) sendWithHTMLFallback(chat *tele.Chat, text string) (*tele.Message, error) {
	return b.sendTextWithOptionalEdit(chat, text, nil)
}

// containsMediaRefs checks if text contains any media references (delegates to shared media package)
func containsMediaRefs(text string) bool {
	return media.ContainsMediaRefs(text)
}

// splitMediaSegments splits text into text and media segments (delegates to shared media package)
func splitMediaSegments(text string) []media.MediaSegment {
	return media.SplitMediaSegments(text)
}

// sendWithMediaRefs parses and sends text with inline media references
// Supports captions (preceding text < 1024 chars) and albums (consecutive images)
func (b *Bot) sendWithMediaRefs(chat *tele.Chat, text string) error {
	segments := splitMediaSegments(text)

	// Get media root
	var mediaRoot string
	if b.gateway != nil && b.gateway.MediaStore() != nil {
		mediaRoot = b.gateway.MediaStore().BaseDir()
	}

	i := 0
	for i < len(segments) {
		seg := segments[i]

		if !seg.IsMedia {
			// Text segment - check if next segment is media for caption attachment
			if i+1 < len(segments) && segments[i+1].IsMedia && !strings.HasPrefix(segments[i+1].Mime, "error/") {
				// Check if text is short enough for caption
				if len(seg.Text) <= TelegramCaptionLimit {
					// Look ahead for consecutive images (for album)
					imageSegments := b.collectConsecutiveImages(segments, i+1)

					if len(imageSegments) > 1 {
						// Album with caption
						b.sendAlbum(chat, mediaRoot, imageSegments, seg.Text)
						i += 1 + len(imageSegments) // skip text + all images
						continue
					}

					// Single media with caption
					nextSeg := segments[i+1]
					absPath, err := media.ResolveMediaPath(mediaRoot, nextSeg.Path)
					if err != nil {
						// Send text separately, then continue
						_, _ = b.sendWithHTMLFallback(chat, seg.Text)
						i++
						continue
					}

					b.sendMediaByMime(chat.ID, absPath, nextSeg.Mime, seg.Text)
					i += 2 // skip both text and media
					continue
				}
			}

			// No media follows, or text too long - send text separately
			if seg.Text != "" {
				_, _ = b.sendWithHTMLFallback(chat, seg.Text)
			}
			i++
			continue
		}

		// Media segment (not preceded by suitable caption text)
		// Handle error mimes
		if strings.HasPrefix(seg.Mime, "error/") {
			errType := strings.TrimPrefix(seg.Mime, "error/")
			errMsg := fmt.Sprintf("[Media %s: %s]", errType, seg.Path)
			_, _ = b.sendWithHTMLFallback(chat, errMsg)
			i++
			continue
		}

		// Check for consecutive images (album without caption)
		imageSegments := b.collectConsecutiveImages(segments, i)
		if len(imageSegments) > 1 {
			b.sendAlbum(chat, mediaRoot, imageSegments, "")
			i += len(imageSegments)
			continue
		}

		// Single media without caption
		absPath, err := media.ResolveMediaPath(mediaRoot, seg.Path)
		if err != nil {
			logging.L_warn("telegram: failed to resolve media path", "path", seg.Path, "error", err)
			i++
			continue
		}

		b.sendMediaByMime(chat.ID, absPath, seg.Mime, "")
		i++
	}

	return nil
}

// collectConsecutiveImages collects consecutive image segments starting at index
func (b *Bot) collectConsecutiveImages(segments []media.MediaSegment, startIdx int) []media.MediaSegment {
	var images []media.MediaSegment
	for j := startIdx; j < len(segments); j++ {
		seg := segments[j]
		if !seg.IsMedia {
			break
		}
		if strings.HasPrefix(seg.Mime, "error/") {
			break
		}
		if !strings.HasPrefix(seg.Mime, "image/") {
			break
		}
		images = append(images, seg)
	}
	return images
}

// sendAlbum sends multiple images as a Telegram album
func (b *Bot) sendAlbum(chat *tele.Chat, mediaRoot string, segments []media.MediaSegment, caption string) {
	if len(segments) == 0 {
		return
	}

	// Telegram album max is 10 items
	maxItems := 10
	if len(segments) > maxItems {
		segments = segments[:maxItems]
	}

	var album tele.Album
	for i, seg := range segments {
		absPath, err := media.ResolveMediaPath(mediaRoot, seg.Path)
		if err != nil {
			logging.L_warn("telegram: failed to resolve album item path", "path", seg.Path, "error", err)
			continue
		}

		photo := &tele.Photo{File: tele.FromDisk(absPath)}
		// Caption only on first item, formatted as HTML
		if i == 0 && caption != "" {
			photo.Caption = FormatMessage(caption)
		}
		album = append(album, photo)
	}

	if len(album) == 0 {
		return
	}

	_, err := b.bot.SendAlbum(chat, album, &tele.SendOptions{ParseMode: tele.ModeHTML})
	if err != nil {
		logging.L_warn("telegram: failed to send album", "count", len(album), "error", err)
		// Fallback: send individually
		for i, seg := range segments {
			absPath, _ := media.ResolveMediaPath(mediaRoot, seg.Path)
			cap := ""
			if i == 0 {
				cap = caption
			}
			b.sendMediaByMime(chat.ID, absPath, seg.Mime, cap)
		}
	} else {
		logging.L_debug("telegram: sent album", "count", len(album), "hasCaption", caption != "")
	}
}

// sendMediaByMime sends media based on mimetype with optional caption
func (b *Bot) sendMediaByMime(chatID int64, absPath, mime, caption string) {
	switch {
	case strings.HasPrefix(mime, "image/"):
		if err := b.SendPhoto(chatID, absPath, caption); err != nil {
			logging.L_warn("telegram: failed to send photo", "path", absPath, "error", err)
		}
	case strings.HasPrefix(mime, "video/"):
		if err := b.SendVideo(chatID, absPath, caption); err != nil {
			logging.L_warn("telegram: failed to send video", "path", absPath, "error", err)
		}
	case strings.HasPrefix(mime, "audio/"):
		if err := b.SendAudio(chatID, absPath, caption); err != nil {
			logging.L_warn("telegram: failed to send audio", "path", absPath, "error", err)
		}
	default:
		if err := b.SendDocument(chatID, absPath, caption); err != nil {
			logging.L_warn("telegram: failed to send document", "path", absPath, "error", err)
		}
	}
}

// TelegramCaptionLimit is Telegram's maximum caption length
const TelegramCaptionLimit = 1024

// SendPhoto sends a photo to a chat with optional caption.
// If caption exceeds Telegram's limit, sends photo first then follow-up message.
func (b *Bot) SendPhoto(chatID int64, path string, caption string) error {
	chat := &tele.Chat{ID: chatID}
	photo := &tele.Photo{File: tele.FromDisk(path)}

	// Format caption as HTML
	formattedCaption := ""
	if caption != "" {
		formattedCaption = FormatMessage(caption)
	}

	if len(formattedCaption) <= TelegramCaptionLimit {
		// Caption fits - send photo with caption
		photo.Caption = formattedCaption
		_, err := b.bot.Send(chat, photo, &tele.SendOptions{ParseMode: tele.ModeHTML})
		if err != nil {
			// Fallback: try without HTML formatting
			logging.L_debug("telegram: HTML caption failed, trying plain text", "error", err)
			photo.Caption = caption
			_, err = b.bot.Send(chat, photo)
		}
		return err
	}

	// Caption too long - send photo first, then follow-up message
	logging.L_debug("telegram: caption exceeds limit, sending photo then text",
		"captionLen", len(formattedCaption),
		"limit", TelegramCaptionLimit,
	)

	_, err := b.bot.Send(chat, photo)
	if err != nil {
		return fmt.Errorf("failed to send photo: %w", err)
	}

	// Send follow-up text message with full caption
	_, err = b.sendWithHTMLFallback(chat, caption)
	if err != nil {
		logging.L_warn("telegram: failed to send follow-up caption", "error", err)
	}
	return nil
}

// SendPhotoFromBytes sends a photo from bytes data to a chat
func (b *Bot) SendPhotoFromBytes(chatID int64, data []byte, caption string) error {
	chat := &tele.Chat{ID: chatID}
	photo := &tele.Photo{File: tele.FromReader(strings.NewReader(string(data)))}

	formattedCaption := ""
	if caption != "" {
		formattedCaption = FormatMessage(caption)
	}

	if len(formattedCaption) <= TelegramCaptionLimit {
		photo.Caption = formattedCaption
		_, err := b.bot.Send(chat, photo, &tele.SendOptions{ParseMode: tele.ModeHTML})
		if err != nil {
			photo.Caption = caption
			_, err = b.bot.Send(chat, photo)
		}
		return err
	}

	// Caption too long - send photo first, then follow-up
	_, err := b.bot.Send(chat, photo)
	if err != nil {
		return fmt.Errorf("failed to send photo: %w", err)
	}

	_, err = b.sendWithHTMLFallback(chat, caption)
	return err
}

// SendMirror sends a mirrored user message to the owner's Telegram chat
func (b *Bot) SendMirror(ctx context.Context, source, userMsg string) error {
	owner := b.users.Owner()
	if owner == nil || owner.TelegramID == "" {
		return nil
	}

	var chatID int64
	if _, err := fmt.Sscanf(owner.TelegramID, "%d", &chatID); err != nil {
		logging.L_warn("telegram: invalid telegram ID for mirror", "telegramID", owner.TelegramID, "error", err)
		return nil
	}
	chat := &tele.Chat{ID: chatID}

	// Format mirror: just the user message with source label
	truncatedUser := truncate(userMsg, 500)
	escapedUser := escapeHTML(truncatedUser)
	mirror := fmt.Sprintf("📱 <b>%s</b>\n\n<b>You:</b> %s", source, escapedUser)

	_, err := b.bot.Send(chat, mirror, &tele.SendOptions{ParseMode: tele.ModeHTML})
	if err != nil {
		logging.L_debug("telegram: HTML mirror failed, falling back to plain text", "error", err, "source", source)
		plainMirror := fmt.Sprintf("📱 %s\n\nYou: %s", source, truncatedUser)
		_, err = b.bot.Send(chat, plainMirror)
	}
	if err != nil {
		logging.L_error("failed to send telegram mirror", "error", err)
	}
	return err
}

// DeliverAssistantMessage sends assistant output to the user's Telegram chat.
func (b *Bot) DeliverAssistantMessage(ctx context.Context, u *user.User, message string) error {
	if u == nil || u.TelegramID == "" {
		return nil
	}

	var chatID int64
	if _, err := fmt.Sscanf(u.TelegramID, "%d", &chatID); err != nil {
		logging.L_warn("telegram: invalid telegram ID for deliver", "telegramID", u.TelegramID, "error", err)
		return nil
	}
	chat := &tele.Chat{ID: chatID}

	// Handle messages with media refs (e.g., from media_display tool)
	if containsMediaRefs(message) {
		return b.sendWithMediaRefs(chat, message)
	}

	_, err := b.sendTextWithOptionalEdit(chat, message, nil)
	return err
}

// DeliverSystemMessage sends system/status output to the user's Telegram chat.
func (b *Bot) DeliverSystemMessage(ctx context.Context, u *user.User, msg delivery.SystemMessage) error {
	return b.DeliverAssistantMessage(ctx, u, msg.DisplayText())
}

// HasUser returns true if the user has a Telegram identity
func (b *Bot) HasUser(u *user.User) bool {
	return u.HasTelegramAuth()
}

// StreamEvent returns false - Telegram is batch-only, doesn't support real-time streaming.
func (b *Bot) StreamEvent(u *user.User, event gateway.AgentEvent) bool {
	return false // Telegram doesn't stream events
}

// DeliverGhostwrite sends a ghostwritten message with typing simulation.
func (b *Bot) DeliverGhostwrite(ctx context.Context, u *user.User, message string) error {
	if u == nil || u.TelegramID == "" {
		return nil // User doesn't have Telegram
	}

	// Parse telegram ID
	chatID, err := strconv.ParseInt(u.TelegramID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid telegram ID: %s", u.TelegramID)
	}

	chat := &tele.Chat{ID: chatID}

	logging.L_info("telegram: ghostwrite", "user", u.ID, "chatID", chatID, "messageLen", len(message))

	// Send typing indicator
	_ = b.bot.Notify(chat, tele.Typing)

	// Get typing delay from config
	typingDelay := 500 * time.Millisecond // default
	if b.gateway != nil {
		if cfg := b.gateway.Config(); cfg != nil && cfg.Supervision.Ghostwriting.TypingDelayMs > 0 {
			typingDelay = time.Duration(cfg.Supervision.Ghostwriting.TypingDelayMs) * time.Millisecond
		}
	}

	// Wait for typing delay (simulates thinking/typing)
	time.Sleep(typingDelay)

	// Send the message
	_, err = b.SendText(chatID, message)
	if err != nil {
		return fmt.Errorf("failed to send ghostwrite: %w", err)
	}
	logging.L_info("telegram: ghostwrite delivered", "user", u.ID, "messageLen", len(message))
	return nil
}

// SendText sends a text message to a chat, splitting if necessary.
// Returns the last sent message for potential editing/deletion.
func (b *Bot) SendText(chatID int64, text string) (*tele.Message, error) {
	chat := &tele.Chat{ID: chatID}
	return b.sendTextWithOptionalEdit(chat, text, nil)
}

// EditMessage edits an existing message.
func (b *Bot) EditMessage(chatID int64, messageID int, text string) error {
	msg := &tele.Message{
		ID:   messageID,
		Chat: &tele.Chat{ID: chatID},
	}

	formatted := FormatMessage(text)
	_, err := b.bot.Edit(msg, formatted, &tele.SendOptions{ParseMode: tele.ModeHTML})
	if err != nil {
		// Fallback to plain text
		logging.L_debug("telegram: HTML edit failed, falling back to plain text", "error", err)
		_, err = b.bot.Edit(msg, text)
	}

	if err != nil {
		return fmt.Errorf("failed to edit message: %w", err)
	}

	logging.L_debug("telegram: edited message", "chatID", chatID, "msgID", messageID)
	return nil
}

// DeleteMessage deletes a message from a chat.
func (b *Bot) DeleteMessage(chatID int64, messageID int) error {
	msg := &tele.Message{
		ID:   messageID,
		Chat: &tele.Chat{ID: chatID},
	}

	if err := b.bot.Delete(msg); err != nil {
		return fmt.Errorf("failed to delete message: %w", err)
	}

	logging.L_debug("telegram: deleted message", "chatID", chatID, "msgID", messageID)
	return nil
}

// React adds a reaction emoji to a message.
// Note: Reactions require Telegram Bot API 6.0+ and specific bot permissions.
func (b *Bot) React(chatID int64, messageID int, emoji string) error {
	// Telegram reactions use the setMessageReaction API method
	// telebot v4 requires (Recipient, Editable, Reactions)
	chat := &tele.Chat{ID: chatID}
	msg := &tele.Message{
		ID:   messageID,
		Chat: chat,
	}

	reactions := tele.Reactions{
		Reactions: []tele.Reaction{
			{
				Type:  tele.ReactionTypeEmoji,
				Emoji: emoji,
			},
		},
	}

	if err := b.bot.React(chat, msg, reactions); err != nil {
		return fmt.Errorf("failed to add reaction: %w", err)
	}

	logging.L_debug("telegram: added reaction", "chatID", chatID, "msgID", messageID, "emoji", emoji)
	return nil
}

// SendVideo sends a video file to a chat.
func (b *Bot) SendVideo(chatID int64, path string, caption string) error {
	chat := &tele.Chat{ID: chatID}
	video := &tele.Video{File: tele.FromDisk(path)}

	formattedCaption := ""
	if caption != "" {
		formattedCaption = FormatMessage(caption)
	}

	if len(formattedCaption) <= TelegramCaptionLimit {
		video.Caption = formattedCaption
		_, err := b.bot.Send(chat, video, &tele.SendOptions{ParseMode: tele.ModeHTML})
		if err != nil {
			video.Caption = caption
			_, err = b.bot.Send(chat, video)
		}
		return err
	}

	// Caption too long - send video first, then follow-up
	_, err := b.bot.Send(chat, video)
	if err != nil {
		return fmt.Errorf("failed to send video: %w", err)
	}

	_, err = b.sendWithHTMLFallback(chat, caption)
	return err
}

// SendDocument sends a document file to a chat.
func (b *Bot) SendDocument(chatID int64, path string, caption string) error {
	chat := &tele.Chat{ID: chatID}
	doc := &tele.Document{File: tele.FromDisk(path)}

	formattedCaption := ""
	if caption != "" {
		formattedCaption = FormatMessage(caption)
	}

	if len(formattedCaption) <= TelegramCaptionLimit {
		doc.Caption = formattedCaption
		_, err := b.bot.Send(chat, doc, &tele.SendOptions{ParseMode: tele.ModeHTML})
		if err != nil {
			doc.Caption = caption
			_, err = b.bot.Send(chat, doc)
		}
		return err
	}

	// Caption too long - send document first, then follow-up
	_, err := b.bot.Send(chat, doc)
	if err != nil {
		return fmt.Errorf("failed to send document: %w", err)
	}

	_, err = b.sendWithHTMLFallback(chat, caption)
	return err
}

// SendAudio sends an audio file to a chat.
func (b *Bot) SendAudio(chatID int64, path string, caption string) error {
	chat := &tele.Chat{ID: chatID}
	audio := &tele.Audio{File: tele.FromDisk(path)}

	formattedCaption := ""
	if caption != "" {
		formattedCaption = FormatMessage(caption)
	}

	if len(formattedCaption) <= TelegramCaptionLimit {
		audio.Caption = formattedCaption
		_, err := b.bot.Send(chat, audio, &tele.SendOptions{ParseMode: tele.ModeHTML})
		if err != nil {
			audio.Caption = caption
			_, err = b.bot.Send(chat, audio)
		}
		return err
	}

	// Caption too long - send audio first, then follow-up
	_, err := b.bot.Send(chat, audio)
	if err != nil {
		return fmt.Errorf("failed to send audio: %w", err)
	}

	_, err = b.sendWithHTMLFallback(chat, caption)
	return err
}

// truncate truncates a string to maxLen characters
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// escapeHTML escapes HTML special characters for Telegram HTML mode
func escapeHTML(s string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
	)
	return replacer.Replace(s)
}

// maxTelegramMessage is the maximum message length for Telegram (4096 chars).
// We use 4000 to leave room for formatting overhead.
const maxTelegramMessage = 4000
const maxTelegramHTMLRetrySplitDepth = 6

// sendTextWithOptionalEdit sends a potentially long text message to Telegram.
// Chunks are split using formatted HTML length so markdown expansion doesn't overflow Telegram limits.
// If firstMsg is provided, first chunk is edited into that message and remaining chunks are sent as new messages.
func (b *Bot) sendTextWithOptionalEdit(chat *tele.Chat, text string, firstMsg *tele.Message) (*tele.Message, error) {
	chunks := splitMessageByFormattedLength(text, maxTelegramMessage)
	if len(chunks) == 0 {
		return nil, nil
	}

	var lastMsg *tele.Message
	for i, chunk := range chunks {
		msgs, err := b.sendChunkWithRetry(chat, firstMsg, i == 0 && firstMsg != nil, chunk, 0)
		if err != nil {
			return lastMsg, fmt.Errorf("failed to deliver telegram text chunk %d/%d: %w", i+1, len(chunks), err)
		}
		if len(msgs) > 0 {
			lastMsg = msgs[len(msgs)-1]
		}
	}

	return lastMsg, nil
}

// sendChunkWithRetry sends one chunk using HTML mode, with split-and-retry for fixable HTML errors.
// If useEdit is true, the first successful send edits firstMsg; additional split children are sent as new messages.
func (b *Bot) sendChunkWithRetry(chat *tele.Chat, firstMsg *tele.Message, useEdit bool, chunk string, depth int) ([]*tele.Message, error) {
	chunk = strings.TrimSpace(chunk)
	if chunk == "" {
		return nil, nil
	}

	formatted := FormatMessage(chunk)

	var (
		msg *tele.Message
		err error
	)
	if useEdit {
		msg, err = b.bot.Edit(firstMsg, formatted, &tele.SendOptions{ParseMode: tele.ModeHTML})
	} else {
		msg, err = b.bot.Send(chat, formatted, &tele.SendOptions{ParseMode: tele.ModeHTML})
	}
	if err == nil {
		return []*tele.Message{msg}, nil
	}

	if shouldRetrySplitForTelegramHTMLError(err, chunk, depth) {
		left, right, ok := splitChunkForRetry(chunk)
		if ok {
			logging.L_debug("telegram: HTML send/edit failed, splitting and retrying",
				"error", err,
				"depth", depth,
				"leftLen", len(left),
				"rightLen", len(right),
			)

			leftMsgs, leftErr := b.sendChunkWithRetry(chat, firstMsg, useEdit, left, depth+1)
			if leftErr == nil {
				rightMsgs, rightErr := b.sendChunkWithRetry(chat, nil, false, right, depth+1)
				if rightErr == nil {
					return append(leftMsgs, rightMsgs...), nil
				}
			}
		}
	}

	logging.L_debug("telegram: HTML send/edit failed, falling back to plain text",
		"error", err,
		"depth", depth,
		"chunkLen", len(chunk),
	)
	if useEdit {
		msg, err = b.bot.Edit(firstMsg, chunk)
	} else {
		msg, err = b.bot.Send(chat, chunk)
	}
	if err != nil {
		return nil, err
	}
	return []*tele.Message{msg}, nil
}

func shouldRetrySplitForTelegramHTMLError(err error, chunk string, depth int) bool {
	if err == nil || len(chunk) <= 1 || depth >= maxTelegramHTMLRetrySplitDepth {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "message is too long") ||
		strings.Contains(msg, "can't parse entities") ||
		strings.Contains(msg, "can't find end tag") ||
		strings.Contains(msg, "entity") ||
		strings.Contains(msg, "bad request")
}

func splitChunkForRetry(chunk string) (string, string, bool) {
	if len(chunk) <= 1 {
		return "", "", false
	}
	splitAt := findSplitPoint(chunk, len(chunk)/2)
	if splitAt <= 0 || splitAt >= len(chunk) {
		splitAt = len(chunk) / 2
	}

	left := strings.TrimSpace(chunk[:splitAt])
	right := strings.TrimSpace(chunk[splitAt:])
	if left == "" || right == "" {
		return "", "", false
	}
	return left, right, true
}

// splitMessageByFormattedLength ensures every chunk fits Telegram limits after markdown->HTML formatting.
func splitMessageByFormattedLength(text string, maxLen int) []string {
	baseChunks := splitMessage(text, maxLen)
	var out []string

	for _, chunk := range baseChunks {
		out = append(out, splitChunkByFormattedLength(chunk, maxLen)...)
	}
	return out
}

func splitChunkByFormattedLength(chunk string, maxLen int) []string {
	chunk = strings.TrimSpace(chunk)
	if chunk == "" {
		return nil
	}

	formatted := FormatMessage(chunk)
	if len(formatted) <= maxLen {
		return []string{chunk}
	}

	if len(chunk) <= 1 {
		return []string{chunk}
	}

	splitAt := findSplitPoint(chunk, len(chunk)/2)
	if splitAt <= 0 || splitAt >= len(chunk) {
		splitAt = len(chunk) / 2
	}

	left := strings.TrimSpace(chunk[:splitAt])
	right := strings.TrimSpace(chunk[splitAt:])
	if left == "" || right == "" {
		splitAt = len(chunk) / 2
		left = strings.TrimSpace(chunk[:splitAt])
		right = strings.TrimSpace(chunk[splitAt:])
	}
	if left == "" || right == "" {
		return []string{chunk}
	}

	result := splitChunkByFormattedLength(left, maxLen)
	result = append(result, splitChunkByFormattedLength(right, maxLen)...)
	return result
}

// splitMessage splits a long message into chunks that fit within Telegram's limit.
// It tries to split at natural boundaries: paragraphs, then sentences, then words.
func splitMessage(text string, maxLen int) []string {
	if len(text) <= maxLen {
		return []string{text}
	}

	var chunks []string
	remaining := text

	for len(remaining) > 0 {
		if len(remaining) <= maxLen {
			chunks = append(chunks, remaining)
			break
		}

		// Find a good split point within maxLen
		splitAt := findSplitPoint(remaining, maxLen)
		chunks = append(chunks, strings.TrimSpace(remaining[:splitAt]))
		remaining = strings.TrimSpace(remaining[splitAt:])
	}

	return chunks
}

// findSplitPoint finds the best position to split text, preferring natural boundaries.
func findSplitPoint(text string, maxLen int) int {
	if len(text) <= maxLen {
		return len(text)
	}

	searchArea := text[:maxLen]

	// Try to split at paragraph boundary (double newline)
	if idx := strings.LastIndex(searchArea, "\n\n"); idx > maxLen/2 {
		return idx + 2 // Include the newlines
	}

	// Try to split at single newline
	if idx := strings.LastIndex(searchArea, "\n"); idx > maxLen/2 {
		return idx + 1
	}

	// Try to split at sentence boundary (. ! ?)
	for _, sep := range []string{". ", "! ", "? "} {
		if idx := strings.LastIndex(searchArea, sep); idx > maxLen/2 {
			return idx + len(sep)
		}
	}

	// Try to split at word boundary (space)
	if idx := strings.LastIndex(searchArea, " "); idx > maxLen/2 {
		return idx + 1
	}

	// Fallback: hard split at maxLen
	return maxLen
}
