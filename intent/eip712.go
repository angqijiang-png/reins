package intent

import (
	"crypto/ecdsa"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
)

const (
	domainName    = "reins"
	domainVersion = "1"
	primaryType   = "Intent"
)

// Domain captures the EIP-712 domain parameters for signing and verifying Intents.
type Domain struct {
	chainID           uint64
	verifyingContract common.Address
}

// NewDomain constructs a Domain bound to the given chain and verifying contract.
func NewDomain(chainID uint64, verifyingContract common.Address) Domain {
	return Domain{chainID: chainID, verifyingContract: verifyingContract}
}

// Hash returns the EIP-712 typed-data digest of the Intent.
func (d Domain) Hash(i Intent) ([32]byte, error) {
	td := d.typedData(i)
	domainSep, err := td.HashStruct("EIP712Domain", td.Domain.Map())
	if err != nil {
		return [32]byte{}, fmt.Errorf("hash domain: %w", err)
	}
	msgHash, err := td.HashStruct(primaryType, td.Message)
	if err != nil {
		return [32]byte{}, fmt.Errorf("hash message: %w", err)
	}
	raw := append([]byte{0x19, 0x01}, append(domainSep, msgHash...)...)
	var out [32]byte
	copy(out[:], crypto.Keccak256(raw))
	return out, nil
}

// Sign produces a SignedIntent with a 65-byte r||s||v signature where v ∈ {27, 28}.
func (d Domain) Sign(i Intent, privKey *ecdsa.PrivateKey) (SignedIntent, error) {
	if privKey == nil {
		return SignedIntent{}, fmt.Errorf("nil private key")
	}
	h, err := d.Hash(i)
	if err != nil {
		return SignedIntent{}, err
	}
	sig, err := crypto.Sign(h[:], privKey)
	if err != nil {
		return SignedIntent{}, fmt.Errorf("sign: %w", err)
	}
	if sig[64] < 27 {
		sig[64] += 27
	}
	return SignedIntent{
		Intent:    i,
		Signature: sig,
		Signer:    crypto.PubkeyToAddress(privKey.PublicKey),
	}, nil
}

// Verify recovers the signer from the signature and reports whether it equals s.Signer.
func (d Domain) Verify(s SignedIntent) (bool, error) {
	if len(s.Signature) != 65 {
		return false, fmt.Errorf("signature length: got %d, want 65", len(s.Signature))
	}
	h, err := d.Hash(s.Intent)
	if err != nil {
		return false, err
	}
	sig := make([]byte, 65)
	copy(sig, s.Signature)
	if sig[64] >= 27 {
		sig[64] -= 27
	}
	pub, err := crypto.SigToPub(h[:], sig)
	if err != nil {
		return false, fmt.Errorf("recover: %w", err)
	}
	return crypto.PubkeyToAddress(*pub) == s.Signer, nil
}

// typedData builds the apitypes.TypedData payload describing the given Intent.
func (d Domain) typedData(i Intent) apitypes.TypedData {
	value := i.Value
	if value == nil {
		value = big.NewInt(0)
	}
	data := i.Data
	if data == nil {
		data = []byte{}
	}
	return apitypes.TypedData{
		Types: apitypes.Types{
			"EIP712Domain": []apitypes.Type{
				{Name: "name", Type: "string"},
				{Name: "version", Type: "string"},
				{Name: "chainId", Type: "uint256"},
				{Name: "verifyingContract", Type: "address"},
			},
			primaryType: []apitypes.Type{
				{Name: "nonce", Type: "uint64"},
				{Name: "to", Type: "address"},
				{Name: "value", Type: "uint256"},
				{Name: "data", Type: "bytes"},
				{Name: "reason", Type: "string"},
				{Name: "deadline", Type: "uint256"},
			},
		},
		PrimaryType: primaryType,
		Domain: apitypes.TypedDataDomain{
			Name:              domainName,
			Version:           domainVersion,
			ChainId:           math.NewHexOrDecimal256(int64(d.chainID)),
			VerifyingContract: d.verifyingContract.Hex(),
		},
		Message: apitypes.TypedDataMessage{
			"nonce":    new(big.Int).SetUint64(i.Nonce),
			"to":       i.To.Hex(),
			"value":    value,
			"data":     data,
			"reason":   i.Reason,
			"deadline": big.NewInt(i.Deadline),
		},
	}
}
