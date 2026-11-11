package gtfs

import (
	"archive/zip"
	"encoding/csv"
	"io"
	"strings"
)

// ReadScheduleFeedVersion returns the optional publisher version from a GTFS
// ZIP. Missing or unreadable metadata leaves change detection to the file hash.
func ReadScheduleFeedVersion(source io.ReaderAt, size int64) string {
	archive, err := zip.NewReader(source, size)
	if err != nil {
		return ""
	}
	file, err := archive.Open("feed_info.txt")
	if err != nil {
		return ""
	}
	defer file.Close()

	const maxFeedInfoBytes = 64 * 1024
	data, err := io.ReadAll(io.LimitReader(file, maxFeedInfoBytes+1))
	if err != nil || len(data) > maxFeedInfoBytes {
		return ""
	}
	content := strings.TrimPrefix(string(data), "\uFEFF")
	rows, err := csv.NewReader(strings.NewReader(content)).ReadAll()
	if err != nil || len(rows) != 2 {
		return ""
	}
	for i, column := range rows[0] {
		if column == "feed_version" {
			return strings.TrimSpace(rows[1][i])
		}
	}
	return ""
}
