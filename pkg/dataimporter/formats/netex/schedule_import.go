package netex

import (
	"context"
	"fmt"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/dataimporter/datasets"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const scheduleWriteBatchSize = 1000

func writeScheduleRecords[T any](ctx context.Context, collection string, records []T, identifier func(T) string) error {
	if len(records) == 0 {
		return nil
	}
	pending := make([]mongo.WriteModel, 0, scheduleWriteBatchSize)
	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		if _, err := database.GetCollection(collection).BulkWrite(ctx, pending, options.BulkWrite().SetOrdered(false)); err != nil {
			return fmt.Errorf("write %s batch: %w", collection, err)
		}
		pending = pending[:0]
		return nil
	}
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return err
		}
		primaryIdentifier := identifier(record)
		if primaryIdentifier == "" {
			return fmt.Errorf("cannot write %s record without a primary identifier", collection)
		}
		document, err := bson.Marshal(record)
		if err != nil {
			return fmt.Errorf("marshal %s record %q: %w", collection, primaryIdentifier, err)
		}
		var fields bson.M
		if err := bson.Unmarshal(document, &fields); err != nil {
			return fmt.Errorf("decode %s record %q: %w", collection, primaryIdentifier, err)
		}
		created := fields["creationdatetime"]
		delete(fields, "creationdatetime")
		model := mongo.NewUpdateOneModel().
			SetFilter(bson.M{"primaryidentifier": primaryIdentifier}).
			SetUpdate(mongo.Pipeline{{{Key: "$replaceWith", Value: bson.M{"$mergeObjects": bson.A{
				fields,
				bson.M{"_id": "$_id"},
				bson.M{"creationdatetime": bson.M{"$ifNull": bson.A{"$creationdatetime", created}}},
			}}}}}).
			SetUpsert(true)
		pending = append(pending, model)
		if len(pending) >= scheduleWriteBatchSize {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	return flush()
}

// Import converts and persists one parsed NeTEx schedule snapshot. Every enabled
// object family is converted and validated before the first collection is written.
func (schedule *Schedule) Import(dataset datasets.DataSet, datasource *ctdf.DataSourceReference) (datasets.DataImportReport, error) {
	if datasource == nil {
		return datasets.DataImportReport{}, fmt.Errorf("NeTEx schedule import requires a datasource reference")
	}
	if dataset.SupportedObjects.OperatorGroups || dataset.SupportedObjects.Fares ||
		dataset.SupportedObjects.RealtimeJourneys || dataset.SupportedObjects.ServiceAlerts ||
		dataset.SupportedObjects.JourneyTracks {
		return datasets.DataImportReport{}, fmt.Errorf("NeTEx schedule importer does not provide operator groups, fares, realtime data, alerts, or journey tracks")
	}
	datasource.OriginalFormat = string(datasets.DataSetFormatNetExSchedule)
	ctx := datasource.GetImportContext()
	if err := ctx.Err(); err != nil {
		return datasets.DataImportReport{}, err
	}
	now := time.Now()
	var stops []*ctdf.Stop
	var groups []*ctdf.StopGroup
	var operators []*ctdf.Operator
	var services []*ctdf.Service
	var journeys []*ctdf.Journey
	if dataset.SupportedObjects.Stops || dataset.SupportedObjects.StopGroups {
		convertedStops, convertedGroups, err := schedule.convertStops(dataset, datasource, now)
		if err != nil {
			return datasets.DataImportReport{}, fmt.Errorf("convert NeTEx stops: %w", err)
		}
		if dataset.SupportedObjects.Stops {
			stops = convertedStops
			if len(stops) == 0 {
				return datasets.DataImportReport{}, fmt.Errorf("NeTEx dataset advertises stops but produced none")
			}
		}
		if dataset.SupportedObjects.StopGroups {
			groups = convertedGroups
			if len(groups) == 0 {
				return datasets.DataImportReport{}, fmt.Errorf("NeTEx dataset advertises stop groups but produced none")
			}
		}
	}
	if dataset.SupportedObjects.Operators || dataset.SupportedObjects.Services || dataset.SupportedObjects.Journeys {
		convertedOperators, convertedServices, convertedJourneys, err := schedule.convertTimetable(dataset, datasource, now)
		if err != nil {
			return datasets.DataImportReport{}, fmt.Errorf("convert NeTEx timetable: %w", err)
		}
		if dataset.SupportedObjects.Operators {
			operators = convertedOperators
			if len(operators) == 0 {
				return datasets.DataImportReport{}, fmt.Errorf("NeTEx dataset advertises operators but produced none")
			}
		}
		if dataset.SupportedObjects.Services {
			services = convertedServices
			if len(services) == 0 {
				return datasets.DataImportReport{}, fmt.Errorf("NeTEx dataset advertises services but produced none")
			}
		}
		if dataset.SupportedObjects.Journeys {
			journeys = convertedJourneys
			if len(journeys) == 0 {
				return datasets.DataImportReport{}, fmt.Errorf("NeTEx dataset advertises journeys but produced none")
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return datasets.DataImportReport{}, err
	}
	if dataset.SupportedObjects.Stops {
		if err := writeScheduleRecords(ctx, "stops_raw", stops, func(value *ctdf.Stop) string { return value.PrimaryIdentifier }); err != nil {
			return datasets.DataImportReport{}, err
		}
	}
	if dataset.SupportedObjects.StopGroups {
		if err := writeScheduleRecords(ctx, "stop_groups", groups, func(value *ctdf.StopGroup) string { return value.PrimaryIdentifier }); err != nil {
			return datasets.DataImportReport{}, err
		}
	}
	if dataset.SupportedObjects.Operators {
		if err := writeScheduleRecords(ctx, "operators", operators, func(value *ctdf.Operator) string { return value.PrimaryIdentifier }); err != nil {
			return datasets.DataImportReport{}, err
		}
	}
	if dataset.SupportedObjects.Services {
		if err := writeScheduleRecords(ctx, "services_raw", services, func(value *ctdf.Service) string { return value.PrimaryIdentifier }); err != nil {
			return datasets.DataImportReport{}, err
		}
	}
	if dataset.SupportedObjects.Journeys {
		if err := writeScheduleRecords(ctx, database.JourneysRawCollectionName, journeys, func(value *ctdf.Journey) string { return value.PrimaryIdentifier }); err != nil {
			return datasets.DataImportReport{}, err
		}
	}
	return datasets.DataImportReport{
		ImportedStops:      len(stops),
		ImportedStopGroups: len(groups),
		ImportedOperators:  len(operators),
		ImportedServices:   len(services),
		ImportedJourneys:   len(journeys),
	}, nil
}
