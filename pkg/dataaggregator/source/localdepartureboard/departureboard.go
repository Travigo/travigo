package localdepartureboard

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/kr/pretty"
	"github.com/liip/sheriff"
	"github.com/rs/zerolog/log"
	iso8601 "github.com/senseyeio/duration"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/dataaggregator/source/cachedresults"
	"github.com/travigo/travigo/pkg/database"

	// "github.com/travigo/travigo/pkg/transforms"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func (s Source) DepartureBoardQuery(q query.DepartureBoard) ([]*ctdf.DepartureBoard, error) {
	var departureBoard []*ctdf.DepartureBoard

	// Calculate tomorrows start date time by shifting current date time by 1 day and then setting hours/minutes/seconds to 0
	nextDayDuration, _ := iso8601.ParseISO8601("P1D")
	dayAfterDateTime := nextDayDuration.Shift(q.StartDateTime)
	dayAfterDateTime = time.Date(
		dayAfterDateTime.Year(), dayAfterDateTime.Month(), dayAfterDateTime.Day(), 0, 0, 0, 0, dayAfterDateTime.Location(),
	)

	// Contains the stops primary id and all platforms primary ids
	allStopIDs := q.Stop.GetAllStopIDs()

	// Load from cache

	filterHash := sha256.New()
	filterHash.Write([]byte(pretty.Sprint(q.Filter)))
	filterHashString := fmt.Sprintf("%x", filterHash.Sum(nil))

	currentTime := time.Now()

	baseCacheItemPath := fmt.Sprintf("cachedresults/departureboardjourneys/%s/%s", q.Stop.PrimaryIdentifier, filterHashString)
	journeyQuery := bson.M{"path.originstopref": bson.M{"$in": allStopIDs}}
	if q.Filter != nil {
		journeyQuery = bson.M{
			"$and": bson.A{
				journeyQuery,
				q.Filter,
			},
		}
	}
	journeysToday := s.getDateJourneys(baseCacheItemPath, journeyQuery, q.StartDateTime)

	log.Debug().Str("Length", time.Now().Sub(currentTime).String()).Int("num", len(journeysToday)).Msg("Get cached journeys - today")

	currentTime = time.Now()
	departureBoardToday := ctdf.GenerateDepartureBoardFromJourneys(journeysToday, allStopIDs, q.StartDateTime, true)
	log.Debug().Str("Length", time.Now().Sub(currentTime).String()).Msg("Departure Board generation today")

	// If not enough journeys in todays departure board then look into tomorrows
	if len(departureBoardToday) < q.Count {
		currentTime = time.Now()

		journeysTomorrow := s.getDateJourneys(baseCacheItemPath, journeyQuery, dayAfterDateTime)

		log.Debug().Str("Length", time.Now().Sub(currentTime).String()).Int("num", len(journeysToday)).Msg("Get cached journeys - tomorrow")
		currentTime = time.Now()

		departureBoardTomorrow := ctdf.GenerateDepartureBoardFromJourneys(journeysTomorrow, allStopIDs, dayAfterDateTime, false)
		log.Debug().Str("Length", time.Now().Sub(currentTime).String()).Msg("Departure Board generation tomorrow")

		departureBoard = append(departureBoardToday, departureBoardTomorrow...)
	} else {
		departureBoard = departureBoardToday
	}

	return departureBoard, nil
}

func (s Source) getDateJourneys(baseCacheItemPath string, journeyQuery bson.M, dateTime time.Time) []*ctdf.Journey {
	var journeys []*ctdf.Journey

	cacheItemPath := fmt.Sprintf("%s/%s", baseCacheItemPath, dateTime.Format("2006-01-02"))

	journeys, err := cachedresults.Get[[]*ctdf.Journey](s.CachedResults, cacheItemPath)

	if err == nil {
		return journeys
	}

	journeysCollection := database.GetCollection("journeys")
	currentTime := time.Now()

	// This projection excludes values we dont care about - the main ones being path.*
	// Reduces memory usage and execution time
	opts := options.Find().SetProjection(bson.D{
		bson.E{Key: "_id", Value: 0},
		bson.E{Key: "otheridentifiers", Value: 0},
		bson.E{Key: "datasource", Value: 0},
		bson.E{Key: "creationdatetime", Value: 0},
		bson.E{Key: "modificationdatetime", Value: 0},
		bson.E{Key: "direction", Value: 0},
		bson.E{Key: "path.track", Value: 0},
		bson.E{Key: "path.associations", Value: 0},
		bson.E{Key: "path.destinationactivity", Value: 0},
		bson.E{Key: "path.distance", Value: 0},
		bson.E{Key: "path.originstop", Value: 0},
		bson.E{Key: "path.destinationstop", Value: 0},
		bson.E{Key: "detailedrailinformation", Value: 0},
	})

	cursor, err := journeysCollection.Find(context.Background(), journeyQuery, opts)
	if err != nil {
		log.Error().Err(err).Msg("Failed to query Journeys")
	}

	log.Debug().Str("Length", time.Now().Sub(currentTime).String()).Msg("Database lookup")
	currentTime = time.Now()

	for cursor.Next(context.Background()) {
		var journey ctdf.Journey
		err := cursor.Decode(&journey)
		if err != nil {
			log.Error().Err(err).Msg("Failed to decode journey")
		}

		if journey.Availability.MatchDate(dateTime) {
			journeys = append(journeys, &journey)
		}
	}

	log.Debug().Str("Length", time.Now().Sub(currentTime).String()).Msg("Database lookup decode 2")

	writeCacheTime := time.Now()
	reducedJourneys, _ := sheriff.Marshal(&sheriff.Options{
		Groups: []string{"departureboard-cache"},
	}, journeys)

	cachedresults.Set(s.CachedResults, cacheItemPath, reducedJourneys, 18*time.Hour)
	log.Debug().Str("Length", time.Now().Sub(writeCacheTime).String()).Msg("Write cache")

	return journeys
}
