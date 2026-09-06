// charge.go — usage-based billing on the hot path.
//
// After the upstream response is fully streamed (or aborted), this
// module reads the captured usage from the tracked session, computes
// a USD cost via AGW's existing pricingCost helper, converts to
// micro-USDC (6 decimals), and debits the owner's balance. If usage
// is unavailable (e.g. the upstream didn't return one), a small
// flat minimum is charged so the hot path can never be free.
//
// This file owns the "dollars → micro-units" conversion; the pricing
// table is configured in the standard AGW pricing block and read
// directly from the Proxy.
package agw

import (
	"errors"
	"math"
)

// minChargeMicro is the per-call minimum charged when the upstream
// didn't return a usable usage object. Keeps the gateway from being
// a free-for-all against misconfigured upstreams.
const minChargeMicro int64 = 100 // 0.0001 USDC

// chargeFromSession debits the owner's wallet for one proxied
// request, using the usage captured by the session's usageScanner.
// Returns the cost in micro-USDC that was debited, or an error.
//
// The cost is also written to the session's request record (via
// sessionHub.updateRequest) so the journal shows it.
func (p *Proxy) chargeFromSession(s *trackedSession) (int64, error) {
	if p == nil || p.Wallet == nil {
		return 0, errors.New("payments not enabled")
	}
	if s == nil {
		return 0, nil
	}

	// Read what usageScanner captured while streaming the response.
	usage := s.usage.tally()

	// Snapshot the model name from the latest request so we can both
	// price and annotate the journal.
	var modelName string
	s.hub.mu.Lock()
	if rec, ok := s.hub.records[s.sessionID]; ok && rec != nil && len(rec.Requests) > 0 {
		modelName = rec.Requests[len(rec.Requests)-1].Model
	}
	s.hub.mu.Unlock()

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

	// 4) Debit
	if _, err := p.Wallet.Debit(micro, "usage: "+modelName); err != nil {
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
