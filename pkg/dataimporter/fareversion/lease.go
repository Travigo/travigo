package fareversion

import (
	"context"
	"fmt"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
)

// EnsureImportLease prevents a worker whose dataset lease was fenced from
// writing more records into its abandoned fare version.
func EnsureImportLease(ctx context.Context, datasource *ctdf.DataSourceReference) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if datasource == nil || datasource.FareLeaseOwner == "" || datasource.FareActivationFence == 0 {
		return nil
	}
	now := time.Now()
	err := database.GetCollection("fare_versions").FindOne(ctx, bson.M{
		"datasetid": datasource.DatasetID, "leaseowner": datasource.FareLeaseOwner,
		"activationfence": datasource.FareActivationFence, "leaseexpiresat": bson.M{"$gt": now},
	}).Err()
	if err != nil {
		return fmt.Errorf("fare import lease is no longer current: %w", err)
	}
	return nil
}
