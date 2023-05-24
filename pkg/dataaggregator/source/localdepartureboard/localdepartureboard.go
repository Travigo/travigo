package localdepartureboard

import (
	"github.com/eko/gocache/lib/v4/cache"
	redisstore "github.com/eko/gocache/store/redis/v4"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator/source"
	"github.com/travigo/travigo/pkg/redis_client"
	"reflect"
)

type Source struct {
	realtimeJourneysStore *cache.Cache[string]
}

func (s Source) GetName() string {
	return "Local Departure Board Generator"
}

func (s Source) Supports() []reflect.Type {
	return []reflect.Type{
		reflect.TypeOf([]*ctdf.DepartureBoard{}),
		reflect.TypeOf(ctdf.RealtimeJourney{}),
	}
}

func (s Source) Lookup(q any) (interface{}, error) {
	switch q.(type) {
	case ctdf.QueryDepartureBoard:
		return s.DepartureBoardQuery(q.(ctdf.QueryDepartureBoard))
	case ctdf.RealtimeJourneyForJourney:
		return s.RealtimeJourneyForJourneyQuery(q.(ctdf.RealtimeJourneyForJourney))
	default:
		return nil, source.UnsupportedSourceError
	}
}

func (s Source) Setup() Source {
	redisStore := redisstore.NewRedis(redis_client.Client)

	s.realtimeJourneysStore = cache.New[string](redisStore)

	return s
}
