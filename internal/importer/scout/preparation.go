package scout

import "context"

// ReadAcquiredPlan reads the plan once, validates its complete local manifest,
// and binds every selected package to its acquisition receipt. The returned
// plan includes receipt SHA-256 hashes and any explicit tile pins. It neither
// downloads nor rewrites inputs; graph preparation still verifies package bytes
// and tile records while importing them.
func ReadAcquiredPlan(ctx context.Context, root, name string, ceiling Budgets) (Plan, error) {
	if err := ctx.Err(); err != nil {
		return Plan{}, err
	}
	p, err := ReadPlan(root, name)
	if err != nil {
		return Plan{}, err
	}
	if err := ValidatePlan(root, p, ceiling); err != nil {
		return Plan{}, err
	}
	for i, pin := range p.Packages {
		if err := ctx.Err(); err != nil {
			return Plan{}, err
		}
		p.Packages[i], err = receipt(root, pin, p.MetadataSHA256, false)
		if err != nil {
			return Plan{}, err
		}
	}
	return p, nil
}
