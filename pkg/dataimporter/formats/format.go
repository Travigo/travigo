package formats

import (
	"io"

	"github.com/travigo/travigo/pkg/ctdf"
)

type Format interface {
	ParseFile(io.Reader) error
	ImportIntoMongoAsCTDF(string, SupportedObjects, *ctdf.DataSource) error
}
