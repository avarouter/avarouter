// client.go — minimal Go demo client for the AGW x402 prepaid gateway.
// This is a CLI tool that proves the end-to-end flow: 402 → sign → 200 → spend.
package main

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

const (
	usdcFuji = "0x5425890298aed601595a70AB815c96711a31Bc65"
	chainID  = int64(43113)
)

func leftPad32(b []byte) []byte {
	if len(b) >= 32 {
		return b[len(b)-32:]
	}
	out := make([]byte, 32)
	copy(out[32-len(b):], b)
	return out
}

func main() {
	gateway := flag.String("gateway", "http://127.0.0.1:8080", "AGW gateway URL")
	amount := flag.Int64("amount", 1000000, "top-up amount in USDC micro-units (default 1 USDC)")
	flag.Parse()

	// 1) Generate or load key (in-memory only for demo)
	priv, err := crypto.GenerateKey()
	if err != nil {
		panic(err)
	}
	payer := crypto.PubkeyToAddress(priv.PublicKey).Hex()
	fmt.Printf("=== AGW x402 demo client ===\n")
	fmt.Printf("Payer: %s\n", payer)
	fmt.Printf("Private key: 0x%x\n\n", crypto.FromECDSA(priv))

	// 2) Round 1: ask for payment requirements
	fmt.Println(">>> POST /v1/topup (no payment)")
	body, _ := json.Marshal(map[string]any{"amount": fmt.Sprintf("%d", *amount)})
	req0, _ := http.NewRequest("POST", *gateway+"/v1/topup", bytes.NewReader(body))
	req0.Header.Set("Content-Type", "application/json")
	req0.Header.Set("X-Payer", payer)
	r, err := http.DefaultClient.Do(req0)
	if err != nil {
		panic(err)
	}
	defer r.Body.Close()
	raw, _ := io.ReadAll(r.Body)
	if r.StatusCode != 402 {
		fmt.Printf("expected 402, got %d: %s\n", r.StatusCode, raw)
		os.Exit(1)
	}
	var spec struct {
		Accepts []struct {
			X402Version       int    `json:"x402Version"`
			Scheme            string `json:"scheme"`
			Network           string `json:"network"`
			MaxAmountRequired string `json:"maxAmountRequired"`
			Resource          string `json:"resource"`
			PayTo             string `json:"payTo"`
			Asset             string `json:"asset"`
		} `json:"accepts"`
		Error string `json:"error"`
	}
	json.Unmarshal(raw, &spec)
	acc := spec.Accepts[0]
	fmt.Printf("  402 received: payTo=%s amount=%s network=%s\n", acc.PayTo, acc.MaxAmountRequired, acc.Network)

	// 3) Construct EIP-712 typed data
	nonce, _ := randomBytes32()
	now := time.Now().Unix()
	value := new(big.Int).SetInt64(*amount)
	validAfter := big.NewInt(now - 60)
	validBefore := big.NewInt(now + 600)

	domainType := []byte("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)")
	msgType := []byte("TransferWithAuthorization(address from,address to,uint256 value,uint256 validAfter,uint256 validBefore,bytes32 nonce)")
	domainSep := crypto.Keccak256(
		crypto.Keccak256(domainType),
		crypto.Keccak256([]byte("USD Coin")),
		crypto.Keccak256([]byte("2")),
		crypto.Keccak256(leftPad32(big.NewInt(chainID).Bytes())),
		crypto.Keccak256(leftPad32(common.HexToAddress(usdcFuji).Bytes())),
	)
	structHash := crypto.Keccak256(
		crypto.Keccak256(msgType),
		crypto.Keccak256(leftPad32(common.HexToAddress(payer).Bytes())),
		crypto.Keccak256(leftPad32(common.HexToAddress(acc.PayTo).Bytes())),
		crypto.Keccak256(leftPad32(value.Bytes())),
		crypto.Keccak256(leftPad32(validAfter.Bytes())),
		crypto.Keccak256(leftPad32(validBefore.Bytes())),
		crypto.Keccak256(nonce[:]),
	)
	msgHash := crypto.Keccak256([]byte{0x19, 0x01}, domainSep, structHash)

	sig, err := crypto.Sign(msgHash, priv)
	if err != nil {
		panic(err)
	}
	rHex := "0x" + common.Bytes2Hex(sig[:32])
	sHex := "0x" + common.Bytes2Hex(sig[32:64])
	v := sig[64] + 27

	auth := map[string]any{
		"from":        payer,
		"to":          acc.PayTo,
		"value":       acc.MaxAmountRequired,
		"validAfter":  fmt.Sprintf("%d", validAfter),
		"validBefore": fmt.Sprintf("%d", validBefore),
		"nonce":       "0x" + common.Bytes2Hex(nonce[:]),
		"v":           v,
		"r":           rHex,
		"s":           sHex,
	}

	// 4) Round 2: submit signed authorization
	fmt.Println("\n>>> POST /v1/topup (with signed payment)")
	payload, _ := json.Marshal(map[string]any{"amount": acc.MaxAmountRequired, "payment": auth})
	req, _ := http.NewRequest("POST", *gateway+"/v1/topup", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Payer", payer)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	body2, _ := io.ReadAll(resp.Body)
	fmt.Printf("  HTTP %d: %s\n", resp.StatusCode, body2)
	if resp.StatusCode != 200 {
		os.Exit(1)
	}

	// 5) Check balance
	fmt.Println("\n>>> GET /payments/balance/{addr}")
	r3, _ := http.Get(*gateway + "/payments/balance/" + payer)
	bal, _ := io.ReadAll(r3.Body)
	fmt.Printf("  %s\n", bal)

	// 6) Make an upstream call (will be settled)
	fmt.Println("\n>>> POST /v1/chat/completions (upstream call)")
	body3, _ := json.Marshal(map[string]any{"model": "mock", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	req2, _ := http.NewRequest("POST", *gateway+"/v1/chat/completions", bytes.NewReader(body3))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("X-Payer", payer)
	r4, _ := http.DefaultClient.Do(req2)
	body4, _ := io.ReadAll(r4.Body)
	fmt.Printf("  HTTP %d: %s\n", r4.StatusCode, body4)

	// 7) Final balance
	fmt.Println("\n>>> Final balance")
	r5, _ := http.Get(*gateway + "/payments/balance/" + payer)
	bal2, _ := io.ReadAll(r5.Body)
	fmt.Printf("  %s\n", bal2)
}

func randomBytes32() ([32]byte, error) {
	var b [32]byte
	_, err := io.ReadFull(rand.Reader, b[:])
	return b, err
}
