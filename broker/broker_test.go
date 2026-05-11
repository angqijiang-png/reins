package broker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/angqijiang-png/reins/approver"
	"github.com/angqijiang-png/reins/intent"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

const testChainID uint64 = 10143

// --- JSON-RPC stub ----------------------------------------------------------

type rpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  interface{}     `json:"result,omitempty"`
	Error   interface{}     `json:"error,omitempty"`
}

// rpcStub is a minimal eth-JSON-RPC server. It answers the four methods used
// by Broker.Submit and counts eth_sendRawTransaction calls.
type rpcStub struct {
	server    *httptest.Server
	sendCount atomic.Int32
}

func newRPCStub(t *testing.T, chainID uint64) *rpcStub {
	t.Helper()
	s := &rpcStub{}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// Handle single request (the ethclient does not batch by default).
		var req rpcReq
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var result interface{}
		switch req.Method {
		case "eth_chainId":
			result = fmt.Sprintf("0x%x", chainID)
		case "eth_getTransactionCount":
			result = "0x0"
		case "eth_gasPrice":
			result = "0x3b9aca00" // 1 gwei
		case "eth_sendRawTransaction":
			s.sendCount.Add(1)
			// 32-byte fake hash; Broker returns the locally-computed hash, so
			// this value is not surfaced to callers.
			result = "0x" + strings.Repeat("11", 32)
		default:
			writeJSON(w, rpcResp{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error: map[string]interface{}{
					"code":    -32601,
					"message": "method not handled by stub: " + req.Method,
				},
			})
			return
		}
		writeJSON(w, rpcResp{JSONRPC: "2.0", ID: req.ID, Result: result})
	}))
	t.Cleanup(s.server.Close)
	return s
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// --- mock Approver ----------------------------------------------------------

type approveDecision struct {
	approved bool
	err      error
	// waitCtx blocks the call until ctx is cancelled, then returns ctx.Err().
	waitCtx bool
}

type mockApprover struct {
	mu        sync.Mutex
	decisions []approveDecision
	captured  []intent.SignedIntent
}

// compile-time check that mockApprover satisfies the real Approver interface.
var _ approver.Approver = (*mockApprover)(nil)

func (m *mockApprover) Request(ctx context.Context, s intent.SignedIntent) (bool, error) {
	m.mu.Lock()
	if len(m.decisions) == 0 {
		m.mu.Unlock()
		return false, errors.New("mockApprover: no decisions left")
	}
	d := m.decisions[0]
	m.decisions = m.decisions[1:]
	m.captured = append(m.captured, s)
	m.mu.Unlock()
	if d.waitCtx {
		<-ctx.Done()
		return false, ctx.Err()
	}
	return d.approved, d.err
}

// --- helpers ---------------------------------------------------------------

func newTestBroker(t *testing.T, stub *rpcStub, ap approver.Approver) *Broker {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	b, err := New(Config{
		RPCURL:          stub.server.URL,
		ChainID:         testChainID,
		PolicyVaultAddr: common.HexToAddress("0xabcdabcdabcdabcdabcdabcdabcdabcdabcdabcd"),
		PrivateKey:      key,
		Approver:        ap,
	})
	if err != nil {
		t.Fatalf("broker.New: %v", err)
	}
	t.Cleanup(b.Close)
	return b
}

func sampleIntent(reason string, valueWei int64) intent.Intent {
	return intent.Intent{
		To:     common.HexToAddress("0xdeaddeaddeaddeaddeaddeaddeaddeaddeaddead"),
		Value:  big.NewInt(valueWei),
		Reason: reason,
	}
}

// --- tests -----------------------------------------------------------------

func TestSubmit_Approve_ReturnsTxHash(t *testing.T) {
	stub := newRPCStub(t, testChainID)
	approver := &mockApprover{decisions: []approveDecision{{approved: true}}}
	b := newTestBroker(t, stub, approver)

	hash, err := b.Submit(context.Background(), sampleIntent("approve case", 1_000_000))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if hash == (common.Hash{}) {
		t.Fatal("expected non-zero tx hash")
	}
	if got := stub.sendCount.Load(); got != 1 {
		t.Fatalf("expected 1 eth_sendRawTransaction call, got %d", got)
	}
	if len(approver.captured) != 1 {
		t.Fatalf("expected approver invoked once, got %d", len(approver.captured))
	}
	if approver.captured[0].Intent.Reason != "approve case" {
		t.Fatalf("approver got wrong intent reason: %q", approver.captured[0].Intent.Reason)
	}
	if approver.captured[0].Intent.Deadline == 0 {
		t.Fatal("expected default deadline to be filled in")
	}
}

func TestSubmit_Reject_ReturnsErrRejected(t *testing.T) {
	stub := newRPCStub(t, testChainID)
	approver := &mockApprover{decisions: []approveDecision{{approved: false}}}
	b := newTestBroker(t, stub, approver)

	hash, err := b.Submit(context.Background(), sampleIntent("reject case", 1))
	if !errors.Is(err, ErrRejected) {
		t.Fatalf("expected ErrRejected, got %v", err)
	}
	if hash != (common.Hash{}) {
		t.Fatalf("expected zero hash on rejection, got %s", hash.Hex())
	}
	if got := stub.sendCount.Load(); got != 0 {
		t.Fatalf("expected 0 eth_sendRawTransaction calls on reject, got %d", got)
	}
}

func TestSubmit_CtxCancelDuringApproval(t *testing.T) {
	stub := newRPCStub(t, testChainID)
	approver := &mockApprover{decisions: []approveDecision{{waitCtx: true}}}
	b := newTestBroker(t, stub, approver)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()

	hash, err := b.Submit(ctx, sampleIntent("ctx case", 1))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if hash != (common.Hash{}) {
		t.Fatalf("expected zero hash, got %s", hash.Hex())
	}
	if got := stub.sendCount.Load(); got != 0 {
		t.Fatalf("expected 0 sends after cancel, got %d", got)
	}
}

func TestSubmit_NonceIncrementsAcrossCalls(t *testing.T) {
	stub := newRPCStub(t, testChainID)
	approver := &mockApprover{decisions: []approveDecision{{approved: true}, {approved: true}}}
	b := newTestBroker(t, stub, approver)

	if _, err := b.Submit(context.Background(), sampleIntent("first", 1)); err != nil {
		t.Fatalf("first Submit: %v", err)
	}
	if _, err := b.Submit(context.Background(), sampleIntent("second", 2)); err != nil {
		t.Fatalf("second Submit: %v", err)
	}

	if len(approver.captured) != 2 {
		t.Fatalf("expected 2 captured intents, got %d", len(approver.captured))
	}
	if got := approver.captured[0].Intent.Nonce; got != 0 {
		t.Fatalf("first intent nonce = %d, want 0", got)
	}
	if got := approver.captured[1].Intent.Nonce; got != 1 {
		t.Fatalf("second intent nonce = %d, want 1", got)
	}
}
