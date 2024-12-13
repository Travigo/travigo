package datasources

import (
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/dataimporter/datasets"
	"github.com/travigo/travigo/pkg/dataimporter/manager"
)

func (s Source) DataSetQuery(q query.DataSet) (*datasets.DataSet, error) {
	dataset, err := manager.GetDataset(q.DataSetID)

	if err != nil {
		return nil, err
	}

	return &dataset, nil
}
