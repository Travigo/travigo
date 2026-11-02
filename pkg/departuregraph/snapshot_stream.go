package departuregraph

import (
	"encoding/gob"
	"fmt"
	"io"
	"reflect"
)

const snapshotChunkEntries = 4096

// v9 keeps each gob message small, including slices stored inside maps. Encoding
// a single snapshotFile buffers the entire uncompressed graph inside gob.
func encodeSnapshot(w io.Writer, snapshot snapshotFile) error {
	encoder := gob.NewEncoder(w)
	header := snapshot
	fields := reflect.ValueOf(&header).Elem()
	for i := 0; i < fields.NumField(); i++ {
		if kind := fields.Field(i).Kind(); kind == reflect.Slice || kind == reflect.Map {
			fields.Field(i).SetZero()
		}
	}
	if err := encoder.Encode(header); err != nil {
		return err
	}
	fields = reflect.ValueOf(snapshot)
	for i := 0; i < fields.NumField(); i++ {
		field := fields.Field(i)
		if field.Kind() != reflect.Slice && field.Kind() != reflect.Map {
			continue
		}
		if err := encoder.Encode(fields.Type().Field(i).Name); err != nil {
			return err
		}
		if err := encodeSnapshotCollection(encoder, field); err != nil {
			return err
		}
	}
	return encoder.Encode("")
}

func encodeSnapshotCollection(encoder *gob.Encoder, value reflect.Value) error {
	length := value.Len()
	if value.IsNil() {
		length = -1
	}
	if err := encoder.Encode(length); err != nil || length < 0 {
		return err
	}
	if value.Kind() == reflect.Slice {
		for start := 0; start < length; start += snapshotChunkEntries {
			if err := encoder.Encode(value.Slice(start, min(start+snapshotChunkEntries, length)).Interface()); err != nil {
				return err
			}
		}
		return nil
	}
	iterator := value.MapRange()
	for iterator.Next() {
		if err := encoder.Encode(iterator.Key().Interface()); err != nil {
			return err
		}
		entry := iterator.Value()
		if entry.Kind() == reflect.Slice {
			if err := encodeSnapshotCollection(encoder, entry); err != nil {
				return err
			}
		} else if err := encoder.Encode(entry.Interface()); err != nil {
			return err
		}
	}
	return nil
}

func decodeSnapshot(r io.Reader, snapshot *snapshotFile) error {
	decoder := gob.NewDecoder(r)
	if err := decoder.Decode(snapshot); err != nil {
		return err
	}
	if snapshot.Version < 1 || snapshot.Version > snapshotVersion {
		return fmt.Errorf("unsupported departure graph snapshot version %d", snapshot.Version)
	}
	if snapshot.Version < 9 {
		return nil
	}
	fields := reflect.ValueOf(snapshot).Elem()
	seen := map[string]bool{}
	for {
		var name string
		if err := decoder.Decode(&name); err != nil {
			return err
		}
		if name == "" {
			// Every collection, including empty ones, is required so a truncated
			// checkpoint cannot silently masquerade as a complete graph.
			for i := 0; i < fields.NumField(); i++ {
				if kind := fields.Field(i).Kind(); (kind == reflect.Slice || kind == reflect.Map) && !seen[fields.Type().Field(i).Name] {
					return fmt.Errorf("missing snapshot field %s", fields.Type().Field(i).Name)
				}
			}
			return nil
		}
		field := fields.FieldByName(name)
		if !field.IsValid() || (field.Kind() != reflect.Slice && field.Kind() != reflect.Map) || seen[name] {
			return fmt.Errorf("invalid snapshot field %q", name)
		}
		seen[name] = true
		if err := decodeSnapshotCollection(decoder, field); err != nil {
			return fmt.Errorf("decode snapshot %s: %w", name, err)
		}
	}
}

func decodeSnapshotCollection(decoder *gob.Decoder, value reflect.Value) error {
	var length int
	if err := decoder.Decode(&length); err != nil {
		return err
	}
	if length < -1 {
		return fmt.Errorf("invalid collection length %d", length)
	}
	if length == -1 {
		value.SetZero()
		return nil
	}
	if value.Kind() == reflect.Slice {
		value.Set(reflect.MakeSlice(value.Type(), length, length))
		chunk := reflect.New(value.Type())
		for start := 0; start < length; start += snapshotChunkEntries {
			count := min(snapshotChunkEntries, length-start)
			chunk.Elem().Set(value.Slice(start, start+count))
			if err := decoder.DecodeValue(chunk); err != nil {
				return err
			}
			if chunk.Elem().Len() != count {
				return fmt.Errorf("invalid snapshot chunk length")
			}
		}
		return nil
	}
	value.Set(reflect.MakeMapWithSize(value.Type(), length))
	key, entry := reflect.New(value.Type().Key()), reflect.New(value.Type().Elem())
	for i := 0; i < length; i++ {
		// Gob omits zero struct fields; do not carry them over from the
		// preceding bucket/grid key when reusing the decode destination.
		key.Elem().SetZero()
		if err := decoder.DecodeValue(key); err != nil {
			return err
		}
		entry.Elem().SetZero()
		if entry.Elem().Kind() == reflect.Slice {
			if err := decodeSnapshotCollection(decoder, entry.Elem()); err != nil {
				return err
			}
		} else if err := decoder.DecodeValue(entry); err != nil {
			return err
		}
		value.SetMapIndex(key.Elem(), entry.Elem())
	}
	return nil
}
