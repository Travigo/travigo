package realtimestore

import (
	"context"

	"github.com/redis/go-redis/v9"
	"github.com/travigo/travigo/pkg/redis_client"
)

const locationIndexCleanupBatchSize int64 = 256

var removeStaleLocationIndexMembers = redis.NewScript(`
local removed = 0
for index = 2, #ARGV do
	local identifier = ARGV[index]
	if redis.call("EXISTS", ARGV[1] .. identifier) == 0 then
		removed = removed + redis.call("ZREM", KEYS[1], identifier)
	end
end
return removed
`)

// CleanupStaleLocationIndex removes geo-index members after their expiring
// location payload has gone. The script makes each existence check and removal
// atomic with respect to location updates, while ZSCAN keeps each cleaner pass
// incremental for Redis.
func CleanupStaleLocationIndex(ctx context.Context) (int64, error) {
	indexKey := realtimeJourneyLocationGeoIndexKey()
	var cursor uint64
	var removed int64

	for {
		values, nextCursor, err := redis_client.Client.ZScan(
			ctx,
			indexKey,
			cursor,
			"*",
			locationIndexCleanupBatchSize,
		).Result()
		if err != nil {
			return removed, err
		}

		identifiers := make([]interface{}, 1, len(values)/2+1)
		identifiers[0] = realtimeJourneyLocationKey("")
		for index := 0; index+1 < len(values); index += 2 {
			identifiers = append(identifiers, values[index])
		}

		if len(identifiers) > 1 {
			batchRemoved, err := removeStaleLocationIndexMembers.Run(
				ctx,
				redis_client.Client,
				[]string{indexKey},
				identifiers...,
			).Int64()
			if err != nil {
				return removed, err
			}
			removed += batchRemoved
		}

		cursor = nextCursor
		if cursor == 0 {
			return removed, nil
		}
	}
}
