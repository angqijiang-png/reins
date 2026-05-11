// Package broker drives the lifecycle of an Intent: it assigns a nonce, signs
// the intent via EIP-712, asks the configured Approver for human approval,
// and—on approval—broadcasts an Ethereum transaction calling
// PolicyVault.execute(bytes) on the target chain.
//
// See docs/CONTRACT.md for the cross-package contract.
package broker

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/angqijiang-png/reins/approver"
	"github.com/angqijiang-png/reins/intent"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

// defaultGasLimit is the gas cap used for execute(bytes) calls until a real
// PolicyVault binding lands and supports estimation.
const defaultGasLimit uint64 = 300_000

// defaultDeadlineWindow is the offset added to time.Now when an Intent omits
// Deadline (per CONTRACT.md, ten minutes).
const defaultDeadlineWindow = int64(600)

// ErrRejected is returned by Submit when the human Approver explicitly rejects
// the intent. It is exported so callers can distinguish rejection from
// transport, signing, or chain errors.
var ErrRejected = errors.New("rejected by human")

// Config wires a Broker to its chain, signing key, and approval channel.
type Config struct {
	// RPCURL is the Ethereum JSON-RPC endpoint (e.g. Monad testnet).
	RPCURL string
	// ChainID is the EIP-155 chain id used for tx signing and EIP-712 domain.
	ChainID uint64
	// PolicyVaultAddr is the address of the PolicyVault contract that exposes
	// execute(bytes).
	PolicyVaultAddr common.Address
	// PrivateKey is the agent's signing key. The derived address is the tx
	// sender and the EIP-712 signer.
	PrivateKey *ecdsa.PrivateKey
	// Approver gates broadcasts; Submit blocks on Approver.Request.
	Approver approver.Approver
}

// Broker is the v0.1 Intent → tx pipeline. It is safe for concurrent use; the
// internal nonce counter is mutex-protected.
type Broker struct {
	cfg      Config
	domain   intent.Domain
	client   *ethclient.Client
	fromAddr common.Address

	mu          sync.Mutex
	intentNonce uint64
}

// New constructs a Broker. It dials cfg.RPCURL eagerly so misconfiguration
// surfaces at construction rather than on the first Submit. Required fields:
// RPCURL, ChainID, PrivateKey, Approver.
func New(cfg Config) (*Broker, error) {
	if cfg.RPCURL == "" {
		return nil, errors.New("broker: Config.RPCURL is required")
	}
	if cfg.ChainID == 0 {
		return nil, errors.New("broker: Config.ChainID is required")
	}
	if cfg.PrivateKey == nil {
		return nil, errors.New("broker: Config.PrivateKey is required")
	}
	if cfg.Approver == nil {
		return nil, errors.New("broker: Config.Approver is required")
	}
	client, err := ethclient.Dial(cfg.RPCURL)
	if err != nil {
		return nil, fmt.Errorf("broker: dial %s: %w", cfg.RPCURL, err)
	}
	return &Broker{
		cfg:      cfg,
		domain:   intent.NewDomain(cfg.ChainID, cfg.PolicyVaultAddr),
		client:   client,
		fromAddr: crypto.PubkeyToAddress(cfg.PrivateKey.PublicKey),
	}, nil
}

// Close releases the underlying RPC client. It is safe to call multiple times.
func (b *Broker) Close() {
	if b.client != nil {
		b.client.Close()
	}
}

// Submit runs the full pipeline for one intent. See package doc for the
// sequence. On rejection it returns ErrRejected. ctx cancellation during
// approval propagates as ctx.Err() via the Approver implementation.
func (b *Broker) Submit(ctx context.Context, i intent.Intent) (common.Hash, error) {
	b.mu.Lock()
	i.Nonce = b.intentNonce
	b.intentNonce++
	b.mu.Unlock()

	if i.Deadline == 0 {
		i.Deadline = time.Now().Unix() + defaultDeadlineWindow
	}

	signed, err := b.domain.Sign(i, b.cfg.PrivateKey)
	if err != nil {
		return common.Hash{}, fmt.Errorf("broker: sign intent: %w", err)
	}

	approved, err := b.cfg.Approver.Request(ctx, signed)
	if err != nil {
		return common.Hash{}, err
	}
	if !approved {
		return common.Hash{}, ErrRejected
	}

	onChainID, err := b.client.ChainID(ctx)
	if err != nil {
		return common.Hash{}, fmt.Errorf("broker: fetch chain id: %w", err)
	}
	if onChainID.Uint64() != b.cfg.ChainID {
		return common.Hash{}, fmt.Errorf("broker: chain id mismatch: rpc=%d cfg=%d", onChainID.Uint64(), b.cfg.ChainID)
	}

	calldata, err := buildExecuteCalldata(signed)
	if err != nil {
		return common.Hash{}, fmt.Errorf("broker: encode calldata: %w", err)
	}

	txNonce, err := b.client.PendingNonceAt(ctx, b.fromAddr)
	if err != nil {
		return common.Hash{}, fmt.Errorf("broker: fetch tx nonce: %w", err)
	}
	gasPrice, err := b.client.SuggestGasPrice(ctx)
	if err != nil {
		return common.Hash{}, fmt.Errorf("broker: suggest gas price: %w", err)
	}

	tx := types.NewTransaction(txNonce, b.cfg.PolicyVaultAddr, big.NewInt(0), defaultGasLimit, gasPrice, calldata)
	signedTx, err := types.SignTx(tx, types.NewEIP155Signer(new(big.Int).SetUint64(b.cfg.ChainID)), b.cfg.PrivateKey)
	if err != nil {
		return common.Hash{}, fmt.Errorf("broker: sign tx: %w", err)
	}
	if err := b.client.SendTransaction(ctx, signedTx); err != nil {
		return common.Hash{}, fmt.Errorf("broker: send tx: %w", err)
	}
	return signedTx.Hash(), nil
}

// --- ABI plumbing for execute(bytes) ---------------------------------------

const executeABIJSON = `[{"type":"function","name":"execute","inputs":[{"name":"payload","type":"bytes"}],"outputs":[]}]`

// executeABI is the parsed minimal ABI for PolicyVault.execute(bytes).
var executeABI = func() abi.ABI {
	parsed, err := abi.JSON(strings.NewReader(executeABIJSON))
	if err != nil {
		panic("broker: parse execute ABI: " + err.Error())
	}
	return parsed
}()

// intentTupleABI describes the Intent struct for ABI tuple encoding. Field
// order matches docs/CONTRACT.md.
var intentTupleABI = []abi.ArgumentMarshaling{
	{Name: "nonce", Type: "uint64"},
	{Name: "to", Type: "address"},
	{Name: "value", Type: "uint256"},
	{Name: "data", Type: "bytes"},
	{Name: "reason", Type: "string"},
	{Name: "deadline", Type: "uint256"},
}

// signedIntentTupleType is the abi.Type for SignedIntent. Constructed once at
// package init since abi.NewType allocates and validates each call.
var signedIntentTupleType = func() abi.Type {
	t, err := abi.NewType("tuple", "SignedIntent", []abi.ArgumentMarshaling{
		{Name: "intent", Type: "tuple", InternalType: "Intent", Components: intentTupleABI},
		{Name: "signature", Type: "bytes"},
		{Name: "signer", Type: "address"},
	})
	if err != nil {
		panic("broker: build SignedIntent abi type: " + err.Error())
	}
	return t
}()

// intentABI mirrors Intent with Deadline as *big.Int so it matches the
// uint256 tuple component during reflection-based ABI packing.
type intentABI struct {
	Nonce    uint64
	To       common.Address
	Value    *big.Int
	Data     []byte
	Reason   string
	Deadline *big.Int
}

// signedIntentABI mirrors SignedIntent for the same reason.
type signedIntentABI struct {
	Intent    intentABI
	Signature []byte
	Signer    common.Address
}

// buildExecuteCalldata encodes a SignedIntent as the bytes payload of
// execute(bytes) and returns the full calldata (selector || encoded bytes).
func buildExecuteCalldata(s intent.SignedIntent) ([]byte, error) {
	value := s.Intent.Value
	if value == nil {
		value = big.NewInt(0)
	}
	inner := signedIntentABI{
		Intent: intentABI{
			Nonce:    s.Intent.Nonce,
			To:       s.Intent.To,
			Value:    new(big.Int).Set(value),
			Data:     s.Intent.Data,
			Reason:   s.Intent.Reason,
			Deadline: big.NewInt(s.Intent.Deadline),
		},
		Signature: s.Signature,
		Signer:    s.Signer,
	}
	innerArgs := abi.Arguments{{Type: signedIntentTupleType}}
	payload, err := innerArgs.Pack(inner)
	if err != nil {
		return nil, fmt.Errorf("pack signed intent: %w", err)
	}
	return executeABI.Pack("execute", payload)
}
