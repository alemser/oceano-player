package recognition

import (
	"context"
	"errors"
	"log"
	"strings"
)

// ChainRecognizer tries each Recognizer in order and returns the first
// non-nil result. On rate limit or error from one provider it moves to
// the next instead of giving up. Returns (nil, nil) when every provider
// ends with no match, including after an earlier provider hit rate limit
// and a later one successfully evaluated the capture with no match.
type ChainRecognizer struct {
	chain []Recognizer
	// lastRateLimitedName records the Name() of the last provider that returned
	// ErrRateLimit during the most recent Recognize call. Reset at the start of
	// each call. Used by callers to identify the specific culprit rather than
	// attributing the rate-limit to every provider in the chain.
	lastRateLimitedName string
}

// NewChainRecognizer returns a ChainRecognizer over the given recognizers,
// skipping any nil entries.
func NewChainRecognizer(recognizers ...Recognizer) Recognizer {
	var chain []Recognizer
	for _, r := range recognizers {
		if r != nil {
			chain = append(chain, r)
		}
	}
	switch len(chain) {
	case 0:
		return nil
	case 1:
		return chain[0]
	default:
		return &ChainRecognizer{chain: chain}
	}
}

func (c *ChainRecognizer) Name() string {
	names := make([]string, len(c.chain))
	for i, r := range c.chain {
		names[i] = r.Name()
	}
	return strings.Join(names, "→")
}

// Primary returns the first recognizer in the chain, or nil when the chain is empty.
func (c *ChainRecognizer) Primary() Recognizer {
	if len(c.chain) == 0 {
		return nil
	}
	return c.chain[0]
}

// RateLimitedProviderName returns the Name() of the last provider in the chain
// that returned ErrRateLimit during the most recent Recognize call. Empty when
// no provider hit a rate limit (including when Recognize has not been called yet).
func (c *ChainRecognizer) RateLimitedProviderName() string {
	return c.lastRateLimitedName
}

func (c *ChainRecognizer) Recognize(ctx context.Context, wavPath string) (*Result, error) {
	c.lastRateLimitedName = "" // reset per call
	var lastErr error
	for i, r := range c.chain {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		result, err := r.Recognize(ctx, wavPath)
		if err != nil {
			log.Printf("recognizer chain: %s: %v — trying next", r.Name(), err)
			lastErr = err
			if errors.Is(err, ErrRateLimit) {
				c.lastRateLimitedName = r.Name()
			}
			continue
		}
		if result != nil {
			if i == 0 {
				log.Printf("recognizer chain: %s: match %s — %s", r.Name(), result.Artist, result.Title)
			} else {
				log.Printf("recognizer chain: %s: fallback match %s — %s", r.Name(), result.Artist, result.Title)
			}
			return result, nil
		}
		// Clean no match from this provider: do not let an earlier rate-limit error
		// force the whole chain to report rate-limited after a fallback ran.
		if errors.Is(lastErr, ErrRateLimit) {
			lastErr = nil
		}
		log.Printf("recognizer chain: %s: no match — trying next", r.Name())
	}
	return nil, lastErr
}
