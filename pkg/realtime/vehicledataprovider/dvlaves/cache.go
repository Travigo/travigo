package dvlaves

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/realtime/vehicledataprovider"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	successTTL  = 90 * 24 * time.Hour
	negativeTTL = 24 * time.Hour
	claimTTL    = 15 * time.Second
)

type cachedVehicle struct {
	ExpiresAt time.Time
	Claim     primitive.ObjectID
	Details   *ctdf.JourneyDetailedBus
}

func (client *Client) EnsureCacheIndex(ctx context.Context) error {
	_, err := client.cache.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "expiresat", Value: 1}},
		Options: options.Index().SetExpireAfterSeconds(0),
	})
	return err
}

func (client *Client) GetVehicleDataBus(ctx context.Context, registrationNumber string) (*ctdf.JourneyDetailedBus, error) {
	registration := NormalizeRegistration(registrationNumber)
	if registration == "" {
		return nil, nil
	}
	key := bson.M{"_id": registration}
	for {
		var cached cachedVehicle
		err := client.cache.FindOne(ctx, key).Decode(&cached)
		now := time.Now()
		switch {
		case err != nil && !errors.Is(err, mongo.ErrNoDocuments):
			return nil, fmt.Errorf("%w: read DVLA VES cache: %w", vehicledataprovider.ErrLookupDeferred, err)
		case err == nil && now.Before(cached.ExpiresAt):
			if cached.Claim.IsZero() {
				return cached.Details, nil
			}
			if err := wait(ctx, 200*time.Millisecond); err != nil {
				return nil, fmt.Errorf("%w: wait for DVLA VES cache: %w", vehicledataprovider.ErrLookupDeferred, err)
			}
			continue
		}

		// The unique _id and expiry predicate allow one caller to replace
		// an expired entry or insert a lease before making an HTTP request.
		claim := primitive.NewObjectID()
		_, err = client.cache.UpdateOne(ctx, bson.M{"_id": registration, "expiresat": bson.M{"$lte": now}},
			bson.M{"$set": bson.M{"expiresat": now.Add(claimTTL), "claim": claim}, "$unset": bson.M{"details": ""}},
			options.Update().SetUpsert(true))
		if mongo.IsDuplicateKeyError(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("%w: claim DVLA VES cache entry: %w", vehicledataprovider.ErrLookupDeferred, err)
		}
		return client.fetchAndCache(ctx, registration, claim)
	}
}

func (client *Client) fetchAndCache(ctx context.Context, registration string, claim primitive.ObjectID) (*ctdf.JourneyDetailedBus, error) {
	var details *ctdf.JourneyDetailedBus
	var status int
	lookupErr := client.waitForRequestSlot(ctx)
	if lookupErr == nil {
		details, status, lookupErr = client.lookup(ctx, registration)
	} else {
		lookupErr = fmt.Errorf("%w: %w", vehicledataprovider.ErrLookupDeferred, lookupErr)
	}

	switch status {
	case http.StatusTooManyRequests:
		_ = client.redis.Set(ctx, rateKey, "1", time.Minute).Err()
	case http.StatusUnauthorized, http.StatusForbidden:
		_ = client.redis.Set(ctx, rateKey, "1", 5*time.Minute).Err()
	}

	claimFilter := bson.M{"_id": registration, "claim": claim}
	if lookupErr != nil {
		// Release only our claim, even if the lookup exhausted its deadline.
		// The lease expires on its own if MongoDB is unavailable.
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		if _, err := client.cache.DeleteOne(cleanupCtx, claimFilter); err != nil {
			return nil, errors.Join(lookupErr, fmt.Errorf("release DVLA VES cache claim: %w", err))
		}
		return nil, lookupErr
	}

	ttl := successTTL
	if status == http.StatusNotFound {
		ttl = negativeTTL
	}
	_, err := client.cache.UpdateOne(ctx, claimFilter, bson.M{
		"$set":   bson.M{"details": details, "expiresat": time.Now().Add(ttl)},
		"$unset": bson.M{"claim": ""},
	})
	if err != nil {
		return details, fmt.Errorf("write DVLA VES cache: %w", err)
	}
	return details, nil
}
