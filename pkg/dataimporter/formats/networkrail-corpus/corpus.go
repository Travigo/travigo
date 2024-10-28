package networkrailcorpus

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

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

	now := time.Now()

	stopsCollection := database.GetCollection("stops_raw")

	var updateOperations []mongo.WriteModel

	for _, tiplocData := range c.TiplocData {
		tiploc := strings.TrimSpace(tiplocData.TIPLOC)
		stanox := strings.TrimSpace(tiplocData.STANOX)
		threeAlpha := strings.TrimSpace(tiplocData.ThreeAlpha)

		if tiploc == "" || stanox == "" {
			continue
		}

		tiplocID := fmt.Sprintf("gb-tiploc-%s", tiploc)
		stanoxID := fmt.Sprintf("gb-stanox-%s", stanox)
		threeAlphaID := fmt.Sprintf("gb-crs-%s", threeAlpha)

		primaryID := fmt.Sprintf("travigo-internalmerge-%s-%s-%s", dataset.Identifier, tiploc, stanox)

		otherIDs := []string{stanoxID, tiplocID}
		if threeAlpha != "" {
			otherIDs = append(otherIDs, threeAlphaID)
		}

		bsonRep, _ := bson.Marshal(bson.M{"$set": bson.M{
			"primaryidentifier":    primaryID,
			"otheridentifiers":     otherIDs,
			"datasource":           datasource,
			"modificationdatetime": now,
			"creationdatetime":     now,
		}})
		updateModel := mongo.NewUpdateOneModel()
		updateModel.SetFilter(bson.M{"primaryidentifier": primaryID})
		updateModel.SetUpdate(bsonRep)
		updateModel.SetUpsert(true)

		updateOperations = append(updateOperations, updateModel)

		log.Info().
			Str("tiploc", tiplocID).
			Str("stanox", stanoxID).
			Msg("Added STANOX Stop")
	}

	if len(updateOperations) > 0 {
		_, err := stopsCollection.BulkWrite(context.Background(), updateOperations, &options.BulkWriteOptions{})
		if err != nil {
			log.Fatal().Err(err).Msg("Failed to bulk write Stops")
		}
	}

	return nil
}
