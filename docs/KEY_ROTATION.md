# Key Rotation Log

## 2026-05-11 — Initial v0.1 demo rotation

During the v0.1 end-to-end demo (see README "Live demo"), four secrets passed through
multiple channels (chat transcripts, terminal screenshots, public Telegram messages)
that should not see secret material in production: a deployer EOA private key,
an agent signer private key, a Telegram bot token, and the `PRIVATE_KEY` entry
in `~/agent-pay/.env` (same key as the deployer).

All are testnet-only, so there was no monetary exposure. They were rotated
anyway to maintain the discipline that any leaked secret — even worthless ones —
gets rotated immediately rather than left around to teach bad habits.

### Actions taken

1. Generated fresh `deployer` and `agent` keypairs via `cast wallet new`.
2. Swept remaining MON balance from the old deployer and old agent to the new ones.
3. Replaced `PRIVATE_KEY` in `~/agent-pay/.env` with the new deployer key.
4. Replaced `AGENT_PRIVKEY` in `examples/monad-testnet/.env` with the new agent key.
5. Revoked the old Telegram bot token via @BotFather → /revoke and issued a new one.
6. Marked old keys as permanently retired; they are never reused.

### Why this matters

In production these wallets would hold real funds, and the same "leak path"
(terminal screenshot, chat transcript, public message) could drain them within
minutes. The point of rotating on testnet is that the muscle memory is the same
on mainnet — by the time you have value at stake, "rotate immediately on any
exposure" is automatic, not a decision.

### Follow-ups outside this repo

- `agent-pay` GitHub repository is currently disabled (likely due to GitHub
  Secret Scanning detecting the deployer key in commit history). With the key
  now rotated and worthless, the next step is to run `git filter-repo` to
  scrub the historical secret, force-push, and request unblock via GitHub
  Support. Tracked separately from `reins`.
