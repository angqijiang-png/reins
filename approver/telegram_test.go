package approver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/angqijiang-png/reins/intent"
	"github.com/ethereum/go-ethereum/common"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const testChatID int64 = 9999

// fakeTG is an in-process Telegram Bot API stand-in. It records sendMessage,
// editMessageText, and answerCallbackQuery calls and exposes a channel for
// tests to inject CallbackQuery updates.
type fakeTG struct {
	server *httptest.Server

	msgCounter int64
	updID      int64

	sends   chan fakeMsg
	edits   chan fakeMsg
	answers chan string

	updateQueue chan tgbotapi.Update
}

type fakeMsg struct {
	MessageID int
	Text      string
}

func newFakeTG(t *testing.T) *fakeTG {
	t.Helper()
	f := &fakeTG{
		sends:       make(chan fakeMsg, 16),
		edits:       make(chan fakeMsg, 16),
		answers:     make(chan string, 16),
		updateQueue: make(chan tgbotapi.Update, 16),
	}
	f.server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.server.Close)
	return f
}

// endpoint returns the Telegram API format string pointing at the fake server.
func (f *fakeTG) endpoint() string {
	return f.server.URL + "/bot%s/%s"
}

func (f *fakeTG) handle(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	method := path
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		method = path[i+1:]
	}
	_ = r.ParseForm()

	switch method {
	case "getMe":
		respond(w, tgbotapi.User{ID: 1, IsBot: true, FirstName: "test", UserName: "testbot"})

	case "sendMessage":
		id := int(atomic.AddInt64(&f.msgCounter, 1))
		chatID, _ := strconv.ParseInt(r.FormValue("chat_id"), 10, 64)
		text := r.FormValue("text")
		select {
		case f.sends <- fakeMsg{MessageID: id, Text: text}:
		default:
		}
		respond(w, tgbotapi.Message{
			MessageID: id,
			Chat:      &tgbotapi.Chat{ID: chatID, Type: "private"},
			Date:      int(time.Now().Unix()),
			Text:      text,
		})

	case "editMessageText":
		msgID, _ := strconv.Atoi(r.FormValue("message_id"))
		chatID, _ := strconv.ParseInt(r.FormValue("chat_id"), 10, 64)
		text := r.FormValue("text")
		select {
		case f.edits <- fakeMsg{MessageID: msgID, Text: text}:
		default:
		}
		respond(w, tgbotapi.Message{
			MessageID: msgID,
			Chat:      &tgbotapi.Chat{ID: chatID, Type: "private"},
			Date:      int(time.Now().Unix()),
			Text:      text,
		})

	case "answerCallbackQuery":
		select {
		case f.answers <- r.FormValue("callback_query_id"):
		default:
		}
		respond(w, true)

	case "getUpdates":
		select {
		case u := <-f.updateQueue:
			respond(w, []tgbotapi.Update{u})
		case <-time.After(50 * time.Millisecond):
			respond(w, []tgbotapi.Update{})
		}

	default:
		respond(w, struct{}{})
	}
}

// pushCallback enqueues a CallbackQuery update for the poller to consume.
func (f *fakeTG) pushCallback(action, reqID string, msgID int) {
	uid := int(atomic.AddInt64(&f.updID, 1))
	f.updateQueue <- tgbotapi.Update{
		UpdateID: uid,
		CallbackQuery: &tgbotapi.CallbackQuery{
			ID:   fmt.Sprintf("cb-%d", uid),
			Data: action + ":" + reqID,
			Message: &tgbotapi.Message{
				MessageID: msgID,
				Chat:      &tgbotapi.Chat{ID: testChatID, Type: "private"},
				Text:      "pending",
			},
		},
	}
}

func respond(w http.ResponseWriter, result any) {
	inner, _ := json.Marshal(result)
	_ = json.NewEncoder(w).Encode(tgbotapi.APIResponse{Ok: true, Result: inner})
}

func makeIntent(nonce uint64, reason string) intent.Intent {
	return intent.Intent{
		Nonce:    nonce,
		To:       common.HexToAddress("0x1111111111111111111111111111111111111111"),
		Value:    new(big.Int).Mul(big.NewInt(123), big.NewInt(1_000_000_000_000_000)), // 0.123 ETH
		Data:     nil,
		Reason:   reason,
		Deadline: 1_735_689_600, // 2025-01-01T00:00:00Z
	}
}

type reqResult struct {
	ok  bool
	err error
}

func newApproverForTest(t *testing.T, f *fakeTG) *telegramApprover {
	t.Helper()
	a, err := newTelegram("TEST_TOKEN", testChatID, f.endpoint())
	if err != nil {
		t.Fatalf("newTelegram: %v", err)
	}
	t.Cleanup(a.Close)
	return a
}

func TestRequest_Approve(t *testing.T) {
	f := newFakeTG(t)
	a := newApproverForTest(t, f)

	in := makeIntent(1, "approve-test")
	reqID, err := requestID(in)
	if err != nil {
		t.Fatalf("requestID: %v", err)
	}

	done := make(chan reqResult, 1)
	go func() {
		ok, err := a.Request(context.Background(), intent.SignedIntent{Intent: in})
		done <- reqResult{ok, err}
	}()

	sent := waitSend(t, f)
	if !strings.Contains(sent.Text, "approve-test") {
		t.Errorf("sent text missing reason: %q", sent.Text)
	}
	if !strings.Contains(sent.Text, reqID[:8]) {
		t.Errorf("sent text missing request id prefix")
	}

	f.pushCallback("approve", reqID, sent.MessageID)

	r := waitResult(t, done, 2*time.Second)
	if r.err != nil {
		t.Fatalf("Request err: %v", r.err)
	}
	if !r.ok {
		t.Fatalf("approved=false, want true")
	}

	if got := waitAnswer(t, f); got == "" {
		t.Error("answerCallbackQuery not called")
	}
	select {
	case e := <-f.edits:
		if e.MessageID != sent.MessageID {
			t.Errorf("edited wrong message: got %d want %d", e.MessageID, sent.MessageID)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("editMessageText not called")
	}
}

func TestRequest_Reject(t *testing.T) {
	f := newFakeTG(t)
	a := newApproverForTest(t, f)

	in := makeIntent(2, "reject-test")
	reqID, _ := requestID(in)

	done := make(chan reqResult, 1)
	go func() {
		ok, err := a.Request(context.Background(), intent.SignedIntent{Intent: in})
		done <- reqResult{ok, err}
	}()

	sent := waitSend(t, f)
	f.pushCallback("reject", reqID, sent.MessageID)

	r := waitResult(t, done, 2*time.Second)
	if r.err != nil {
		t.Fatalf("Request err: %v", r.err)
	}
	if r.ok {
		t.Fatalf("approved=true, want false")
	}
}

func TestRequest_Timeout(t *testing.T) {
	f := newFakeTG(t)
	a := newApproverForTest(t, f)

	in := makeIntent(3, "timeout-test")

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	start := time.Now()
	ok, err := a.Request(ctx, intent.SignedIntent{Intent: in})
	elapsed := time.Since(start)

	if ok {
		t.Errorf("approved=true, want false on timeout")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err=%v, want context.DeadlineExceeded", err)
	}
	if elapsed < 200*time.Millisecond {
		t.Errorf("returned too quickly: %v", elapsed)
	}

	// Pending entry must be cleaned up so a later request with same intent works.
	reqID, _ := requestID(in)
	a.mu.Lock()
	_, stillPending := a.pending[reqID]
	a.mu.Unlock()
	if stillPending {
		t.Error("pending entry not cleared after timeout")
	}
}

func TestRequest_Concurrent(t *testing.T) {
	f := newFakeTG(t)
	a := newApproverForTest(t, f)

	in1 := makeIntent(10, "first-intent")
	in2 := makeIntent(11, "second-intent")
	id1, _ := requestID(in1)
	id2, _ := requestID(in2)
	if id1 == id2 {
		t.Fatal("request IDs collided — distinct intents must differ")
	}

	done1 := make(chan reqResult, 1)
	done2 := make(chan reqResult, 1)
	go func() {
		ok, err := a.Request(context.Background(), intent.SignedIntent{Intent: in1})
		done1 <- reqResult{ok, err}
	}()
	go func() {
		ok, err := a.Request(context.Background(), intent.SignedIntent{Intent: in2})
		done2 <- reqResult{ok, err}
	}()

	sentA := waitSend(t, f)
	sentB := waitSend(t, f)
	msgFor1, msgFor2 := sentA, sentB
	if !strings.Contains(sentA.Text, "first-intent") {
		msgFor1, msgFor2 = sentB, sentA
	}

	// Resolve out-of-order to prove no cross-wiring: reject in1, approve in2.
	f.pushCallback("reject", id1, msgFor1.MessageID)
	f.pushCallback("approve", id2, msgFor2.MessageID)

	r1 := waitResult(t, done1, 2*time.Second)
	r2 := waitResult(t, done2, 2*time.Second)

	if r1.err != nil || r2.err != nil {
		t.Fatalf("errs: r1=%v r2=%v", r1.err, r2.err)
	}
	if r1.ok {
		t.Errorf("in1 approved=true, want false (was rejected)")
	}
	if !r2.ok {
		t.Errorf("in2 approved=false, want true (was approved)")
	}
}

func waitSend(t *testing.T, f *fakeTG) fakeMsg {
	t.Helper()
	select {
	case m := <-f.sends:
		return m
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for sendMessage")
	}
	return fakeMsg{}
}

func waitAnswer(t *testing.T, f *fakeTG) string {
	t.Helper()
	select {
	case s := <-f.answers:
		return s
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for answerCallbackQuery")
	}
	return ""
}

func waitResult(t *testing.T, ch <-chan reqResult, d time.Duration) reqResult {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(d):
		t.Fatal("timed out waiting for Request to return")
	}
	return reqResult{}
}
