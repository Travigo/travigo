package stats

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/ioutil"

	"cloud.google.com/go/storage"
	"github.com/britbus/britbus/pkg/ctdf"
	"github.com/britbus/britbus/pkg/database"
	"github.com/britbus/britbus/pkg/elastic_client"
	"github.com/britbus/britbus/pkg/transforms"
	"github.com/elastic/go-elasticsearch/v8/esapi"
	"github.com/rs/zerolog/log"
	"github.com/ulikunitz/xz"
	"go.mongodb.org/mongo-driver/bson"
	"google.golang.org/api/iterator"
)

type Indexer struct {
	CloudBucketName string

	operatorsCache map[string]*ctdf.Operator
	servicesCache  map[string]*ctdf.Service
}

func (i *Indexer) Perform() {
	i.operatorsCache = map[string]*ctdf.Operator{}
	i.servicesCache = map[string]*ctdf.Service{}

	client, err := storage.NewClient(context.Background())
	if err != nil {
		log.Fatal().Err(err).Msg("Could not create GCP storage client")
	}

	bucket := client.Bucket(i.CloudBucketName)

	objects := bucket.Objects(context.TODO(), nil)

	for {
		objectAttr, err := objects.Next()

		if err == iterator.Done {
			break
		}
		if err != nil {
			log.Error().Err(err).Msg("Failed to iterate over bucket")
		}

		log.Info().Msgf("Found bundle file %s", objectAttr.Name)

		bundleAlreadyIndexed := i.bundleIndexed(objectAttr.Name)

		if bundleAlreadyIndexed {
			log.Info().Msgf("Bundle file %s already indexed", objectAttr.Name)
		} else {
			object := bucket.Object(objectAttr.Name)
			storageReader, err := object.NewReader(context.Background())
			if err != nil {
				log.Error().Err(err).Msgf("Failed to get object %s", objectAttr.Name)
			}

			i.indexJourneysBundle(objectAttr.Name, storageReader)
		}
	}
}

func (i *Indexer) bundleIndexed(bundleName string) bool {
	var queryBytes bytes.Buffer
	query := map[string]interface{}{
		"query": map[string]interface{}{
			"term": map[string]interface{}{
				"BundleSourceFile.keyword": bundleName,
			},
		},
	}
	json.NewEncoder(&queryBytes).Encode(query)
	res, err := elastic_client.Client.Count(
		elastic_client.Client.Count.WithContext(context.Background()),
		elastic_client.Client.Count.WithIndex("journey-history-*"),
		elastic_client.Client.Count.WithBody(&queryBytes),
		elastic_client.Client.Count.WithPretty(),
	)

	if err != nil {
		log.Fatal().Err(err).Msg("Failed to query index")
	}

	responseBytes, _ := io.ReadAll(res.Body)
	var responseMap map[string]interface{}
	json.Unmarshal(responseBytes, &responseMap)

	if responseMap["status"] != nil {
		log.Fatal().Str("response", string(responseBytes)).Msg("Failed to query index")
	}

	if int(responseMap["count"].(float64)) > 0 {
		return true
	}

	return false
}

func (i *Indexer) indexJourneysBundle(bundleName string, file io.Reader) {
	log.Info().Msgf("Indexing bundle file %s", bundleName)

	xzReader, err := xz.NewReader(file)
	if err != nil {
		log.Error().Err(err).Msg("Failed to decompress xz file")
	}

	tarReader := tar.NewReader(xzReader)

	for {
		_, err := tarReader.Next()
		if err == io.EOF {
			break
		}

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
			BundleSourceFile: bundleName,

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
	}

	log.Info().Msgf("Bundle file %s indexing complete", bundleName)
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
