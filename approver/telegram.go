package approver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/angqijiang-png/reins/intent"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// Default long-poll timeout passed to Telegram's getUpdates.
const updatesLongPollSeconds = 30

// telegramApprover implements Approver over a Telegram bot using inline-keyboard
// callbacks. A single background poller goroutine fans out callback decisions
// to the goroutines blocked inside Request.
type telegramApprover struct {
	bot    *tgbotapi.BotAPI
	chatID int64

	mu      sync.Mutex
	pending map[string]chan bool

	pollerOnce sync.Once
	pollerDone chan struct{}

	closeOnce sync.Once
}

// NewTelegram returns an Approver backed by a Telegram bot. The bot must have
// permission to post in the chat identified by chatID. Construction validates
// the token by calling Telegram's getMe; a failure panics.
func NewTelegram(botToken string, chatID int64) Approver {
	a, err := newTelegram(botToken, chatID, tgbotapi.APIEndpoint)
	if err != nil {
		panic(fmt.Errorf("approver: telegram init failed: %w", err))
	}
	return a
}

// newTelegram is the unexported constructor that allows tests to override the
// Telegram API endpoint.
func newTelegram(token string, chatID int64, endpoint string) (*telegramApprover, error) {
	bot, err := tgbotapi.NewBotAPIWithAPIEndpoint(token, endpoint)
	if err != nil {
		return nil, err
	}
	return &telegramApprover{
		bot:     bot,
		chatID:  chatID,
		pending: make(map[string]chan bool),
	}, nil
}

// Request implements Approver. It posts a formatted approval prompt to the
// configured chat, then blocks until the matching callback arrives or ctx is
// done.
func (t *telegramApprover) Request(ctx context.Context, s intent.SignedIntent) (bool, error) {
	reqID, err := requestID(s.Intent)
	if err != nil {
		return false, fmt.Errorf("compute request id: %w", err)
	}

	decision := make(chan bool, 1)
	t.mu.Lock()
	if _, exists := t.pending[reqID]; exists {
		t.mu.Unlock()
		return false, errors.New("approver: duplicate intent already in flight")
	}
	t.pending[reqID] = decision
	t.mu.Unlock()

	t.pollerOnce.Do(t.startPoller)

	msg := tgbotapi.NewMessage(t.chatID, formatIntent(s.Intent, reqID))
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Approve", "approve:"+reqID),
			tgbotapi.NewInlineKeyboardButtonData("❌ Reject", "reject:"+reqID),
		),
	)
	if _, err := t.bot.Send(msg); err != nil {
		t.unregister(reqID)
		return false, fmt.Errorf("send approval prompt: %w", err)
	}

	select {
	case approved := <-decision:
		return approved, nil
	case <-ctx.Done():
		t.unregister(reqID)
		return false, ctx.Err()
	}
}

// Close stops the background poller. Safe to call multiple times. Optional;
// callers may also let the process exit.
func (t *telegramApprover) Close() {
	t.closeOnce.Do(func() {
		t.bot.StopReceivingUpdates()
		if t.pollerDone != nil {
			<-t.pollerDone
		}
	})
}

func (t *telegramApprover) unregister(reqID string) {
	t.mu.Lock()
	delete(t.pending, reqID)
	t.mu.Unlock()
}

func (t *telegramApprover) startPoller() {
	cfg := tgbotapi.NewUpdate(0)
	cfg.Timeout = updatesLongPollSeconds
	updates := t.bot.GetUpdatesChan(cfg)
	done := make(chan struct{})
	t.pollerDone = done
	go func() {
		defer close(done)
		for update := range updates {
			t.handleUpdate(update)
		}
	}()
}

func (t *telegramApprover) handleUpdate(update tgbotapi.Update) {
	cb := update.CallbackQuery
	if cb == nil {
		return
	}
	action, reqID, ok := parseCallback(cb.Data)
	if !ok {
		return
	}
	approved := action == "approve"

	t.mu.Lock()
	decision, found := t.pending[reqID]
	if found {
		delete(t.pending, reqID)
	}
	t.mu.Unlock()

	label := "❌ Rejected"
	if approved {
		label = "✅ Approved"
	}

	// Telegram requires an answer to every callback query so the spinner stops.
	_, _ = t.bot.Request(tgbotapi.NewCallback(cb.ID, label))

	// Edit the original message so the buttons disappear and the decision is
	// visible in the chat history.
	if cb.Message != nil {
		edit := tgbotapi.NewEditMessageText(
			t.chatID,
			cb.Message.MessageID,
			cb.Message.Text+"\n\n→ "+label,
		)
		_, _ = t.bot.Send(edit)
	}

	if found {
		decision <- approved
	}
}

// parseCallback splits "approve:<reqID>" or "reject:<reqID>". Unknown payloads
// return ok=false and are ignored.
func parseCallback(data string) (action, reqID string, ok bool) {
	i := strings.IndexByte(data, ':')
	if i < 0 {
		return "", "", false
	}
	a, r := data[:i], data[i+1:]
	if a != "approve" && a != "reject" {
		return "", "", false
	}
	if r == "" {
		return "", "", false
	}
	return a, r, true
}

// requestID returns a stable identifier for an Intent derived from sha256 of
// its JSON encoding. Struct field declaration order in intent.Intent makes
// json.Marshal output deterministic.
func requestID(i intent.Intent) (string, error) {
	b, err := json.Marshal(i)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

// formatIntent renders an Intent for the Telegram approval message.
func formatIntent(i intent.Intent, reqID string) string {
	short := reqID
	if len(short) > 8 {
		short = short[:8]
	}
	return fmt.Sprintf(
		"🤖 Agent payment request\n\nTo: %s\nValue: %s ETH\nReason: %s\nDeadline: %s\n\nRequest ID: %s",
		i.To.Hex(),
		formatETH(i.Value),
		i.Reason,
		time.Unix(i.Deadline, 0).UTC().Format(time.RFC3339),
		short,
	)
}

// formatETH renders wei as ETH with 6 fractional digits.
func formatETH(wei *big.Int) string {
	if wei == nil {
		return "0.000000"
	}
	weiPerEth := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	f := new(big.Float).SetPrec(256).SetInt(wei)
	d := new(big.Float).SetPrec(256).SetInt(weiPerEth)
	f.Quo(f, d)
	return f.Text('f', 6)
}
