package cachedresults

import (
	"bytes"
	"compress/gzip"
	"testing"
)

func TestDecompressCachedValueSupportsZstdAndLegacyGzip(t *testing.T) {
	payload := []byte(`{"journeys":["one","two"]}`)

	t.Run("zstd", func(t *testing.T) {
		compressed := cacheZstdEncoder.EncodeAll(payload, nil)
		decoded, codec, err := decompressCachedValue(compressed)
		if err != nil {
			t.Fatalf("decompress zstd cache value: %v", err)
		}
		if codec != "zstd" || !bytes.Equal(decoded, payload) {
			t.Fatalf("unexpected zstd decode result: codec=%q payload=%q", codec, decoded)
		}
	})

	t.Run("legacy gzip", func(t *testing.T) {
		var compressed bytes.Buffer
		writer := gzip.NewWriter(&compressed)
		if _, err := writer.Write(payload); err != nil {
			t.Fatalf("write gzip fixture: %v", err)
		}
		if err := writer.Close(); err != nil {
			t.Fatalf("close gzip fixture: %v", err)
		}

		decoded, codec, err := decompressCachedValue(compressed.Bytes())
		if err != nil {
			t.Fatalf("decompress gzip cache value: %v", err)
		}
		if codec != "gzip" || !bytes.Equal(decoded, payload) {
			t.Fatalf("unexpected gzip decode result: codec=%q payload=%q", codec, decoded)
		}
	})
}
