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
	amount := big.NewInt(1_000_000)
	now := time.Now().Unix()
	validAfter := big.NewInt(now - 60)
	validBefore := big.NewInt(now + 300)

	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}

	// 3) Compute the same EIP-712 digest VerifyPaymentAuth computes
	domainSeparator := computeDomainSeparatorForTest(t, usdc)
	structHash := computeTransferWithAuthStructForTest(t,
		common.HexToAddress(payer),
		common.HexToAddress(recipient),
		amount,
		validAfter,
		validBefore,
		nonce,
	)
	msgHash := crypto.Keccak256(
		[]byte{0x19, 0x01},
		domainSeparator,
		structHash,
	)

	// 4) Sign the digest with the payer key
	sig, err := crypto.Sign(msgHash, priv)
	if err != nil {
		t.Fatal(err)
	}
	v := sig[64] + 27
	r := sig[:32]
	s := sig[32:64]

	auth := PaymentAuth{
		From:        payer,
		To:          recipient,
		Value:       amount.String(),
		ValidAfter:  validAfter.String(),
		ValidBefore: validBefore.String(),
		Nonce:       "0x" + common.Bytes2Hex(nonce[:]),
		V:           v,
		R:           "0x" + common.Bytes2Hex(r),
		S:           "0x" + common.Bytes2Hex(s),
	}

	// 5) Verify
	got, err := usdc.VerifyPaymentAuth(payer, auth)
	if err != nil {
		t.Fatalf("VerifyPaymentAuth: %v", err)
	}
	if got != strings.ToLower(payer) {
		t.Fatalf("recovered: got %s, want %s", got, strings.ToLower(payer))
	}
}

func TestVerifyPaymentAuthRejectsBadPayer(t *testing.T) {
	usdc, _ := NewUSDC(DefaultFujiUSDC, DefaultFujiRPC, DefaultFujiChainID, nil)
	auth := PaymentAuth{
		From:        "0x" + randomHex(t, 20),
		To:          "0x" + randomHex(t, 20),
		Value:       "1000000",
		ValidAfter:  "0",
		ValidBefore: "9999999999",
		Nonce:       "0x" + randomHex(t, 32),
		V:           27, R: "0x" + randomHex(t, 32), S: "0x" + randomHex(t, 32),
	}
	if _, err := usdc.VerifyPaymentAuth("0x"+randomHex(t, 20), auth); err == nil {
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
		ValidBefore: "1",
		Nonce:       "0x" + randomHex(t, 32),
		V:           27, R: "0x" + randomHex(t, 32), S: "0x" + randomHex(t, 32),
	}
	if _, err := usdc.VerifyPaymentAuth("0x"+randomHex(t, 20), auth); err == nil {
		t.Fatal("expected error for expired auth")
	}
}

func TestPaymentRequirementsShape(t *testing.T) {
	u, err := NewUSDC(DefaultFujiUSDC, DefaultFujiRPC, DefaultFujiChainID, nil)
	if err != nil {
		t.Fatal(err)
	}
	req, nonce, err := u.BuildRequirements("/v1/topup", "desc", "0xRecipient", 1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	if nonce == [32]byte{} {
		t.Fatal("nonce must be non-zero")
	}
	if req.Scheme != "exact" {
		t.Errorf("scheme: got %q, want exact", req.Scheme)
	}
	if !strings.HasPrefix(req.Network, "eip155:") {
		t.Errorf("network: got %q, want eip155:*", req.Network)
	}
	if req.MaxAmountRequired != "1000000" {
		t.Errorf("amount: got %q, want 1000000", req.MaxAmountRequired)
	}
}

// --- helpers (must match the construction inside usdc.go) ---

func computeDomainSeparatorForTest(t *testing.T, u *USDC) []byte {
	t.Helper()
	domainType := []byte("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)")
	domainTypeHash := crypto.Keccak256(domainType)
	nameHash := crypto.Keccak256([]byte(USDCDomainName))
	versionHash := crypto.Keccak256([]byte(USDCDomainVersion))
	chainIDBytes := leftPad32(u.ChainID.Bytes())
	addrBytes := leftPad32(u.Address.Bytes())
	return crypto.Keccak256(
		domainTypeHash, nameHash, versionHash, chainIDBytes, addrBytes,
	)
}

func computeTransferWithAuthStructForTest(
	t *testing.T,
	from, to common.Address,
	value, validAfter, validBefore *big.Int,
	nonce [32]byte,
) []byte {
	t.Helper()
	msgType := []byte("TransferWithAuthorization(address from,address to,uint256 value,uint256 validAfter,uint256 validBefore,bytes32 nonce)")
	typeHash := crypto.Keccak256(msgType)
	fromBytes := leftPad32(from.Bytes())
	toBytes := leftPad32(to.Bytes())
	valueBytes := leftPad32(value.Bytes())
	validAfterBytes := leftPad32(validAfter.Bytes())
	validBeforeBytes := leftPad32(validBefore.Bytes())
	nonceBytes := append([]byte{}, nonce[:]...)
	return crypto.Keccak256(
		typeHash,
		fromBytes, toBytes, valueBytes,
		validAfterBytes, validBeforeBytes, nonceBytes,
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
