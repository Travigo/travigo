package gtfs

import (
	stdcsv "encoding/csv"
	"strings"

	gtfscsv "github.com/travigo/go-csv"
)

// decodeGTFSHeader parses the header with encoding/csv before passing it to
// go-csv, whose DecodeHeader method does not remove CSV quotes.
func decodeGTFSHeader(decoder *gtfscsv.Decoder, line string) error {
	header, err := stdcsv.NewReader(strings.NewReader(line)).Read()
	if err != nil {
		return err
	}

	_, err = decoder.DecodeHeader(strings.Join(header, ","))
	return err
}
