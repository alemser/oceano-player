package recognition

import (
	"context"
	"log"
	"strings"
)

// ChainRecognizer tries each Recognizer in order and returns the first
// non-nil result. On rate limit or error from one provider it moves to
// the next instead of giving up. Returns (nil, nil) only when all
// providers report no match.
type ChainRecognizer struct {
	chain []Recognizer
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

func (c *ChainRecognizer) Recognize(ctx context.Context, wavPath string) (*Result, error) {
	var lastErr error
	for i, r := range c.chain {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		result, err := r.Recognize(ctx, wavPath)
		if err != nil {
			log.Printf("recognizer chain: %s: %v — trying next", r.Name(), err)
			lastErr = err
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
		log.Printf("recognizer chain: %s: no match — trying next", r.Name())
	}
	return nil, lastErr
}
