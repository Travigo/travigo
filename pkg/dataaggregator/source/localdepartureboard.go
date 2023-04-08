package source

import (
	"context"
	"reflect"
	"sort"
	"time"

	"github.com/britbus/britbus/pkg/ctdf"
	"github.com/britbus/britbus/pkg/dataaggregator/query"
	"github.com/britbus/britbus/pkg/database"
	"github.com/britbus/britbus/pkg/transforms"
	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"

	iso8601 "github.com/senseyeio/duration"
)

type LocalDepartureBoardSource struct {
}

func (l LocalDepartureBoardSource) GetName() string {
	return "Local Departure Board Generator"
}

func (l LocalDepartureBoardSource) Supports() []reflect.Type {
	return []reflect.Type{
		reflect.TypeOf([]*ctdf.DepartureBoard{}),
	}
}

func (l LocalDepartureBoardSource) Lookup(q any) (interface{}, error) {
	switch q.(type) {
	case query.DepartureBoard:
		query := q.(query.DepartureBoard)

		var departureBoard []*ctdf.DepartureBoard

		// Calculate tomorrows start date time by shifting current date time by 1 day and then setting hours/minutes/seconds to 0
		nextDayDuration, _ := iso8601.ParseISO8601("P1D")
		dayAfterDateTime := nextDayDuration.Shift(query.StartDateTime)
		dayAfterDateTime = time.Date(
			dayAfterDateTime.Year(), dayAfterDateTime.Month(), dayAfterDateTime.Day(), 0, 0, 0, 0, dayAfterDateTime.Location(),
		)

		journeys := []*ctdf.Journey{}

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
			bson.E{Key: "destinationdisplay", Value: 0},
			bson.E{Key: "path.track", Value: 0},
			bson.E{Key: "path.originactivity", Value: 0},
			bson.E{Key: "path.destinationactivity", Value: 0},
		})
		cursor, _ := journeysCollection.Find(context.Background(), bson.M{"path.originstopref": query.Stop.PrimaryIdentifier}, opts)

		if err := cursor.All(context.Background(), &journeys); err != nil {
			log.Error().Err(err).Msg("Failed to decode Stop")
		}

		log.Debug().Str("Length", (time.Now().Sub(currentTime).String())).Msg("Database lookup")

		realtimeTimeframe := query.StartDateTime.Format("2006-01-02")

		currentTime = time.Now()
		departureBoardToday := ctdf.GenerateDepartureBoardFromJourneys(journeys, query.Stop.PrimaryIdentifier, query.StartDateTime, realtimeTimeframe, true)
		log.Debug().Str("Length", (time.Now().Sub(currentTime).String())).Msg("Departure Board generation today")

		// If not enough journeys in todays departure board then look into tomorrows
		if len(departureBoardToday) < query.Count {
			currentTime = time.Now()
			departureBoardTomorrow := ctdf.GenerateDepartureBoardFromJourneys(journeys, query.Stop.PrimaryIdentifier, dayAfterDateTime, realtimeTimeframe, false)
			log.Debug().Str("Length", (time.Now().Sub(currentTime).String())).Msg("Departure Board generation tomorrow")

			departureBoard = append(departureBoardToday, departureBoardTomorrow...)
		} else {
			departureBoard = departureBoardToday
		}

		// Sort departures by DepartureBoard time
		sort.Slice(departureBoard, func(i, j int) bool {
			return departureBoard[i].Time.Before(departureBoard[j].Time)
		})

		// Once sorted cut off any records higher than our max count
		if len(departureBoard) > query.Count {
			departureBoard = departureBoard[:query.Count]
		}

		currentTime = time.Now()
		// Transforming the whole document is incredibly ineffecient
		// Instead just transform the Operator & Service as those are the key values
		for _, item := range departureBoard {
			transforms.Transform(item.Journey.Operator, 1)
			transforms.Transform(item.Journey.Service, 1)
		}
		log.Debug().Str("Length", (time.Now().Sub(currentTime).String())).Msg("Transform")

		return departureBoard, nil
	default:
		return nil, UnsupportedSourceError
	}
}
