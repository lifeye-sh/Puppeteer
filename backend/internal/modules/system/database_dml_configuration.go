package system

import "context"

const DatabaseBlockDMLKey = "db-proxy-block-dml"

type databaseDMLProofKey struct{}

func WithDatabaseDMLConfigurationProof(ctx context.Context) context.Context {
	return context.WithValue(ctx, databaseDMLProofKey{}, true)
}
func hasDatabaseDMLConfigurationProof(ctx context.Context) bool {
	v, _ := ctx.Value(databaseDMLProofKey{}).(bool)
	return v
}
