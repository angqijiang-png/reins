// Command monad-testnet submits one hardcoded test Intent through the reins
// pipeline against the Monad testnet, gated by a Telegram approval prompt.
//
// Required environment variables:
//
//	MONAD_RPC          JSON-RPC URL of the Monad testnet node
//	POLICY_VAULT_ADDR  0x-prefixed address of the PolicyVault contract
//	AGENT_PRIVKEY      hex-encoded ECDSA private key (no 0x prefix)
//	TG_BOT_TOKEN       Telegram bot token from @BotFather
//	TG_CHAT_ID         numeric Telegram chat id (where approval prompt is sent)
//
// See README.md in this directory for setup details.
package main

import (
	"context"
	"fmt"
	"log"
	"math/big"
	"os"
	"strconv"
	"time"

	"github.com/angqijiang-png/reins/approver"
	"github.com/angqijiang-png/reins/broker"
	"github.com/angqijiang-png/reins/intent"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// monadTestnetChainID is the Monad testnet EIP-155 chain id. If you target a
// different network, edit this constant or wire it to an env var.
const monadTestnetChainID uint64 = 10143

// approvalTimeout caps how long we wait for a human reply in Telegram.
const approvalTimeout = 5 * time.Minute

func main() {
	if err := run(); err != nil {
		log.Fatalf("monad-testnet: %v", err)
	}
}

func run() error {
	rpcURL := mustEnv("MONAD_RPC")
	vaultAddrHex := mustEnv("POLICY_VAULT_ADDR")
	privHex := mustEnv("AGENT_PRIVKEY")
	botToken := mustEnv("TG_BOT_TOKEN")
	chatIDStr := mustEnv("TG_CHAT_ID")

	chatID, err := strconv.ParseInt(chatIDStr, 10, 64)
	if err != nil {
		return fmt.Errorf("TG_CHAT_ID must be a numeric chat id: %w", err)
	}

	priv, err := crypto.HexToECDSA(privHex)
	if err != nil {
		return fmt.Errorf("parse AGENT_PRIVKEY: %w", err)
	}

	if !common.IsHexAddress(vaultAddrHex) {
		return fmt.Errorf("POLICY_VAULT_ADDR is not a valid address: %q", vaultAddrHex)
	}
	vaultAddr := common.HexToAddress(vaultAddrHex)

	ap := approver.NewTelegram(botToken, chatID)

	b, err := broker.New(broker.Config{
		RPCURL:          rpcURL,
		ChainID:         monadTestnetChainID,
		PolicyVaultAddr: vaultAddr,
		PrivateKey:      priv,
		Approver:        ap,
	})
	if err != nil {
		return fmt.Errorf("broker.New: %w", err)
	}
	defer b.Close()

	// Hardcoded demo intent: pay 0.001 ETH-equivalent to a fixed receiver.
	testIntent := intent.Intent{
		To:     common.HexToAddress("0x000000000000000000000000000000000000dEaD"),
		Value:  new(big.Int).Mul(big.NewInt(1_000_000_000_000_000), big.NewInt(1)), // 0.001 * 1e18 wei
		Reason: "Pay for API credits",
	}

	ctx, cancel := context.WithTimeout(context.Background(), approvalTimeout)
	defer cancel()

	fmt.Println("Submitting intent; approve in Telegram to proceed...")
	hash, err := b.Submit(ctx, testIntent)
	if err != nil {
		return fmt.Errorf("Submit: %w", err)
	}
	fmt.Printf("tx hash: %s\n", hash.Hex())
	return nil
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("required env var %s is not set; see .env.example", key)
	}
	return v
}
