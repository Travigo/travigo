package dbwatch

import (
	"reflect"
	"testing"

	"github.com/travigo/travigo/pkg/ctdf"
	"go.mongodb.org/mongo-driver/bson"
)

func TestServiceAlertUpdateHasContentChange(t *testing.T) {
	tests := []struct {
		name              string
		updated           bson.M
		removed           []string
		wantContentChange bool
	}{
		{
			name:              "modification timestamp only",
			updated:           bson.M{"modificationdatetime": "new-time"},
			wantContentChange: false,
		},
		{
			name:              "datasource timestamp only",
			updated:           bson.M{"datasource.timestamp": "new-timestamp"},
			wantContentChange: false,
		},
		{
			name:              "validity window only",
			updated:           bson.M{"validfrom": "new-start", "validuntil": "new-end"},
			wantContentChange: false,
		},
		{
			name:              "text changed",
			updated:           bson.M{"text": "new text"},
			wantContentChange: true,
		},
		{
			name:              "important field removed",
			removed:           []string{"title"},
			wantContentChange: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := serviceAlertUpdateHasContentChange(serviceAlertUpdateDescription{
				UpdatedFields: test.updated,
				RemovedFields: test.removed,
			})
			if got != test.wantContentChange {
				t.Fatalf("service alert content change = %t, want %t", got, test.wantContentChange)
			}
		})
	}
}

func TestServiceAlertUpdateHasField(t *testing.T) {
	if !serviceAlertUpdateHasField(serviceAlertUpdateDescription{
		UpdatedFields: bson.M{"matchedidentifiers.0": "stop-2"},
	}, "matchedidentifiers") {
		t.Fatal("expected nested matchedidentifiers update to be detected")
	}
}

func TestServiceAlertAddedMatchedIdentifiers(t *testing.T) {
	before := &ctdf.ServiceAlert{MatchedIdentifiers: []string{"service-1", "stop-1"}}

	added, changed := serviceAlertAddedMatchedIdentifiers(before, &ctdf.ServiceAlert{
		MatchedIdentifiers: []string{"service-1", "stop-1", "stop-2"},
	})
	if !changed || !reflect.DeepEqual(added, []string{"stop-2"}) {
		t.Fatalf("added identifiers = %#v, changed = %t", added, changed)
	}

	added, changed = serviceAlertAddedMatchedIdentifiers(before, &ctdf.ServiceAlert{
		MatchedIdentifiers: []string{"service-1", "stop-2"},
	})
	if !changed || !reflect.DeepEqual(added, []string{"stop-2"}) {
		t.Fatalf("replacement identifiers = %#v, changed = %t", added, changed)
	}

	added, changed = serviceAlertAddedMatchedIdentifiers(before, &ctdf.ServiceAlert{
		MatchedIdentifiers: []string{"stop-1", "service-1"},
	})
	if changed || len(added) != 0 {
		t.Fatalf("reordered identifiers = %#v, changed = %t", added, changed)
	}
}
