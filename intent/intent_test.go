package intent

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

func sampleIntent() Intent {
	return Intent{
		Nonce:    7,
		To:       common.HexToAddress("0x1111111111111111111111111111111111111111"),
		Value:    big.NewInt(1_500_000_000_000_000_000), // 1.5 ETH
		Data:     []byte{0xde, 0xad, 0xbe, 0xef},
		Reason:   "swap USDC for ETH",
		Deadline: 1_750_000_000,
	}
}

func sampleDomain() Domain {
	return NewDomain(10143, common.HexToAddress("0x2222222222222222222222222222222222222222"))
}

func TestHash_Deterministic(t *testing.T) {
	d := sampleDomain()
	cases := []struct {
		name string
		i    Intent
	}{
		{"populated", sampleIntent()},
		{"zero-value", Intent{}},
		{"nil-data-nil-value", Intent{Nonce: 1, To: common.HexToAddress("0x3"), Reason: "x", Deadline: 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h1, err := d.Hash(tc.i)
			if err != nil {
				t.Fatalf("hash1: %v", err)
			}
			h2, err := d.Hash(tc.i)
			if err != nil {
				t.Fatalf("hash2: %v", err)
			}
			if h1 != h2 {
				t.Fatalf("hash not deterministic: %x vs %x", h1, h2)
			}
		})
	}
}

func TestHash_FieldSensitivity(t *testing.T) {
	d := sampleDomain()
	base := sampleIntent()
	baseHash, err := d.Hash(base)
	if err != nil {
		t.Fatalf("base hash: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*Intent)
	}{
		{"nonce", func(i *Intent) { i.Nonce = base.Nonce + 1 }},
		{"to", func(i *Intent) { i.To = common.HexToAddress("0x9999999999999999999999999999999999999999") }},
		{"value", func(i *Intent) { i.Value = new(big.Int).Add(base.Value, big.NewInt(1)) }},
		{"data", func(i *Intent) { i.Data = []byte{0x00} }},
		{"reason", func(i *Intent) { i.Reason = base.Reason + "!" }},
		{"deadline", func(i *Intent) { i.Deadline = base.Deadline + 1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := base
			tc.mutate(&m)
			h, err := d.Hash(m)
			if err != nil {
				t.Fatalf("hash: %v", err)
			}
			if h == baseHash {
				t.Fatalf("hash unchanged after mutating %s", tc.name)
			}
		})
	}
}

func TestSignVerify(t *testing.T) {
	d := sampleDomain()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	otherKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("genkey other: %v", err)
	}

	signed, err := d.Sign(sampleIntent(), key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if len(signed.Signature) != 65 {
		t.Fatalf("sig len = %d, want 65", len(signed.Signature))
	}
	if v := signed.Signature[64]; v != 27 && v != 28 {
		t.Fatalf("v = %d, want 27 or 28", v)
	}

	cases := []struct {
		name string
		mod  func(s *SignedIntent)
		want bool
	}{
		{"roundtrip", func(*SignedIntent) {}, true},
		{"tampered-signature", func(s *SignedIntent) { s.Signature[0] ^= 0xFF }, false},
		{"signer-mismatch", func(s *SignedIntent) {
			s.Signer = crypto.PubkeyToAddress(otherKey.PublicKey)
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := SignedIntent{
				Intent:    signed.Intent,
				Signature: append([]byte(nil), signed.Signature...),
				Signer:    signed.Signer,
			}
			tc.mod(&s)
			got, err := d.Verify(s)
			// A tampered signature may either recover to a different address (false, nil)
			// or fail recovery entirely (false, err). Both are acceptable rejections.
			if !tc.want && got {
				t.Fatalf("verify = true, want false (err=%v)", err)
			}
			if tc.want && (err != nil || !got) {
				t.Fatalf("verify = (%v, %v), want (true, nil)", got, err)
			}
		})
	}
}
