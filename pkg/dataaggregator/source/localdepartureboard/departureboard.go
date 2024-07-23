package localdepartureboard

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/kr/pretty"
	"github.com/rs/zerolog/log"
	iso8601 "github.com/senseyeio/duration"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/dataaggregator/source/cachedresults"
	"github.com/travigo/travigo/pkg/database"
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

	var journeys []*ctdf.Journey
	// Load from cache

	filterHash := sha256.New()
	filterHash.Write([]byte(pretty.Sprint(q.Filter)))
	filterHashString := fmt.Sprintf("%x", filterHash.Sum(nil))

	cacheItemPath := fmt.Sprintf("cachedresults/departureboardjourneys/%s/%s", q.Stop.PrimaryIdentifier, filterHashString)
	journeys, err := cachedresults.Get[[]*ctdf.Journey](s.CachedResults, cacheItemPath)

	if err != nil {
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
			bson.E{Key: "path.destinationactivity", Value: 0},
			bson.E{Key: "path.distance", Value: 0},
			bson.E{Key: "path.originstop", Value: 0},
			bson.E{Key: "path.destinationstop", Value: 0},
		})

		journeyQuery := bson.M{"path.originstopref": bson.M{"$in": allStopIDs}}
		if q.Filter != nil {
			journeyQuery = bson.M{
				"$and": bson.A{
					bson.M{"path.originstopref": bson.M{"$in": allStopIDs}},
					q.Filter,
				},
			}
		}

		pretty.Println(journeyQuery)

		cursor, err := journeysCollection.Find(context.Background(), journeyQuery, opts)
		if err != nil {
			log.Error().Err(err).Msg("Failed to query Journeys")
		}

		log.Debug().Str("Length", time.Now().Sub(currentTime).String()).Msg("Database lookup")
		currentTime = time.Now()

		err = cursor.All(context.Background(), &journeys)
		if err != nil {
			log.Error().Err(err).Msg("Failed to decode Journey")
		}

		log.Debug().Str("Length", time.Now().Sub(currentTime).String()).Msg("Database lookup decode 2")
		cachedresults.Set(s.CachedResults, cacheItemPath, journeys, 4*time.Hour)
	}

	currentTime := time.Now()
	departureBoardToday := ctdf.GenerateDepartureBoardFromJourneys(journeys, allStopIDs, q.StartDateTime, true)
	log.Debug().Str("Length", time.Now().Sub(currentTime).String()).Msg("Departure Board generation today")

	// If not enough journeys in todays departure board then look into tomorrows
	if len(departureBoardToday) < q.Count {
		currentTime = time.Now()
		departureBoardTomorrow := ctdf.GenerateDepartureBoardFromJourneys(journeys, allStopIDs, dayAfterDateTime, false)
		log.Debug().Str("Length", time.Now().Sub(currentTime).String()).Msg("Departure Board generation tomorrow")

		departureBoard = append(departureBoardToday, departureBoardTomorrow...)
	} else {
		departureBoard = departureBoardToday
	}

	currentTime = time.Now()
	// Transforming the whole document is incredibly ineffecient
	// Instead just transform the Operator & Service as those are the key values
	// for _, item := range departureBoard {
	// 	transforms.Transform(item.Journey.Operator, 1)
	// 	transforms.Transform(item.Journey.Service, 1)
	// }
	log.Debug().Str("Length", time.Now().Sub(currentTime).String()).Msg("Transform")

	return departureBoard, nil
}
