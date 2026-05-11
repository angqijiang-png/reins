# reins

> Hold the reins of your AI agents on-chain.
> A Go SDK for keeping autonomous agents on a tight leash when they touch money.

[![Go Reference](https://pkg.go.dev/badge/github.com/angqijiang-png/reins.svg)](https://pkg.go.dev/github.com/angqijiang-png/reins)
![Status: v0.1](https://img.shields.io/badge/status-v0.1-orange)

`reins` is the off-chain half of [agent-pay](https://github.com/angqijiang-png/agent-pay) — a policy-gated payment wallet for AI agents on EVM chains. If `agent-pay`'s Solidity contracts are the saddle that lets an agent move money, `reins` is the bridle: it signs an intent (EIP-712 typed data), routes the intent through a human-approval channel (Telegram in v0.1), and submits the approved transaction on-chain.

## Why

LLM-driven agents are being handed money: paying for API credits, settling invoices, executing trades. The default patterns are uncomfortable. Either you give an agent a hot key with a spending limit and hope it doesn't get prompt-injected, or you wrap every action in a bespoke human-in-the-loop UI per provider. The first scales but is unsafe; the second is safe but doesn't scale.

`reins` separates the three concerns cleanly:

1. **Intent** — what the agent wants to do, signed deterministically with EIP-712.
2. **Approval** — a human says yes or no, through whatever channel they actually live in. Telegram now; Slack, Feishu, web, and CLI later.
3. **Execution** — the approved intent is posted to a policy-checking contract on-chain.

Your agent backend imports `reins`. `reins` does the rest.

## Quick start

```go
import (
    "github.com/angqijiang-png/reins/approver"
    "github.com/angqijiang-png/reins/broker"
    "github.com/angqijiang-png/reins/intent"
    "github.com/ethereum/go-ethereum/common"
)

b, err := broker.New(broker.Config{
    RPCURL:          os.Getenv("MONAD_RPC"),
    ChainID:         10143, // Monad testnet
    PolicyVaultAddr: common.HexToAddress(os.Getenv("POLICY_VAULT_ADDR")),
    PrivateKey:      agentKey,
    Approver:        approver.NewTelegram(os.Getenv("TG_BOT_TOKEN"), tgChatID),
})
if err != nil { panic(err) }

txHash, err := b.Submit(ctx, intent.Intent{
    To:     common.HexToAddress("0xRecipient..."),
    Value:  big.NewInt(1e15), // 0.001 ETH
    Reason: "Pay for API credits",
})
```

The full runnable demo lives in [`examples/monad-testnet`](./examples/monad-testnet).

## Live demo

First successful end-to-end run on Monad testnet (2026-05-11):

- **ReinsEcho contract**: [`0xc45527442Ab539Bc9236e1403FDEa48162b99fb1`](https://testnet.monadexplorer.com/address/0xc45527442Ab539Bc9236e1403FDEa48162b99fb1)
- **Approved transaction**: [`0xdec0935688e62ba0d5e246b8655ff30f47e7b1f3bec796f4fb6131c8e32f8b0c`](https://testnet.monadexplorer.com/tx/0xdec0935688e62ba0d5e246b8655ff30f47e7b1f3bec796f4fb6131c8e32f8b0c) (block #31083768, status: Success, 1 `Executed` log)
- **Agent signer**: `0x4512583edCc17358B877FF95690E8a1E475c539d`
- **Approval channel**: Telegram (human-in-the-loop)

`ReinsEcho` is a 30-line minimal echo contract used to validate the SDK pipeline. It accepts `execute(bytes)` and emits the calldata payload — enough surface to prove `intent → approve → execute` works end-to-end without entangling with a real PolicyVault. Production PolicyVault integration lives in [agent-pay](https://github.com/angqijiang-png/agent-pay).

> Note: during the first live run we hit `BUTTON_DATA_INVALID` from Telegram because the SDK was using a 64-char sha256 hex digest as the inline-keyboard callback ID, exceeding Telegram's 64-byte callback_data limit. Fixed in commit [`d309b74`](https://github.com/angqijiang-png/reins/commit/d309b74) by truncating to 16 chars. Tracked as [issue #6](https://github.com/angqijiang-png/reins/issues/6) — a useful reminder that mock servers don't enforce upstream API constraints.

## Status

**v0.1.** Telegram approval, single signer, in-memory nonce, exercised on Monad testnet. Not production. The interface contract has known rough edges tracked under the [`v0.2` label](https://github.com/angqijiang-png/reins/labels/v0.2) — encoding decisions, nil-value semantics, signature field ergonomics — that will be resolved before any real integration.

## Layout

```
intent/      EIP-712 typed-data Intent + Domain.Sign / Verify
approver/    Approver interface + Telegram inline-keyboard implementation
broker/      Sign → wait for approval → submit tx pipeline
examples/    End-to-end demo against Monad testnet
docs/        CONTRACT.md — internal package boundary
```

## Design notes

**Why not Safe / Biconomy?** Smart-wallet SDKs assume the principal is a human clicking a wallet button. With agents the principal is software, and the human becomes the chaperone. `reins` is tiny on purpose — it isn't trying to be a wallet, it's a tight bridle on agent payment actions.

**Why an in-memory nonce in v0.1?** Persistence and recovery are orthogonal concerns. Getting the sign / approve / execute pipeline correct first is the v0.1 bar. Durable nonce sourcing from the on-chain PolicyVault is on the v0.2 list.

**Why Telegram first?** Lowest-friction approval channel: every developer already has a Telegram account; BotFather hands out tokens in under a minute. The `Approver` interface is one method (`Request(ctx, signed) (bool, error)`), so adding Slack / Feishu / web is a single file each.

## License

MIT.
