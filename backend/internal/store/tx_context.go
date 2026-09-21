package store

import "context"

// txContextKey keeps a transaction-bound query surface private to this package.
type txContextKey struct{}

// WithDBTX returns a context carrying dbtx for callers that must share an
// existing transaction without opening a nested transaction.
func WithDBTX(ctx context.Context, dbtx DBTX) context.Context {
	return context.WithValue(ctx, txContextKey{}, dbtx)
}

// DBTXFromContext returns the transaction-bound query surface, if one was
// attached with WithDBTX.
func DBTXFromContext(ctx context.Context) DBTX {
	dbtx, _ := ctx.Value(txContextKey{}).(DBTX)
	return dbtx
}
