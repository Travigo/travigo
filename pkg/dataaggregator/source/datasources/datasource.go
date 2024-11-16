package datasources

import (
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/dataimporter/datasets"
	"github.com/travigo/travigo/pkg/dataimporter/manager"
)

func (s Source) DataSourceQuery(q query.DataSource) (*datasets.DataSource, error) {
	datasource, err := manager.GetDatasource(q.Identifier)

	if err != nil {
		return nil, err
	}

	return &datasource, nil
}
