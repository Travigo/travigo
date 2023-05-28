package realtime

import (
	"archive/tar"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/transforms"
	"github.com/ulikunitz/xz"
	"go.mongodb.org/mongo-driver/bson"
)

type Archiver struct {
	OutputDirectory     string
	WriteIndividualFile bool
	WriteBundle         bool
	CloudUpload         bool
	CloudBucketName     string

	currentTime time.Time
	tarWriter   *tar.Writer
}

func (a *Archiver) Perform() {
	log.Info().Interface("archiver", a).Msg("Running Archive process")

	currentTime := time.Now()
	a.currentTime = currentTime

	cutOffTime := currentTime.Add(-2 * time.Hour)

	bundleFilename := fmt.Sprintf("%s.tar.xz", currentTime.Format(time.RFC3339))

	var xzWriter *xz.Writer

	if a.WriteBundle {
		bundleFile, err := os.Create(path.Join(a.OutputDirectory, bundleFilename))
		if err != nil {
			log.Error().Err(err).Msg("Failed to open file")
		}

		xzWriter, _ = xz.NewWriter(bundleFile)
		a.tarWriter = tar.NewWriter(xzWriter)
	}

	// Keep a record of all the stops at this point in time
	log.Info().Msg("Writing stops")
	stopsCollection := database.GetCollection("stops")
	cursor, err := stopsCollection.Find(context.Background(), bson.M{})
	var stops []*ctdf.Stop

	for cursor.Next(context.TODO()) {
		var stop ctdf.Stop
		err := cursor.Decode(&stop)
		if err != nil {
			log.Error().Err(err).Msg("Failed to decode Stop")
		}

		transforms.Transform(&stop, 2)

		stops = append(stops, &stop)
	}
	stopsJSON, err := json.Marshal(stops)
	if err != nil {
		log.Error().Err(err).Msg("Error converting stops to json")
	}
	a.writeFile("stops.json", stopsJSON)

	// Keep a record of all the services at this point in time
	log.Info().Msg("Writing services")
	servicesCollection := database.GetCollection("services")
	cursor, _ = servicesCollection.Find(context.Background(), bson.M{})
	var services []*ctdf.Service

	for cursor.Next(context.TODO()) {
		var service ctdf.Service
		err := cursor.Decode(&service)
		if err != nil {
			log.Error().Err(err).Msg("Failed to decode Service")
		}

		transforms.Transform(&service, 2)

		services = append(services, &service)
	}
	servicesJSON, err := json.Marshal(services)
	if err != nil {
		log.Error().Err(err).Msg("Error converting services to json")
	}
	a.writeFile("services.json", servicesJSON)

	// Keep a record of all the operators at this point in time
	log.Info().Msg("Writing operators")
	operatorsCollection := database.GetCollection("operators")
	cursor, _ = operatorsCollection.Find(context.Background(), bson.M{})
	var operators []*ctdf.Operator

	for cursor.Next(context.TODO()) {
		var operator ctdf.Operator
		err := cursor.Decode(&operator)
		if err != nil {
			log.Error().Err(err).Msg("Failed to decode Operator")
		}

		transforms.Transform(&operator, 2)

		operators = append(operators, &operator)
	}
	operatorsJSON, err := json.Marshal(operators)
	if err != nil {
		log.Error().Err(err).Msg("Error converting operators to json")
	}
	a.writeFile("operators.json", operatorsJSON)

	// Keep a record of all the operator groups at this point in time
	log.Info().Msg("Writing operator groups")
	operatorGroupsCollection := database.GetCollection("operator_groups")
	cursor, _ = operatorGroupsCollection.Find(context.Background(), bson.M{})
	var operatorGroups []*ctdf.OperatorGroup

	for cursor.Next(context.TODO()) {
		var operatorGroup ctdf.OperatorGroup
		err := cursor.Decode(&operatorGroup)
		if err != nil {
			log.Error().Err(err).Msg("Failed to decode OperatorGroup")
		}

		transforms.Transform(&operatorGroup, 2)

		operatorGroups = append(operatorGroups, &operatorGroup)
	}
	operatorGroupsJSON, err := json.Marshal(operatorGroups)
	if err != nil {
		log.Error().Err(err).Msg("Error converting operator groups to json")
	}
	a.writeFile("operator_groups.json", operatorGroupsJSON)

	// Write all the journeys
	log.Info().Msgf("Archiving realtime journeys older than %s", cutOffTime)

	realtimeJourneysCollection := database.GetCollection("realtime_journeys")
	searchFilter := bson.M{"modificationdatetime": bson.M{"$lt": cutOffTime}}
	cursor, _ = realtimeJourneysCollection.Find(context.Background(), searchFilter)

	recordCount := 0

	for cursor.Next(context.TODO()) {
		var realtimeJourney ctdf.RealtimeJourney
		err := cursor.Decode(&realtimeJourney)
		if err != nil {
			log.Error().Err(err).Msg("Failed to decode RealtimeJourney")
		}

		realtimeJourney.GetJourney()
		realtimeJourney.Journey.GetService()

		archivedJourney := a.convertRealtimeToArchiveJourney(&realtimeJourney)
		archivedJourneyJSON, err := json.Marshal(archivedJourney)
		if err != nil {
			log.Error().Err(err).Msg("Error converting archive to json")
		}

		filename := strings.ReplaceAll(fmt.Sprintf("%s.json", archivedJourney.PrimaryIdentifier), "/", "_")

		a.writeFile(filename, archivedJourneyJSON)

		recordCount += 1
	}

	log.Info().Int("recordCount", recordCount).Msg("Journeys archive document generation complete")

	if a.WriteBundle {
		a.tarWriter.Close()
		xzWriter.Close()
	}

	if a.CloudUpload {
		a.uploadToStorage(bundleFilename)
	}

	realtimeJourneysCollection.DeleteMany(context.Background(), searchFilter)

	database.Instance.Database.RunCommand(context.Background(), bson.M{"compact": "realtime_journeys"})
}

func (a *Archiver) writeFile(filename string, contents []byte) {
	if a.WriteIndividualFile {
		file, err := os.Create(path.Join(a.OutputDirectory, filename))
		if err != nil {
			log.Error().Err(err).Msg("Failed to open file")
		}

		_, err = file.Write(contents)
		if err != nil {
			log.Error().Err(err).Msg("Failed to write to file")
		}

		file.Close()
	}

	if a.WriteBundle {
		memoryFileInfo := MemoryFileInfo{
			MfiName:    filename,
			MfiSize:    int64(len(contents)),
			MfiMode:    777,
			MfiModTime: a.currentTime,
			MfiIsDir:   false,
		}

		header, err := tar.FileInfoHeader(memoryFileInfo, filename)
		if err != nil {
			log.Error().Err(err).Msg("Failed to generate tar header")
		}

		// Write file header to the tar archive
		err = a.tarWriter.WriteHeader(header)
		if err != nil {
			log.Error().Err(err).Msg("Failed to write tar header")
		}

		_, err = a.tarWriter.Write(contents)
		if err != nil {
			log.Error().Err(err).Msg("Failed to write to file")
		}
	}
}

func (a *Archiver) uploadToStorage(filename string) {
	fullBundlePath := path.Join(a.OutputDirectory, filename)

	client, err := storage.NewClient(context.Background())
	if err != nil {
		log.Fatal().Err(err).Msg("Could not create GCP storage client")
	}

	bucket := client.Bucket(a.CloudBucketName)
	object := bucket.Object(filename)

	reader, err := os.Open(fullBundlePath)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to open file")
	}
	defer reader.Close()

	writer := object.NewWriter(context.Background())

	io.Copy(writer, reader)

	err = writer.Close()

	if err == nil {
		log.Info().Msgf("Written file %s to bucket %s", object.ObjectName(), object.BucketName())
	} else {
		log.Fatal().Err(err).Msg("Failed to write file to GCP")
	}
}

func (a *Archiver) convertRealtimeToArchiveJourney(realtimeJourney *ctdf.RealtimeJourney) *ctdf.ArchivedJourney {
	var archivedStops []*ctdf.ArchivedJourneyStops

	stops, arrivalTimes, departureTimes := realtimeJourney.Journey.FlattenStops()

	for _, stopRef := range stops {
		realtimeJourneyStop := realtimeJourney.Stops[stopRef]

		archivedJourneyStop := ctdf.ArchivedJourneyStops{
			StopRef: stopRef,

			ExpectedArrivalTime:   arrivalTimes[stopRef],
			ExpectedDepartureTime: departureTimes[stopRef],

			HasActualData: false,
			Offset:        0,
		}

		if realtimeJourneyStop != nil {
			archivedJourneyStop.HasActualData = true
			archivedJourneyStop.ActualArrivalTime = realtimeJourneyStop.ArrivalTime
			archivedJourneyStop.ActualDepartureTime = realtimeJourneyStop.DepartureTime

			archivedJourneyStop.Offset = int(archivedJourneyStop.ActualArrivalTime.Sub(realtimeJourneyStop.ArrivalTime).Seconds())
		}

		archivedStops = append(archivedStops, &archivedJourneyStop)
	}

	archived := &ctdf.ArchivedJourney{
		PrimaryIdentifier: realtimeJourney.PrimaryIdentifier,

		JourneyRef: realtimeJourney.JourneyRef,

		ServiceRef: realtimeJourney.Journey.ServiceRef,

		OperatorRef: realtimeJourney.Journey.OperatorRef,

		CreationDateTime:     realtimeJourney.CreationDateTime,
		ModificationDateTime: realtimeJourney.ModificationDateTime,

		DataSource: realtimeJourney.DataSource,

		Stops: archivedStops,

		Reliability: realtimeJourney.Reliability,

		VehicleRef: realtimeJourney.VehicleRef,
	}

	return archived
}
