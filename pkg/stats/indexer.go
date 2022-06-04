package stats

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"time"

	"github.com/britbus/britbus/pkg/ctdf"
	"github.com/britbus/britbus/pkg/database"
	"github.com/britbus/britbus/pkg/elastic_client"
	"github.com/britbus/britbus/pkg/transforms"
	"github.com/elastic/go-elasticsearch/v8/esapi"
	"github.com/rs/zerolog/log"
	"github.com/ulikunitz/xz"
	"go.mongodb.org/mongo-driver/bson"
)

type Indexer struct {
	CloudBucketName string

	operatorsCache map[string]*ctdf.Operator
	servicesCache  map[string]*ctdf.Service
}

func (i *Indexer) Perform() {
	i.operatorsCache = map[string]*ctdf.Operator{}
	i.servicesCache = map[string]*ctdf.Service{}

	bundleName := "./test/output/2022-06-02T22:37:19+01:00.tar.xz"
	file, _ := os.Open(bundleName)
	defer file.Close()

	i.indexJourneysBundle(bundleName, file)
}

func (i *Indexer) indexJourneysBundle(bundleName string, file io.Reader) {
	xzReader, err := xz.NewReader(file)
	if err != nil {
		log.Error().Err(err).Msg("Failed to decompress xz file")
	}

	tarReader := tar.NewReader(xzReader)

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}

		log.Info().Msg(header.Name)

		fileBytes, _ := ioutil.ReadAll(tarReader)

		var ctdfArchivedJourney *ctdf.ArchivedJourney
		err = json.Unmarshal(fileBytes, &ctdfArchivedJourney)
		if err != nil {
			log.Error().Err(err).Msg("Failed to decode JSON file")
		}

		operator := i.getOperator(ctdfArchivedJourney.OperatorRef)
		transforms.Transform(operator)

		// Convert to the extended stats Archived Journey form
		archivedJourney := ArchivedJourney{
			ArchivedJourney:  *ctdfArchivedJourney,
			BundleSourceFile: fmt.Sprintf("%s/%s", bundleName, header.Name),

			PrimaryOperatorRef: operator.PrimaryIdentifier,
			OperatorGroupRef:   operator.OperatorGroupRef,

			Regions: operator.Regions,
		}

		archivedJourneyBytes, _ := json.Marshal(archivedJourney)

		elastic_client.IndexRequest(&esapi.IndexRequest{
			Index:   "journey-history-1",
			Body:    bytes.NewReader(archivedJourneyBytes),
			Refresh: "true",
		})

		time.Sleep(25 * time.Millisecond)
	}
}

func (i *Indexer) getOperator(identifier string) *ctdf.Operator {
	if i.operatorsCache[identifier] == nil {
		operatorsCollection := database.GetCollection("operators")
		var operator *ctdf.Operator
		operatorsCollection.FindOne(context.Background(), bson.M{"$or": bson.A{
			bson.M{"primaryidentifier": identifier},
			bson.M{"otheridentifiers": identifier},
		}}).Decode(&operator)

		i.operatorsCache[identifier] = operator

		return operator
	} else {
		return i.operatorsCache[identifier]
	}
}

func (i *Indexer) getService(identifier string) *ctdf.Service {
	if i.servicesCache[identifier] == nil {
		servicesCollection := database.GetCollection("services")
		var service *ctdf.Service
		servicesCollection.FindOne(context.Background(), bson.M{"primaryidentifier": identifier}).Decode(&service)

		i.servicesCache[identifier] = service

		return service
	} else {
		return i.servicesCache[identifier]
	}
}
