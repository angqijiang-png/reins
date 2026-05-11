# reins — Internal Contract

This document defines interfaces that packages depend on across boundaries.
All three packages MUST conform to this. Do not modify without bumping all callers.

## Domain Types

```go
package intent

import (
    "math/big"
    "github.com/ethereum/go-ethereum/common"
)

// Intent is what an agent proposes to do on-chain.
type Intent struct {
    Nonce    uint64         `json:"nonce"`
    To       common.Address `json:"to"`
    Value    *big.Int       `json:"value"`     // in wei
    Data     []byte         `json:"data"`      // calldata, empty for plain transfer
    Reason   string         `json:"reason"`    // human-readable, shown in approval UI
    Deadline int64          `json:"deadline"`  // unix seconds
}

// SignedIntent = Intent + EIP-712 signature.
type SignedIntent struct {
    Intent    Intent `json:"intent"`
    Signature []byte `json:"signature"` // 65 bytes, r||s||v
    Signer    common.Address `json:"signer"`
}
```

## Package: intent/

Owner: Terminal 1

Public API:
- `func NewDomain(chainID uint64, verifyingContract common.Address) Domain`
- `func (d Domain) Hash(i Intent) ([32]byte, error)`     // EIP-712 typed data hash
- `func (d Domain) Sign(i Intent, privKey *ecdsa.PrivateKey) (SignedIntent, error)`
- `func (d Domain) Verify(s SignedIntent) (bool, error)`

Constraints:
- Use go-ethereum's `signer/core/apitypes` for EIP-712.
- TypedData domain name: "reins", version: "1".
- Type string: `Intent(uint64 nonce,address to,uint256 value,bytes data,string reason,uint256 deadline)`.

## Package: approver/

Owner: Terminal 2

Public API:
```go
type Approver interface {
    // Request blocks until the human responds or ctx times out.
    // Returns (approved=true) if human clicked approve, (false, nil) if rejected,
    // (false, err) if timeout/transport error.
    Request(ctx context.Context, s SignedIntent) (approved bool, err error)
}

// Telegram implements Approver via inline keyboard buttons.
func NewTelegram(botToken string, chatID int64) Approver
```

Constraints:
- Use `github.com/go-telegram-bot-api/telegram-bot-api/v5`.
- Render the Intent as a formatted message: To / Value (in ETH) / Reason / Deadline.
- Two inline buttons: ✅ Approve / ❌ Reject.
- Match button callback to the intent by hashing intent (use intent.Domain.Hash).
- Block (don't poll-and-return) until callback arrives or ctx done.

## Package: broker/

Owner: Terminal 3

Public API:
```go
type Broker struct { /* unexported */ }

type Config struct {
    RPCURL            string
    ChainID           uint64
    PolicyVaultAddr   common.Address
    PrivateKey        *ecdsa.PrivateKey  // agent's signing key
    Approver          approver.Approver
}

func New(cfg Config) (*Broker, error)
func (b *Broker) Submit(ctx context.Context, i intent.Intent) (txHash common.Hash, err error)
```

Submit flow:
1. Fill in Nonce (read from PolicyVault contract or local counter — v0.1 uses local counter starting from 0).
2. Sign via intent.Domain.Sign.
3. Call Approver.Request — block until human approves.
4. If approved, build an Ethereum tx that calls PolicyVault.execute(signedIntent) on Monad testnet.
5. Return tx hash. (Don't wait for receipt in v0.1.)

For v0.1, PolicyVault ABI is mocked — Broker just needs to call any function called `execute(bytes)` 
with abi.encode(signedIntent). The real PolicyVault binding can come later.
