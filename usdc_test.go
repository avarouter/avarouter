// usdc_test.go — round-trips a real EIP-3009 signature through
// VerifyPaymentAuth to prove the typed-data construction, hash and
// ecrecover path all work correctly.
package agw

import (
	"crypto/rand"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestVerifyPaymentAuthRoundTrip(t *testing.T) {
	// 1) Generate a payer keypair
	priv, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	payer := crypto.PubkeyToAddress(priv.PublicKey).Hex()
	recipient := "0x" + randomHex(t, 20)

	// 2) Construct the typed-data hash the way USDC expects
	usdc, err := NewUSDC(DefaultFujiUSDC, DefaultFujiRPC, DefaultFujiChainID, nil)
	if err != nil {
		t.Fatal(err)
	}
	amount := big.NewInt(1_000_000) // 1 USDC
	value := amount.String()
	now := time.Now().Unix()
	validAfter := big.NewInt(now - 60).String()
	validBefore := big.NewInt(now + 300).String()

	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}

	// 3) Compute the same EIP-712 digest VerifyPaymentAuth computes
	domainSeparator := computeDomainSeparatorForTest(t, usdc)
	msgHash := computeTransferWithAuthDigestForTest(
		t, domainSeparator,
		common.HexToAddress(payer),
		common.HexToAddress(recipient),
		amount,
		big.NewInt(now-60),
		big.NewInt(now+300),
		nonce,
	)

	// 4) Sign the digest with the payer key
	sig, err := crypto.Sign(msgHash, priv)
	if err != nil {
		t.Fatal(err)
	}
	// go-ethereum's crypto.Sign returns v ∈ {0, 1}; EIP-3009 uses
	// v ∈ {27, 28}. Add 27 to translate.
	v := sig[64] + 27
	r := sig[:32]
	ss := sig[32:64]
	t.Logf("sig[64]=%d, v after +27=%d, msgHash=%x", sig[64], v, msgHash)

	auth := PaymentAuth{
		From:        payer,
		To:          recipient,
		Value:       value,
		ValidAfter:  validAfter,
		ValidBefore: validBefore,
		Nonce:       "0x" + common.Bytes2Hex(nonce[:]),
		V:           v,
		R:           "0x" + common.Bytes2Hex(r),
		S:           "0x" + common.Bytes2Hex(ss),
	}

	// 5) Verify
	got, err := usdc.VerifyPaymentAuth(payer, auth)
	if err != nil {
		t.Fatalf("VerifyPaymentAuth: %v", err)
	}
	// got is lowercase; normalize payer for comparison.
	if got != strings.ToLower(payer) {
		t.Fatalf("recovered: got %s, want %s", got, strings.ToLower(payer))
	}
}

func TestVerifyPaymentAuthRejectsBadPayer(t *testing.T) {
	usdc, _ := NewUSDC(DefaultFujiUSDC, DefaultFujiRPC, DefaultFujiChainID, nil)
	// attacker claims from=0xATTACKER but signs as 0xVICTIM
	victim := "0x" + randomHex(t, 20)
	auth := PaymentAuth{
		From:        "0x" + randomHex(t, 20), // different
		To:          "0x" + randomHex(t, 20),
		Value:       "1000000",
		ValidAfter:  "0",
		ValidBefore: "9999999999",
		Nonce:       "0x" + randomHex(t, 32),
		V:           27, R: "0x" + randomHex(t, 32), S: "0x" + randomHex(t, 32),
	}
	if _, err := usdc.VerifyPaymentAuth(victim, auth); err == nil {
		t.Fatal("expected error for mismatched from")
	}
}

func TestVerifyPaymentAuthRejectsExpired(t *testing.T) {
	usdc, _ := NewUSDC(DefaultFujiUSDC, DefaultFujiRPC, DefaultFujiChainID, nil)
	auth := PaymentAuth{
		From:        "0x" + randomHex(t, 20),
		To:          "0x" + randomHex(t, 20),
		Value:       "1000000",
		ValidAfter:  "0",
		ValidBefore: "1", // expired long ago
		Nonce:       "0x" + randomHex(t, 32),
		V:           27, R: "0x" + randomHex(t, 32), S: "0x" + randomHex(t, 32),
	}
	if _, err := usdc.VerifyPaymentAuth("0x"+randomHex(t, 20), auth); err == nil {
		t.Fatal("expected error for expired auth")
	}
}

// --- helpers (must match the construction inside usdc.go) ---

func computeDomainSeparatorForTest(t *testing.T, u *USDC) []byte {
	t.Helper()
	domainType := []byte("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)")
	domainHash := crypto.Keccak256(domainType)
	nameHash := crypto.Keccak256([]byte(USDCDomainName))
	versionHash := crypto.Keccak256([]byte(USDCDomainVersion))
	chainIDHash := crypto.Keccak256(leftPad32(u.ChainID.Bytes()))
	addrHash := crypto.Keccak256(leftPad32(u.Address.Bytes()))
	return crypto.Keccak256(
		domainHash, nameHash, versionHash, chainIDHash, addrHash,
	)
}

func computeTransferWithAuthDigestForTest(
	t *testing.T, domainSep []byte,
	from, to common.Address,
	value, validAfter, validBefore *big.Int,
	nonce [32]byte,
) []byte {
	t.Helper()
	msgType := []byte("TransferWithAuthorization(address from,address to,uint256 value,uint256 validAfter,uint256 validBefore,bytes32 nonce)")
	typeHash := crypto.Keccak256(msgType)
	fromHash := crypto.Keccak256(leftPad32(from.Bytes()))
	toHash := crypto.Keccak256(leftPad32(to.Bytes()))
	valueHash := crypto.Keccak256(leftPad32(value.Bytes()))
	validAfterHash := crypto.Keccak256(leftPad32(validAfter.Bytes()))
	validBeforeHash := crypto.Keccak256(leftPad32(validBefore.Bytes()))
	nonceHash := crypto.Keccak256(nonce[:])
	structHash := crypto.Keccak256(
		typeHash,
		fromHash, toHash, valueHash,
		validAfterHash, validBeforeHash, nonceHash,
	)
	// EIP-712 final digest = keccak256(0x19 0x01 || domainSep || structHash)
	return crypto.Keccak256(
		[]byte{0x19, 0x01},
		domainSep,
		structHash,
	)
}

func randomHex(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	const digits = "0123456789abcdef"
	out := make([]byte, n*2)
	for i, c := range b {
		out[i*2] = digits[c>>4]
		out[i*2+1] = digits[c&0x0f]
	}
	return string(out)
}
