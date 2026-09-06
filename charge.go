// charge.go — usage-based billing on the hot path.
//
// After the upstream response is fully streamed (or aborted), this
// module reads the captured usage from the tracked session, computes
// a USD cost via AGW's existing pricingCost helper, converts to
// micro-USDC (6 decimals), and debits the **user that owns the API
// key** for this session. If usage is unavailable (e.g. the upstream
// didn't return one), a small flat minimum is charged so the hot
// path can never be free.
package agw

import (
	"errors"
	"math"
)

// minChargeMicro is the per-call minimum charged when the upstream
// didn't return a usable usage object.
const minChargeMicro int64 = 100 // 0.0001 USDC

// chargeFromSession debits the user that owns the API key for one
// proxied request, using the usage captured by the session's
// usageScanner. Returns the cost in micro-USDC that was debited, or
// an error.
//
// The cost + user are also written to the session's request record
// (via sessionHub.updateRequest) so the journal shows who paid.
func (p *Proxy) chargeFromSession(s *trackedSession) (int64, error) {
	if p == nil || p.Wallet == nil {
		return 0, errors.New("payments not enabled")
	}
	if s == nil {
		return 0, nil
	}

	// Find the user (from API key) and model name from the session.
	var userAddr, modelName string
	s.hub.mu.Lock()
	if rec, ok := s.hub.records[s.sessionID]; ok && rec != nil && len(rec.Requests) > 0 {
		req := rec.Requests[len(rec.Requests)-1]
		userAddr = req.UserAddr
		modelName = req.Model
	}
	s.hub.mu.Unlock()
	if userAddr == "" {
		// No user on the session — cannot charge. Log and skip.
		p.Logger.Warn("skipping charge: no user on session", "session", s.sessionID)
		return 0, nil
	}

	// Read what usageScanner captured while streaming the response.
	usage := s.usage.tally()

	// 1) Pricing-based cost
	var usdCost float64
	if usage.Seen && modelName != "" {
		p.Mu.RLock()
		pricing := append([]PricingRule(nil), p.Pricing...)
		p.Mu.RUnlock()
		usdCost = pricingCost(pricing, modelName, usage)
	}

	// 2) USD → micro-USDC
	micro := int64(math.Round(usdCost * float64(USDCScale)))

	// 3) Enforce minimum
	if micro < minChargeMicro {
		micro = minChargeMicro
	}

	// 4) Debit the user (not a global "owner")
	if _, err := p.Wallet.Debit(userAddr, micro, "usage: "+modelName); err != nil {
		return 0, err
	}

	// 5) Annotate the session's request record
	if s.hub != nil {
		s.hub.updateRequest(s, func(req *sessionRequest) {
			req.CostMicroUSDC = micro
			req.CostUSD = usdCost
		})
	}

	p.Logger.Info("charged",
		"user", userAddr,
		"model", modelName,
		"tokens_in", usage.InputTokens,
		"tokens_out", usage.OutputTokens,
		"cache_read", usage.CacheReadTokens,
		"cache_write", usage.CacheWriteTokens,
		"usd", usdCost,
		"micro", micro,
	)
	return micro, nil
}
