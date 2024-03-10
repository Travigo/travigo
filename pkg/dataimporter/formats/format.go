package formats

import (
	"io"

	"github.com/adjust/rmq/v5"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataimporter/datasets"
)

type Format interface {
	ParseFile(io.Reader) error
	Import(datasets.DataSet, *ctdf.DataSource) error
}

type RealtimeQueueFormat interface {
	Format
	SetupRealtimeQueue(rmq.Queue)
}
