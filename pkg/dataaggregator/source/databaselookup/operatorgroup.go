package databaselookup

import (
	"context"
	"errors"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
)

func (s Source) OperatorGroupQuery(q ctdf.QueryOperatorGroup) (*ctdf.OperatorGroup, error) {
	collection := database.GetCollection("operator_groups")
	var operator *ctdf.OperatorGroup
	collection.FindOne(context.Background(), q.ToBson()).Decode(&operator)

	if operator == nil {
		return nil, errors.New("could not find a matching Operator Group")
	} else {
		return operator, nil
	}
}
