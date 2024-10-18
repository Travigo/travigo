package main

import (
	"context"
	"fmt"

	"github.com/kr/pretty"
	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
)

func main() {
	ctx := context.Background()
	dbUri := "neo4j://localhost"
	dbUser := "neo4j"
	dbPassword := "neo4jneo4j"
	driver, err := neo4j.NewDriverWithContext(
		dbUri,
		neo4j.BasicAuth(dbUser, dbPassword, ""))
	defer driver.Close(ctx)

	err = driver.VerifyConnectivity(ctx)
	if err != nil {
		panic(err)
	}

	pretty.Println("testing")

	database.Connect()

	session := driver.NewSession(ctx, neo4j.SessionConfig{DatabaseName: "neo4j"})
	defer session.Close(ctx)

	// stops
	ctdfStops := []ctdf.Stop{}
	collection := database.GetCollection("stops")
	cursor, _ := collection.Find(context.Background(), bson.M{"primaryidentifier": bson.M{"$in": []string{
		"GB:ATCO:9100CAMBDGE",
		"GB:ATCO:9100ROYSTON",
		"GB:ATCO:9100ASHWELC",
		"GB:ATCO:9100LTCE",
		"GB:ATCO:9100BALDOCK",
		"GB:ATCO:9100HITCHIN",
		"GB:ATCO:9100STEVNGE",
		"GB:ATCO:9100FNPK",
		"GB:ATCO:9100STPXBOX",
		"GB:ATCO:9100FRNDNLT",
		"GB:ATCO:9100BLFR",
		"GB:ATCO:9100LNDNBDE",
		"GB:ATCO:9100ECROYDN",
		"GB:ATCO:9100GTWK",
		"GB:ATCO:9100THBDGS",
		"GB:ATCO:9100HYWRDSH",
		"GB:ATCO:9100BURGESH",
		"GB:ATCO:9100BRGHTN",

		"GB:ATCO:9100RAINHMK",
		"GB:ATCO:9100GLNGHMK",
		"GB:ATCO:9100CHATHAM",
		"GB:ATCO:9100RCHT",
		"GB:ATCO:9100STROOD",
		"GB:ATCO:9100HIGM",
		"GB:ATCO:9100GRVSEND",
		"GB:ATCO:9100NRTHFLT",
		"GB:ATCO:9100SWNSCMB",
		"GB:ATCO:9100GNHT",
		"GB:ATCO:9100STCR",
		"GB:ATCO:9100DARTFD",
		"GB:ATCO:9100SLADEGN",
		"GB:ATCO:9100ABWD",
		"GB:ATCO:9100PLMS",
		"GB:ATCO:9100WOLWCHA",
		"GB:ATCO:9100CRLN",
		"GB:ATCO:9100WCOMBEP",
		"GB:ATCO:9100MAZEH",
		"GB:ATCO:9100GNWH",
		"GB:ATCO:9100DEPTFD",
		"GB:ATCO:9100KNTSHTN",
	}}})
	cursor.All(ctx, &ctdfStops)
	// var ctdfJourney []ctdf.Journey

	_, err = session.Run(ctx, "match (a) -[r] -> () delete a, r", map[string]any{})
	_, err = session.Run(ctx, "match (a) delete a", map[string]any{})

	for _, stop := range ctdfStops {
		pretty.Println(stop.PrimaryIdentifier)

		_, err := session.ExecuteWrite(ctx,
			func(tx neo4j.ManagedTransaction) (any, error) {
				_, err := tx.Run(
					ctx,
					"CREATE (s:Stop {primaryidentifier: $primaryidentifier, primaryname: $primaryname})",
					map[string]any{
						"primaryidentifier": stop.PrimaryIdentifier,
						"primaryname":       stop.PrimaryName,
					})
				if err != nil {
					return nil, err
				}

				// Return the Organization ID to which the new Person ends up in
				return nil, nil
			})

		pretty.Println(err)
	}

	// journeys
	ctdfJourneys := []ctdf.Journey{}
	journeysSollection := database.GetCollection("journeys")
	cursor, _ = journeysSollection.Find(context.Background(), bson.M{"primaryidentifier": bson.M{"$in": []string{
		"GB:RAIL:G54460:240603:P",
		"GB:RAIL:G54374:240603:P",
	}}})
	cursor.All(ctx, &ctdfJourneys)

	for _, journey := range ctdfJourneys {
		pretty.Println(journey.PrimaryIdentifier)

		_, err := session.ExecuteWrite(ctx,
			func(tx neo4j.ManagedTransaction) (any, error) {
				_, err := tx.Run(
					ctx,
					"CREATE (j:Journey {primaryidentifier: $primaryidentifier, destinationdisplay: $destinationdisplay})",
					map[string]any{
						"primaryidentifier":  journey.PrimaryIdentifier,
						"destinationdisplay": journey.DestinationDisplay,
					})
				if err != nil {
					return nil, err
				}

				for _, path := range journey.Path {
					jpiID := fmt.Sprintf("%s:%s:%s", journey.PrimaryIdentifier, path.OriginStopRef, path.DestinationStopRef)
					// MERGE (i)-[:ORIGIN]->(o)
					// MERGE (i)-[:DESTINATION]->(d)
					// MERGE (i)-[:JOURNEY]->(j)
					// MATCH (o:Stop {primaryidentifier: $originstop})
					// MATCH (d:Stop {primaryidentifier: $originstop})
					// MATCH (j:Journey {primaryidentifier: $journey})
					pretty.Println(path.OriginStopRef, path.DestinationStopRef)
					_, err := tx.Run(
						ctx,
						`
						CREATE (i:JourneyPathItem {id: $id, originstop: $originstop, destinationstop: $destinationstop, journey: $journey})
						`,
						map[string]any{
							"id":              jpiID,
							"originstop":      path.OriginStopRef,
							"destinationstop": path.DestinationStopRef,
							"journey":         journey.PrimaryIdentifier,
						})
					if err != nil {
						return nil, err
					}

					_, err = tx.Run(
						ctx, `
						MATCH (i:JourneyPathItem {id: $jpiID})
						MATCH (j:Journey {primaryidentifier: $journey})
						CREATE (i)-[:JOURNEY]->(j)
						`, map[string]any{
							"jpiID":   jpiID,
							"journey": journey.PrimaryIdentifier,
						})
					if err != nil {
						return nil, err
					}

					_, err = tx.Run(
						ctx, `
						MATCH (o:Stop {primaryidentifier: $originstop})
						MATCH (i:JourneyPathItem {id: $jpiID})
						CREATE (i)-[:ORIGIN]->(o)
						`, map[string]any{
							"jpiID":      jpiID,
							"originstop": path.OriginStopRef,
						})
					if err != nil {
						return nil, err
					}

					_, err = tx.Run(
						ctx, `
						MATCH (d:Stop {primaryidentifier: $destinationstop})
						MATCH (i:JourneyPathItem {id: $jpiID})
						CREATE (i)-[:DESTINATION]->(d)
						`, map[string]any{
							"jpiID":           jpiID,
							"destinationstop": path.DestinationStopRef,
						})
					if err != nil {
						return nil, err
					}
				}

				// Return the Organization ID to which the new Person ends up in
				return nil, nil
			})

		pretty.Println(err)
	}
}
