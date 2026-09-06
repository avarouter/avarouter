// usdc.go — EIP-3009 transferWithAuthorization binding for USDC on
// Avalanche C-Chain (Fuji testnet by default, mainnet supported via
// constructor arguments). Two responsibilities:
//
//  1. Build the 402 PaymentRequired JSON describing the cost in USDC
//     (the client uses this to construct a typed-data signature).
//  2. Verify an off-chain EIP-712 signature against the recovered
//     address (no chain call needed) — the authorization is then
//     settled by submitting transferWithAuthorization to the USDC
//     contract and waiting for the receipt.
//
// We deliberately avoid pulling in the full x402-go SDK to keep the
// dependency surface minimal (AGW already has go-ethereum). The wire
// format we emit is still x402-V2-compatible: a 402 response with an
// `accepts[]` array containing `scheme=exact, network=eip155:<chain>`.
package agw

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

// Default Fuji USDC on Avalanche C-Chain testnet (Chain ID 43113).
// Source: https://developers.circle.com/stablecoins/docs/usdc-on-test-networks
const (
	DefaultFujiUSDC     = "0x5425890298aed601595a70AB815c96711a31Bc65"
	DefaultFujiChainID  = 43113
	DefaultFujiRPC      = "https://api.avax-test.network/ext/bc/C/rpc"
	USDCDomainName      = "USD Coin"
	USDCDomainVersion   = "2"
)

// USDC describes a configured USDC deployment that AGW accepts for topup.
type USDC struct {
	Address     common.Address
	ChainID     *big.Int
	RPCURL      string
	// ServerKey is the operator's hot key that submits transferWithAuthorization
	// transactions (and pays gas in AVAX on Avalanche). Required for on-chain
	// settlement; nil disables on-chain settlement (credits go to internal
	// balance only — useful for demo without funding the server key).
	ServerKey *ecdsa.PrivateKey
	// ReceiptTimeout bounds how long we wait for a tx to be mined.
	ReceiptTimeout time.Duration
	// client is lazily initialized on first Settle call.
	client *ethclient.Client
}

// NewUSDC constructs a USDC config from string inputs.
func NewUSDC(address, rpcURL string, chainID int64, serverKey *ecdsa.PrivateKey) (*USDC, error) {
	if !common.IsHexAddress(address) {
		return nil, fmt.Errorf("usdc: invalid address %q", address)
	}
	if rpcURL == "" {
		return nil, errors.New("usdc: rpc url required")
	}
	if chainID <= 0 {
		return nil, errors.New("usdc: chain id required")
	}
	return &USDC{
		Address:        common.HexToAddress(address),
		ChainID:        big.NewInt(chainID),
		RPCURL:         rpcURL,
		ServerKey:      serverKey,
		ReceiptTimeout: 60 * time.Second,
	}, nil
}

// CAIP2 returns the x402 V2 network identifier (e.g. "eip155:43113").
func (u *USDC) CAIP2() string {
	return fmt.Sprintf("eip155:%s", u.ChainID.String())
}

// PaymentRequirements is the per-network requirement emitted in the 402
// response. This struct is what the client reads to construct the
// EIP-712 typed-data signature.
type PaymentRequirements struct {
	X402Version         int      `json:"x402Version"`
	Scheme              string   `json:"scheme"`            // always "exact"
	Network             string   `json:"network"`           // CAIP-2, e.g. "eip155:43113"
	MaxAmountRequired   string   `json:"maxAmountRequired"` // micro-units, string for big-int safety
	Resource            string   `json:"resource"`          // the URL being paid for
	Description         string   `json:"description"`
	MimeType            string   `json:"mimeType"`          // application/json
	PayTo               string   `json:"payTo"`             // server's payout address
	MaxTimeoutSeconds   int      `json:"maxTimeoutSeconds"`
	Asset               string   `json:"asset"`             // USDC contract address
	Extra               map[string]string `json:"extra,omitempty"`
}

// BuildRequirements returns the 402 body for a topup of `amountMicro`
// (USDC micro-units) to be paid to `payTo`. The nonce is a random
// 32-byte value; the client must include it in its signed payload.
//
// The validAfter/validBefore window is set to (now-60s, now+maxTimeoutSeconds)
// so the client signs an authorization that the verifier will actually
// accept. Without this, the client can sign anything but the server
// rejects it as "expired" (validBefore=0 in the empty Extra).
func (u *USDC) BuildRequirements(resource, description, payTo string, amountMicro int64) (PaymentRequirements, [32]byte, error) {
	if amountMicro <= 0 {
		return PaymentRequirements{}, [32]byte{}, errors.New("amount must be positive")
	}
	nonce, err := randomBytes32()
	if err != nil {
		return PaymentRequirements{}, [32]byte{}, err
	}
	now := time.Now().Unix()
	return PaymentRequirements{
		X402Version:       2,
		Scheme:            "exact",
		Network:           u.CAIP2(),
		MaxAmountRequired: fmt.Sprintf("%d", amountMicro),
		Resource:          resource,
		Description:       description,
		MimeType:          "application/json",
		PayTo:             payTo,
		MaxTimeoutSeconds: 60,
		Asset:             u.Address.Hex(),
		Extra: map[string]string{
			"name":        USDCDomainName,
			"version":     USDCDomainVersion,
			"validAfter":  fmt.Sprintf("%d", now-60),
			"validBefore": fmt.Sprintf("%d", now+60),
		},
	}, nonce, nil
}

// PaymentAuth is the client-supplied signed payload attached to a
// topup retry via the PAYMENT-SIGNATURE header (or body for POST
// /v1/topup). It mirrors EIP-3009's TransferWithAuthorization fields.
type PaymentAuth struct {
	From        string `json:"from"`
	To          string `json:"to"`
	Value       string `json:"value"`         // micro-units
	ValidAfter  string `json:"validAfter"`    // unix seconds
	ValidBefore string `json:"validBefore"`   // unix seconds
	Nonce       string `json:"nonce"`         // 0x-prefixed bytes32
	V           uint8  `json:"v"`
	R           string `json:"r"`             // 0x-prefixed bytes32
	S           string `json:"s"`             // 0x-prefixed bytes32
}

// VerifyPaymentAuth reconstructs the EIP-712 typed-data hash for
// TransferWithAuthorization and uses ecrecover to confirm the
// signature was produced by `expectedPayer` (lowercase 0x...). It
// also checks validAfter/validBefore timing. Returns the recovered
// address (lowercase 0x) on success.
func (u *USDC) VerifyPaymentAuth(expectedPayer string, auth PaymentAuth) (string, error) {
	expected, err := normalizeAddr(expectedPayer)
	if err != nil {
		return "", err
	}
	from, err := normalizeAddr(auth.From)
	if err != nil {
		return "", fmt.Errorf("auth.from: %w", err)
	}
	if from != expected {
		return "", fmt.Errorf("auth.from (%s) does not match expected payer (%s)", from, expected)
	}
	if !common.IsHexAddress(auth.To) {
		return "", fmt.Errorf("auth.to invalid: %q", auth.To)
	}
	value, ok := new(big.Int).SetString(auth.Value, 10)
	if !ok || value.Sign() <= 0 {
		return "", fmt.Errorf("auth.value invalid: %q", auth.Value)
	}
	validAfter, ok := new(big.Int).SetString(auth.ValidAfter, 10)
	if !ok {
		return "", fmt.Errorf("auth.validAfter invalid: %q", auth.ValidAfter)
	}
	validBefore, ok := new(big.Int).SetString(auth.ValidBefore, 10)
	if !ok {
		return "", fmt.Errorf("auth.validBefore invalid: %q", auth.ValidBefore)
	}
	now := time.Now().Unix()
	if validAfter.Int64() > now {
		return "", fmt.Errorf("authorization not yet valid (validAfter=%d, now=%d)", validAfter.Int64(), now)
	}
	if validBefore.Int64() < now {
		return "", fmt.Errorf("authorization expired (validBefore=%d, now=%d)", validBefore.Int64(), now)
	}
	nonce, err := hexutil.Decode("0x" + strings.TrimPrefix(auth.Nonce, "0x"))
	if err != nil || len(nonce) != 32 {
		return "", fmt.Errorf("auth.nonce invalid: %q", auth.Nonce)
	}
	r, err := hexutil.Decode("0x" + strings.TrimPrefix(auth.R, "0x"))
	if err != nil || len(r) != 32 {
		return "", fmt.Errorf("auth.r invalid")
	}
	s, err := hexutil.Decode("0x" + strings.TrimPrefix(auth.S, "0x"))
	if err != nil || len(s) != 32 {
		return "", fmt.Errorf("auth.s invalid")
	}

	// EIP-712 domain separator.
	// Per the EIP-712 spec: atomic types (address, uint256, bytes32) are
	// padded to 32 bytes and inlined directly; only bytes/string are
	// hashed. Don't hash address/uint256.
	domainType := []byte("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)")
	domainTypeHash := crypto.Keccak256(domainType)

	nameHash := crypto.Keccak256([]byte(USDCDomainName))
	versionHash := crypto.Keccak256([]byte(USDCDomainVersion))
	chainIDBytes := leftPad32(u.ChainID.Bytes())
	addrBytes := leftPad32(u.Address.Bytes())

	domainSeparator := crypto.Keccak256(
		domainTypeHash,
		nameHash,
		versionHash,
		chainIDBytes,
		addrBytes,
	)

	// EIP-712 struct hash for TransferWithAuthorization. Same encoding
	// rule: atomic types inlined as 32 bytes, only bytes32 (the nonce)
	// is "as-is" (already 32 bytes).
	msgType := []byte("TransferWithAuthorization(address from,address to,uint256 value,uint256 validAfter,uint256 validBefore,bytes32 nonce)")
	typeHash := crypto.Keccak256(msgType)

	fromBytes := leftPad32(common.HexToAddress(from).Bytes())
	toBytes := leftPad32(common.HexToAddress(auth.To).Bytes())
	valueBytes := leftPad32(value.Bytes())
	validAfterBytes := leftPad32(validAfter.Bytes())
	validBeforeBytes := leftPad32(validBefore.Bytes())
	nonceBytes := make([]byte, 32)
	copy(nonceBytes, nonce)

	structHash := crypto.Keccak256(
		typeHash,
		fromBytes,
		toBytes,
		valueBytes,
		validAfterBytes,
		validBeforeBytes,
		nonceBytes,
	)
	// EIP-712 final digest = keccak256(0x19 0x01 || domainSeparator || structHash)
	msgHash := crypto.Keccak256(
		[]byte{0x19, 0x01},
		domainSeparator,
		structHash,
	)

	// ecrecover expects 0x{r, s, v} where r,s are 32 bytes and v is
	// the last byte. EIP-3009 (and EIP-712) carry v ∈ {27, 28};
	// go-ethereum's SigToPub expects v ∈ {0, 1} at byte 64. We
	// accept both on input to be lenient with implementations that
	// already normalize.
	v := int(auth.V)
	if v >= 27 {
		v -= 27
	}
	if v != 0 && v != 1 {
		return "", fmt.Errorf("invalid v: %d (must be 0/1 or 27/28)", auth.V)
	}
	sig := make([]byte, 65)
	copy(sig[0:32], r)
	copy(sig[32:64], s)
	sig[64] = byte(v)

	pub, err := crypto.SigToPub(msgHash, sig)
	if err != nil {
		return "", fmt.Errorf("ecrecover: %w", err)
	}
	if os.Getenv("AGW_DEBUG_EIP712") == "1" {
		// debug helper for cross-checking with the client's sign tool
		fmt.Fprintf(os.Stderr, "DEBUG EIP-712: msgHash=%x sig[64]=%d v-in=%d recovered=%s expected=%s\n",
			msgHash, sig[64], auth.V, crypto.PubkeyToAddress(*pub).Hex(), expected)
		fmt.Fprintf(os.Stderr, "  domainSeparator=%x\n  structHash=%x\n  r=%x\n  s=%x\n",
			domainSeparator, structHash, r, s)
	}
	recovered := strings.ToLower(crypto.PubkeyToAddress(*pub).Hex())
	if recovered != expected {
		return "", fmt.Errorf("signature does not match expected payer (recovered=%s)", recovered)
	}
	return recovered, nil
}

// SettlePaymentAuth submits transferWithAuthorization to the USDC
// contract and waits for the receipt. Returns the tx hash on success.
// If ServerKey is nil, returns an error (caller should fall back to
// "credit on signature only" mode for offline demos).
func (u *USDC) SettlePaymentAuth(ctx context.Context, auth PaymentAuth) (string, error) {
	if u.ServerKey == nil {
		return "", errors.New("usdc: server key not configured (offline mode)")
	}
	if u.client == nil {
		c, err := ethclient.Dial(u.RPCURL)
		if err != nil {
			return "", fmt.Errorf("usdc: dial rpc: %w", err)
		}
		u.client = c
	}

	value, ok := new(big.Int).SetString(auth.Value, 10)
	if !ok {
		return "", fmt.Errorf("usdc: bad value %q", auth.Value)
	}
	validAfter, _ := new(big.Int).SetString(auth.ValidAfter, 10)
	validBefore, _ := new(big.Int).SetString(auth.ValidBefore, 10)
	nonce, err := hexutil.Decode("0x" + strings.TrimPrefix(auth.Nonce, "0x"))
	if err != nil {
		return "", err
	}
	r, _ := hexutil.Decode("0x" + strings.TrimPrefix(auth.R, "0x"))
	s, _ := hexutil.Decode("0x" + strings.TrimPrefix(auth.S, "0x"))

	// transferWithAuthorization(address,address,uint256,uint256,uint256,bytes32,uint8,bytes32,bytes32)
	const transferWithAuthABI = `[{
		"inputs": [
			{"name":"from","type":"address"},
			{"name":"to","type":"address"},
			{"name":"value","type":"uint256"},
			{"name":"validAfter","type":"uint256"},
			{"name":"validBefore","type":"uint256"},
			{"name":"nonce","type":"bytes32"},
			{"name":"v","type":"uint8"},
			{"name":"r","type":"bytes32"},
			{"name":"s","type":"bytes32"}
		],
		"name":"transferWithAuthorization",
		"outputs":[{"name":"","type":"bool"}],
		"stateMutability":"nonpayable",
		"type":"function"
	}]`

	parsed, err := abi.JSON(strings.NewReader(transferWithAuthABI))
	if err != nil {
		return "", fmt.Errorf("usdc: parse abi: %w", err)
	}
	data, err := parsed.Pack("transferWithAuthorization",
		common.HexToAddress(auth.From),
		common.HexToAddress(auth.To),
		value,
		validAfter,
		validBefore,
		[32]byte(nonce),
		auth.V,
		[32]byte(r),
		[32]byte(s),
	)
	if err != nil {
		return "", fmt.Errorf("usdc: pack: %w", err)
	}

	// Build & sign tx
	chainID, err := u.client.ChainID(ctx)
	if err != nil {
		return "", fmt.Errorf("usdc: chain id: %w", err)
	}
	nonceOnChain, err := u.client.PendingNonceAt(ctx, crypto.PubkeyToAddress(u.ServerKey.PublicKey))
	if err != nil {
		return "", fmt.Errorf("usdc: nonce: %w", err)
	}
	gasPrice, err := u.client.SuggestGasPrice(ctx)
	if err != nil {
		return "", fmt.Errorf("usdc: gas price: %w", err)
	}

	tx := types.NewTx(&types.LegacyTx{
		Nonce:    nonceOnChain,
		To:       &u.Address,
		Value:    big.NewInt(0),
		Gas:      200000,
		GasPrice: gasPrice,
		Data:     data,
	})
	signed, err := types.SignTx(tx, types.NewEIP155Signer(chainID), u.ServerKey)
	if err != nil {
		return "", fmt.Errorf("usdc: sign: %w", err)
	}
	if err := u.client.SendTransaction(ctx, signed); err != nil {
		return "", fmt.Errorf("usdc: send: %w", err)
	}

	// Wait for receipt
	waitCtx, cancel := context.WithTimeout(ctx, u.ReceiptTimeout)
	defer cancel()
	receipt, err := bind.WaitMined(waitCtx, u.client, signed)
	if err != nil {
		return "", fmt.Errorf("usdc: wait mined: %w", err)
	}
	if receipt.Status != types.ReceiptStatusSuccessful {
		return "", fmt.Errorf("usdc: tx reverted (gas used %d)", receipt.GasUsed)
	}
	return signed.Hash().Hex(), nil
}

// leftPad32 left-pads b to 32 bytes. EIP-712 expects 32-byte aligned
// field encodings for address and uint256.
func leftPad32(b []byte) []byte {
	if len(b) >= 32 {
		return b[len(b)-32:]
	}
	out := make([]byte, 32)
	copy(out[32-len(b):], b)
	return out
}

// randomBytes32 returns a cryptographically random 32-byte value.
func randomBytes32() ([32]byte, error) {
	var b [32]byte
	_, err := rand.Read(b[:])
	return b, err
}

// ecdsaKeyFromBytes parses a raw 32-byte secp256k1 private key.
func ecdsaKeyFromBytes(b []byte) (*ecdsa.PrivateKey, error) {
	if len(b) != 32 {
		return nil, fmt.Errorf("expected 32 bytes, got %d", len(b))
	}
	priv, err := crypto.ToECDSA(b)
	if err != nil {
		return nil, err
	}
	return priv, nil
}
