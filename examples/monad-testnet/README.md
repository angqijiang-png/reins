# monad-testnet — reins end-to-end demo

Submits one hardcoded `intent.Intent` through the full reins pipeline:

1. Local nonce + EIP-712 sign (`intent.Domain.Sign`)
2. Telegram approval prompt (`approver.NewTelegram`)
3. On approve, broadcast `PolicyVault.execute(bytes)` on Monad testnet
4. Print the tx hash

This is a single-shot script — it submits one intent and exits.

## Prerequisites

- Go 1.22+ (project uses Go 1.26 in `go.mod`).
- An Ethereum-compatible signing key funded with Monad testnet tokens.
- A Telegram bot:
  1. Talk to [@BotFather](https://t.me/BotFather), `/newbot`, save the token.
  2. Send any message to your bot, then grab your chat id from
     `https://api.telegram.org/bot<TOKEN>/getUpdates` or `@userinfobot`.

## Setup

```bash
cp .env.example .env
# Edit .env: fill in MONAD_RPC, POLICY_VAULT_ADDR, AGENT_PRIVKEY, TG_BOT_TOKEN, TG_CHAT_ID
set -a; source .env; set +a
```

## Run

```bash
go run .
```

You should see:

```
Submitting intent; approve in Telegram to proceed...
```

Open Telegram, review the rendered intent, tap **✅ Approve**. The script then
prints:

```
tx hash: 0x...
```

Paste the hash into the Monad testnet explorer to confirm the transaction
landed (template — substitute your explorer URL):

```
https://testnet-explorer.monad.xyz/tx/<HASH>
```

Tap **❌ Reject** instead and the program exits with
`Submit: rejected by human`.

## Notes

- The receipt is not awaited; landing/confirmation is up to you to verify in
  the explorer.
- The intent uses a default deadline of `now + 10m`; older intents will be
  rejected by PolicyVault once it ships.
- Chain id is hardcoded to `10143` in `main.go`; edit there to target a
  different network.
