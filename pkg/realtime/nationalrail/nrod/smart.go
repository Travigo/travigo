package nrod

import (
	"context"
	"errors"
	"strings"

	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/railtd"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

type smartReportingPoint struct {
	railtd.SMARTLocation
	arrival bool
}

func (t *tdTracker) loadSMART(ctx context.Context) error {
	var snapshot railtd.SMARTSnapshot
	err := database.GetCollection(railtd.SMARTCollection).FindOne(ctx, bson.M{"_id": railtd.SMARTSnapshotID}).Decode(&snapshot)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil
	}
	if err != nil {
		return err
	}

	// A step must identify one reporting location and one kind of movement.
	// A conflicting record with no usable coordinates still makes it ambiguous.
	type report struct {
		stanox  string
		arrival bool
	}
	reports := map[tdKey]report{}
	ambiguous := map[tdKey]bool{}
	for _, step := range snapshot.Steps {
		if strings.TrimSpace(step.STEPTYPE) != "B" {
			continue
		}
		key := tdKey{strings.TrimSpace(step.TD), strings.TrimSpace(step.FROMBERTH), strings.TrimSpace(step.TOBERTH)}
		if key.Area == "" || key.From == "" || key.To == "" || key.From == key.To {
			continue
		}
		value := report{stanox: strings.TrimSpace(step.STANOX)}
		switch strings.TrimSpace(step.EVENT) {
		case "A", "C":
			value.arrival = true
		case "B", "D":
		default:
			ambiguous[key] = true
			continue
		}
		if previous, ok := reports[key]; ok && previous != value {
			ambiguous[key] = true
		}
		reports[key] = value
	}
	t.smart = make(map[tdKey]smartReportingPoint)
	for key, report := range reports {
		location, ok := snapshot.Locations[report.stanox]
		if ambiguous[key] || !ok || len(location.Refs) == 0 || !railtd.ValidPoint(&location.Point) {
			continue
		}
		t.smart[key] = smartReportingPoint{SMARTLocation: location, arrival: report.arrival}
		t.areas[key.Area] = true
	}
	return nil
}
