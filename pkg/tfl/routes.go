package tfl

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/britbus/britbus/pkg/ctdf"
	"github.com/britbus/britbus/pkg/database"
	"github.com/kr/pretty"
	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const ServiceIDFormat = "GB:TFL:SERVICE:%s"

type RouteAPI struct {
	Lines    []*Line
	Services []*ctdf.Service

	Journeys []*ctdf.Journey
}

type Line struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ModeName string `json:"modeName"`
	//Disruptions string `json:"disruptions"`
	Created       string          `json:"created"`
	Modified      string          `json:"modified"`
	RouteSections []RouteSections `json:"routeSections"`

	OutboundSequence *Sequence
	InboundSequence  *Sequence
	// lineStatuses

	// serviceStatuses
}

func (l *Line) GetSequence() error {
	l.OutboundSequence = &Sequence{}
	err := l.OutboundSequence.Get(l.ID, "outbound")
	if err != nil {
		return err
	}

	l.InboundSequence = &Sequence{}
	err = l.InboundSequence.Get(l.ID, "inbound")
	if err != nil {
		return err
	}

	return nil
}

type RouteSections struct {
	Name            string `json:"name"`
	Direction       string `json:"direction"`
	OriginationName string `json:"originationName"`
	DestinationName string `json:"destinationName"`
	Originator      string `json:"originator"`
	Destination     string `json:"destination"`
	ServiceType     string `json:"serviceType"`
	ValidTo         string `json:"validTo"`
	ValidFrom       string `json:"validFrom"`
}

func (r *RouteAPI) ParseJSON(reader io.Reader, datasource ctdf.DataSource) error {
	datasource.Dataset = "Line/Route"

	bytes, err := io.ReadAll(reader)

	if err != nil {
		return err
	}

	var tflLines []Line
	json.Unmarshal(bytes, &tflLines)

	for _, line := range tflLines {
		creationDateTime, err := time.Parse(time.RFC3339, line.Created)
		if err != nil {
			return err
		}
		modifiedDateTime, err := time.Parse(time.RFC3339, line.Modified)
		if err != nil {
			return err
		}

		routes := []ctdf.Route{}

		for _, tflRoute := range line.RouteSections {
			routes = append(routes, ctdf.Route{
				Description: tflRoute.Name,
			})
		}

		if len(line.RouteSections) > 2 {
			pretty.Println(line.ID, line.Name, len(line.RouteSections))
		}

		r.Lines = append(r.Lines, &line)
		r.Services = append(r.Services, &ctdf.Service{
			PrimaryIdentifier: fmt.Sprintf(ServiceIDFormat, line.ID),
			OtherIdentifiers: map[string]string{
				"LineID": line.ID,
			},

			CreationDateTime:     creationDateTime,
			ModificationDateTime: modifiedDateTime,

			DataSource: &datasource,

			ServiceName: line.Name,

			OperatorRef: "GB:NOC:TFLO",

			Routes: routes,

			OutboundDescription: &ctdf.ServiceDescription{},
			InboundDescription:  &ctdf.ServiceDescription{},
		})
	}

	log.Info().Int("services", len(r.Services)).Msgf("Extracted CTDF Services from TfL API")

	return nil
}

func (r *RouteAPI) ImportIntoMongoAsCTDF() {
	log.Info().Msg("Converting & Importing TFL Routes as CTDF Services into Mongo")
	serviceOperations := []mongo.WriteModel{}
	var serviceOperationInsert uint64
	var serviceOperationUpdate uint64

	servicesCollection := database.GetCollection("services")

	for _, service := range r.Services {
		// Check if we want to add this service to the list of MongoDB operations
		bsonRep, _ := bson.Marshal(service)

		var existingCtdfService *ctdf.Service
		servicesCollection.FindOne(context.Background(), bson.M{"primaryidentifier": service.PrimaryIdentifier}).Decode(&existingCtdfService)

		if existingCtdfService == nil {
			insertModel := mongo.NewInsertOneModel()
			insertModel.SetDocument(bsonRep)

			serviceOperations = append(serviceOperations, insertModel)
			serviceOperationInsert += 1
		} else if existingCtdfService.ModificationDateTime.Before(service.ModificationDateTime) || existingCtdfService.ModificationDateTime.Year() == 0 {
			updateModel := mongo.NewReplaceOneModel()
			updateModel.SetFilter(bson.M{"primaryidentifier": service.PrimaryIdentifier})
			updateModel.SetReplacement(bsonRep)

			serviceOperations = append(serviceOperations, updateModel)
			serviceOperationUpdate += 1
		}
	}

	log.Info().Uint64("writes", serviceOperationInsert).Uint64("updates", serviceOperationUpdate).Msg("Written Services changes to database")

	if len(serviceOperations) > 0 {
		_, err := servicesCollection.BulkWrite(context.TODO(), serviceOperations, &options.BulkWriteOptions{})
		if err != nil {
			log.Fatal().Err(err).Msg("Failed to bulk write Services")
		}
	}
}

func (line *Line) GenerateCTDFJourneys(datasource ctdf.DataSource) error {
	datasource.Dataset = "Line/Route/Sequence,Line/Timetable"

	// TODO handle both inbound & outbound
	sequence := line.OutboundSequence

	for _, stopPointSequence := range sequence.StopPointSequences {
		baseJourney := ctdf.Journey{
			PrimaryIdentifier: fmt.Sprintf("%s:%s:%s:%d", "GB:NOC:TFLO", line.ID, sequence.Direction, stopPointSequence.BranchID),
			OtherIdentifiers: map[string]string{
				"LineID":   line.ID,
				"BranchID": fmt.Sprint(stopPointSequence.BranchID),
			},

			CreationDateTime:     time.Now(),
			ModificationDateTime: time.Now(),

			DataSource: &datasource,

			ServiceRef:         fmt.Sprintf(ServiceIDFormat, line.ID),
			OperatorRef:        "GB:NOC:TFLO",
			Direction:          sequence.Direction,
			DepartureTime:      time.Date(0, 1, 1, 23, 59, 59, 0, time.UTC),
			DestinationDisplay: line.Name,

			Availability: nil,

			Path: []*ctdf.JourneyPathItem{},
		}

		pretty.Println(baseJourney)
	}

	return nil
}
