package metadata

import "context"

// Chain coordinates metadata enrichment providers in configured order.
// PR1 scaffold: behavior is intentionally conservative and side-effect free.
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

// Run executes providers in order and returns the first non-empty patch.
// Full merge-policy orchestration will be expanded in later rollout steps.
func (c *Chain) Run(ctx context.Context, req Request) (*Patch, error) {
	if c == nil {
		return nil, nil
	}
	for _, p := range c.providers {
		patch, err := p.Enrich(ctx, req)
		if err != nil {
			return nil, err
		}
		if !patch.Empty() {
			return patch, nil
		}
	}
	return nil, nil
}

