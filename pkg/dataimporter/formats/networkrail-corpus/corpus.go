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

func (c *Corpus) Import(dataset datasets.DataSet, datasource *ctdf.DataSourceReference) error {
	if !dataset.SupportedObjects.Stops {
		return errors.New("This format requires stops to be enabled")
	}

	now := time.Now()

	stopsCollection := database.GetCollection("stops_raw")

	// PERF(low-risk): pre-size the write-model slice (one model per TIPLOC at most)
	// to avoid repeated slice growth/reallocation during append.
	updateOperations := make([]mongo.WriteModel, 0, len(c.TiplocData))

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

		// PERF(low-risk): per-record log.Info() (~2500 lines per import) replaced with
		// a single summary log.Info() after the loop. Use Debug for per-record detail.
		log.Debug().
			Str("tiploc", tiplocID).
			Str("stanox", stanoxID).
			Msg("Added STANOX Stop")
	}

	// PERF(low-risk): single summary log instead of one Info line per TIPLOC.
	log.Info().Int("count", len(updateOperations)).Msg("Prepared STANOX Stops")

	if len(updateOperations) > 0 {
		_, err := stopsCollection.BulkWrite(context.Background(), updateOperations, &options.BulkWriteOptions{})
		if err != nil {
			log.Fatal().Err(err).Msg("Failed to bulk write Stops")
		}
	}

	return nil
}
