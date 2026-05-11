// Package intent defines the agent-proposed on-chain action and its EIP-712 signing primitives.
package intent

import (
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

// Intent is what an agent proposes to do on-chain.
type Intent struct {
	Nonce    uint64         `json:"nonce"`
	To       common.Address `json:"to"`
	Value    *big.Int       `json:"value"`    // in wei
	Data     []byte         `json:"data"`     // calldata, empty for plain transfer
	Reason   string         `json:"reason"`   // human-readable, shown in approval UI
	Deadline int64          `json:"deadline"` // unix seconds
}

// SignedIntent is an Intent paired with its EIP-712 signature and recovered signer.
type SignedIntent struct {
	Intent    Intent         `json:"intent"`
	Signature []byte         `json:"signature"` // 65 bytes, r||s||v
	Signer    common.Address `json:"signer"`
}

// String renders the Intent for human review (e.g. Telegram approval message).
func (i Intent) String() string {
	value := i.Value
	if value == nil {
		value = big.NewInt(0)
	}
	return fmt.Sprintf(
		"To:       %s\nValue:    %s ETH\nReason:   %s\nDeadline: %s",
		i.To.Hex(),
		formatETH(value),
		i.Reason,
		time.Unix(i.Deadline, 0).UTC().Format(time.RFC3339),
	)
}

// formatETH converts wei to a decimal ETH string with 6 fractional digits.
func formatETH(wei *big.Int) string {
	weiPerEth := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	f := new(big.Float).SetPrec(256).SetInt(wei)
	divisor := new(big.Float).SetPrec(256).SetInt(weiPerEth)
	f.Quo(f, divisor)
	return f.Text('f', 6)
}
