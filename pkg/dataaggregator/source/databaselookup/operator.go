package databaselookup

import (
	"context"
	"errors"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/database"
)

func (s Source) OperatorQuery(operatorQuery query.Operator) (*ctdf.Operator, error) {
	collection := database.GetCollection("operators")
	var operator *ctdf.Operator
	collection.FindOne(context.Background(), operatorQuery.ToBson()).Decode(&operator)

	if operator == nil {
		return nil, errors.New("could not find a matching Operator")
	} else {
		return operator, nil
	}
}
