package manager

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func TestValidateRailFareLegRulesBatchesOfferReferences(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	for _, missing := range []bool{false, true} {
		mt.Run(fmt.Sprintf("missing final offer %t", missing), func(mt *mtest.T) {
			old := database.Instance
			database.Instance = &database.MongoInstance{Client: mt.Client, Database: mt.DB}
			mt.Cleanup(func() { database.Instance = old })
			const dataset, version = "gb-nationalrail-fares", "v1"
			var rules, firstMatches []bson.D
			for i := range 2001 {
				ref := fmt.Sprintf("offer-%d", i)
				rule := bson.D{
					{Key: "primaryidentifier", Value: fmt.Sprintf("rule-%d", i)},
					{Key: "datasetid", Value: dataset}, {Key: "fareversionid", Value: version},
					{Key: "productref", Value: "product"}, {Key: "offerref", Value: ref},
				}
				rules = append(rules, rule)
				if i == 0 {
					rules = append(rules, append(bson.D{{Key: "primaryidentifier", Value: "rule-duplicate"}}, rule[1:]...))
				}
				if i < 2000 {
					firstMatches = append(firstMatches, bson.D{{Key: "primaryidentifier", Value: ref}})
				}
			}
			var lastMatch []bson.D
			if !missing {
				lastMatch = []bson.D{{{Key: "primaryidentifier", Value: "offer-2000"}}}
			}
			mt.AddMockResponses(
				mtest.CreateCursorResponse(0, mt.DB.Name()+".fare_leg_rules", mtest.FirstBatch, rules...),
				mtest.CreateCursorResponse(0, mt.DB.Name()+".fare_offers", mtest.FirstBatch, firstMatches...),
				mtest.CreateCursorResponse(0, mt.DB.Name()+".fare_offers", mtest.FirstBatch, lastMatch...),
			)
			err := validateRailFareLegRules(context.Background(), bson.M{"datasetid": dataset, "fareversionid": version}, dataset, version)
			if missing {
				if err == nil || !strings.Contains(err.Error(), "offer-2000") || !strings.Contains(err.Error(), "rule-2000") {
					mt.Fatalf("expected missing reference and source, got %v", err)
				}
			} else if err != nil {
				mt.Fatal(err)
			}
			events := mt.GetAllStartedEvents()
			if len(events) != 3 {
				mt.Fatalf("got %d commands, want one rule scan and two offer reads", len(events))
			}
			for i, size := range []int{2000, 1} {
				filter := events[i+1].Command.Lookup("filter").Document()
				refs, err := filter.Lookup("primaryidentifier", "$in").Array().Values()
				if err != nil || len(refs) != size || filter.Lookup("datasetid").StringValue() != dataset || filter.Lookup("fareversionid").StringValue() != version {
					mt.Fatalf("unexpected offer query scope or size: %s, %s, %d", filter.Lookup("datasetid"), filter.Lookup("fareversionid"), len(refs))
				}
				seen := make(map[string]bool, size)
				for _, ref := range refs {
					if id := ref.StringValue(); seen[id] {
						mt.Fatalf("duplicate ID %q in offer query", id)
					} else {
						seen[id] = true
					}
				}
			}
		})
	}
}

func TestCheckFareReferenceReportsMissingGroupedScalar(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("grouped product", func(mt *mtest.T) {
		old := database.Instance
		database.Instance = &database.MongoInstance{Client: mt.Client, Database: mt.DB}
		mt.Cleanup(func() { database.Instance = old })
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+".fare_leg_rules", mtest.FirstBatch,
			bson.D{{Key: "primaryidentifier", Value: "rule-1"}, {Key: "productref", Value: "missing-product"}},
		))
		err := checkFareReference(context.Background(), "fare_leg_rules", bson.M{"datasetid": "gb-nationalrail-fares", "fareversionid": "v1"}, "productref", false, "fare_products", "primaryidentifier")
		if err == nil || !strings.Contains(err.Error(), "rule-1") || !strings.Contains(err.Error(), "missing-product") {
			mt.Fatalf("expected missing scalar and source, got %v", err)
		}
	})
}
