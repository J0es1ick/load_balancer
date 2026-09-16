package balancer

import "context"

type retryPolicyContextKey struct{}
type upstreamHostContextKey struct{}

type upstreamHostPolicy struct {
	preserve bool
	rewrite  string
}

func WithRetryPolicy(ctx context.Context, policy RetryPolicy) context.Context {
	policy.Methods = append([]string(nil), policy.Methods...)
	policy.Statuses = append([]int(nil), policy.Statuses...)
	return context.WithValue(ctx, retryPolicyContextKey{}, policy)
}

func WithUpstreamHost(ctx context.Context, preserve bool, rewrite string) context.Context {
	return context.WithValue(ctx, upstreamHostContextKey{}, upstreamHostPolicy{preserve: preserve, rewrite: rewrite})
}

func requestRetryPolicy(ctx context.Context, fallback RetryPolicy) RetryPolicy {
	policy, ok := ctx.Value(retryPolicyContextKey{}).(RetryPolicy)
	if !ok {
		return fallback
	}
	return policy
}

func requestUpstreamHost(ctx context.Context) (upstreamHostPolicy, bool) {
	policy, ok := ctx.Value(upstreamHostContextKey{}).(upstreamHostPolicy)
	return policy, ok
}
