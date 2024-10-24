package networkrailcorpus

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/dataimporter/datasets"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type Corpus struct {
	TiplocData []TiplocData `json:"TIPLOCDATA"`
}

type TiplocData struct {
	NLC        int
	STANOX     string
	TIPLOC     string
	ThreeAlpha string `json:"3ALPHA"`
	UIC        string
	NLCDESC    string
	NLCDESC16  string
}

func (c *Corpus) Import(dataset datasets.DataSet, datasource *ctdf.DataSource) error {
	if !dataset.SupportedObjects.Stops {
		return errors.New("This format requires stops to be enabled")
	}

	stopsCollection := database.GetCollection("stops_raw")

	var updateOperations []mongo.WriteModel

	for _, tiplocData := range c.TiplocData {
		tiploc := strings.TrimSpace(tiplocData.TIPLOC)
		stanox := strings.TrimSpace(tiplocData.STANOX)

		if tiploc == "" || stanox == "" {
			continue
		}

		var stop ctdf.Stop
		err := stopsCollection.FindOne(context.Background(), bson.M{"otheridentifiers": fmt.Sprintf("GB:TIPLOC:%s", tiploc)}).Decode(&stop)

		if err != nil {
			continue
		}

		bsonRep, _ := bson.Marshal(bson.M{"$push": bson.M{"otheridentifiers": fmt.Sprintf("GB:STANOX:%s", stanox)}})
		updateModel := mongo.NewUpdateOneModel()
		updateModel.SetFilter(bson.M{"primaryidentifier": stop.PrimaryIdentifier})
		updateModel.SetUpdate(bsonRep)
		updateModel.SetUpsert(true)

		updateOperations = append(updateOperations, updateModel)

		log.Info().
			Str("stop", stop.PrimaryIdentifier).
			Str("stanox", stanox).
			Msg("Added STANOX to Stop")
	}

	if len(updateOperations) > 0 {
		_, err := stopsCollection.BulkWrite(context.Background(), updateOperations, &options.BulkWriteOptions{})
		if err != nil {
			log.Fatal().Err(err).Msg("Failed to bulk write Stops")
		}
	}

	return nil
}
