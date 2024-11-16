package datasources

import (
	"reflect"

	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/dataaggregator/source"
	"github.com/travigo/travigo/pkg/dataimporter/datasets"
)

type Source struct {
}

func (s Source) GetName() string {
	return "Datasources"
}

func (s Source) Supports() []reflect.Type {
	return []reflect.Type{
		reflect.TypeOf(datasets.DataSet{}),
		reflect.TypeOf(datasets.DataSource{}),
	}
}

func (s Source) Lookup(q any) (interface{}, error) {
	switch q.(type) {
	case query.DataSet:
		return s.DataSetQuery(q.(query.DataSet))
	case query.DataSource:
		return s.DataSourceQuery(q.(query.DataSource))
	default:
		return nil, source.UnsupportedSourceError
	}
}
