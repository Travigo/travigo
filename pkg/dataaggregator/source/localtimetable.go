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

type LocalTimetableSource struct {
}

func (l LocalTimetableSource) GetName() string {
	return "Local Timetable Generator"
}

func (l LocalTimetableSource) Supports() []reflect.Type {
	return []reflect.Type{
		reflect.TypeOf([]*ctdf.TimetableRecord{}),
	}
}

func (l LocalTimetableSource) Lookup(q any) (interface{}, error) {
	switch q.(type) {
	case query.TimetableRecords:
		query := q.(query.TimetableRecords)

		var journeysTimetable []*ctdf.TimetableRecord

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
		journeysTimetableToday := ctdf.GenerateTimetableFromJourneys(journeys, query.Stop.PrimaryIdentifier, query.StartDateTime, realtimeTimeframe, true)
		log.Debug().Str("Length", (time.Now().Sub(currentTime).String())).Msg("Timetable generation today")

		// If not enough journeys in todays timetable then look into tomorrows
		if len(journeysTimetableToday) < query.Count {
			currentTime = time.Now()
			journeysTimetableTomorrow := ctdf.GenerateTimetableFromJourneys(journeys, query.Stop.PrimaryIdentifier, dayAfterDateTime, realtimeTimeframe, false)
			log.Debug().Str("Length", (time.Now().Sub(currentTime).String())).Msg("Timetable generation tomorrow")

			journeysTimetable = append(journeysTimetableToday, journeysTimetableTomorrow...)
		} else {
			journeysTimetable = journeysTimetableToday
		}

		// Sort timetable by TimetableRecord time
		sort.Slice(journeysTimetable, func(i, j int) bool {
			return journeysTimetable[i].Time.Before(journeysTimetable[j].Time)
		})

		// Once sorted cut off any records higher than our max count
		if len(journeysTimetable) > query.Count {
			journeysTimetable = journeysTimetable[:query.Count]
		}

		currentTime = time.Now()
		// Transforming the whole document is incredibly ineffecient
		// Instead just transform the Operator & Service as those are the key values
		for _, item := range journeysTimetable {
			transforms.Transform(item.Journey.Operator, 1)
			transforms.Transform(item.Journey.Service, 1)
		}
		log.Debug().Str("Length", (time.Now().Sub(currentTime).String())).Msg("Transform")

		return journeysTimetable, nil
	default:
		return nil, UnsupportedSourceError
	}
}
