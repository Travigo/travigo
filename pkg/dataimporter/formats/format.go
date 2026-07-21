package formats

import (
	"io"

	"github.com/adjust/rmq/v5"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataimporter/datasets"
)

type Format interface {
	ParseFile(io.Reader) error
	Import(datasets.DataSet, *ctdf.DataSourceReference) (datasets.DataImportReport, error)
}

type RealtimeQueueFormat interface {
	Format
	SetupRealtimeQueue(rmq.Queue)
}

// APIFormat imports directly from a remote API instead of a downloaded file.
// It is still a normal registered dataset and follows the same reporting and
// batch-runner lifecycle as file-backed formats.
type APIFormat interface {
	Format
	ImportAPI(datasets.DataSet, *ctdf.DataSourceReference) (datasets.DataImportReport, error)
}
