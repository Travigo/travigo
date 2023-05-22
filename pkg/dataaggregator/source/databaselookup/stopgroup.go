package databaselookup

import (
	"context"
	"errors"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/database"
)

func (s Source) StopGroupQuery(stopQuery query.StopGroup) (*ctdf.StopGroup, error) {
	collection := database.GetCollection("stop_groups")
	var stopGroup *ctdf.StopGroup
	collection.FindOne(context.Background(), stopQuery.ToBson()).Decode(&stopGroup)

	if stopGroup == nil {
		return nil, errors.New("could not find a matching Stop Group")
	} else {
		return stopGroup, nil
	}
}
