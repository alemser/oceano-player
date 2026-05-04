package metadata

import "context"

// Chain coordinates metadata enrichment providers in configured order.
type Chain struct {
	providers   []Provider
	mergePolicy MergePolicy
}

func NewChain(providers []Provider, mergePolicy MergePolicy) *Chain {
	out := make([]Provider, 0, len(providers))
	for _, p := range providers {
		if p != nil {
			out = append(out, p)
		}
	}
	if mergePolicy == "" {
		mergePolicy = MergePolicyFillMissingThenStop
	}
	return &Chain{
		providers:   out,
		mergePolicy: mergePolicy,
	}
}

func (c *Chain) MergePolicy() MergePolicy {
	if c == nil {
		return MergePolicyFillMissingThenStop
	}
	return c.mergePolicy
}

func (c *Chain) ProviderNames() []string {
	if c == nil || len(c.providers) == 0 {
		return nil
	}
	names := make([]string, 0, len(c.providers))
	for _, p := range c.providers {
		names = append(names, p.Name())
	}
	return names
}

// Run executes providers according to merge policy.
// seed is the starting metadata (e.g. already persisted); nil is treated as empty.
// The returned patch is a new value suitable for applying on top of seed semantics.
func (c *Chain) Run(ctx context.Context, req Request, seed *Patch) (*Patch, error) {
	if c == nil {
		return ClonePatch(seed), nil
	}

	policy := c.mergePolicy
	if policy == "" {
		policy = MergePolicyFillMissingThenStop
	}

	switch policy {
	case MergePolicyFirstSuccess:
		return c.runFirstSuccess(ctx, req, seed)
	case MergePolicyCollectAllBestEffort:
		return c.runCollectAll(ctx, req, seed)
	default:
		return c.runFillMissingThenStop(ctx, req, seed)
	}
}

func (c *Chain) runFirstSuccess(ctx context.Context, req Request, seed *Patch) (*Patch, error) {
	out := ClonePatch(seed)
	for _, p := range c.providers {
		patch, err := p.Enrich(ctx, req)
		if err != nil {
			return nil, err
		}
		if patch == nil || patch.Empty() {
			continue
		}
		mergeOverwriteNonEmpty(out, patch)
		return out, nil
	}
	return out, nil
}

func (c *Chain) runCollectAll(ctx context.Context, req Request, seed *Patch) (*Patch, error) {
	out := ClonePatch(seed)
	for _, p := range c.providers {
		patch, err := p.Enrich(ctx, req)
		if err != nil {
			return nil, err
		}
		mergeFillMissing(out, patch)
	}
	return out, nil
}

func (c *Chain) runFillMissingThenStop(ctx context.Context, req Request, seed *Patch) (*Patch, error) {
	out := ClonePatch(seed)
	mask := newMissingMask(seed)
	if !mask.any() {
		return out, nil
	}
	for _, p := range c.providers {
		if mask.satisfiedBy(out) {
			break
		}
		patch, err := p.Enrich(ctx, req)
		if err != nil {
			return nil, err
		}
		mergeFillMissing(out, patch)
		if mask.satisfiedBy(out) {
			break
		}
	}
	return out, nil
}
