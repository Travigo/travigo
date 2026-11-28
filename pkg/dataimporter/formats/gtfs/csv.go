package gtfs

import (
	stdcsv "encoding/csv"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
)

// gtfsCSVDecoder uses encoding/csv so quoted commas and escaped quotes stay within their fields.
type gtfsCSVDecoder[T any] struct {
	reader *stdcsv.Reader
	header []string
	fields []int
}

func newGTFSCSVDecoder[T any](source io.Reader) (*gtfsCSVDecoder[T], error) {
	reader := stdcsv.NewReader(source)
	reader.Comment = '#'
	reader.TrimLeadingSpace = true

	header, err := reader.Read()
	if err != nil {
		return nil, err
	}
	if len(header) == 0 {
		return nil, fmt.Errorf("GTFS CSV has an empty header")
	}
	for i := range header {
		header[i] = strings.TrimSpace(header[i])
	}
	reader.FieldsPerRecord = len(header)

	recordType := reflect.TypeOf((*T)(nil)).Elem()
	if recordType.Kind() != reflect.Struct {
		return nil, fmt.Errorf("GTFS CSV record type %s is not a struct", recordType)
	}

	fieldIndexes := make(map[string]int, recordType.NumField())
	for i := 0; i < recordType.NumField(); i++ {
		field := recordType.Field(i)
		if !field.IsExported() {
			continue
		}
		tag, ok := field.Tag.Lookup("csv")
		if !ok || tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if name == "" {
			continue
		}
		if _, exists := fieldIndexes[name]; !exists {
			fieldIndexes[name] = i
		}
	}

	fields := make([]int, len(header))
	for i, name := range header {
		fields[i] = -1
		if fieldIndex, ok := fieldIndexes[name]; ok {
			fields[i] = fieldIndex
		}
	}

	return &gtfsCSVDecoder[T]{reader: reader, header: header, fields: fields}, nil
}

func (decoder *gtfsCSVDecoder[T]) Read() (T, error) {
	var record T
	values, err := decoder.reader.Read()
	if err != nil {
		return record, err
	}

	recordValue := reflect.ValueOf(&record).Elem()
	for i, fieldIndex := range decoder.fields {
		if fieldIndex < 0 {
			continue
		}
		if err := setGTFSCSVField(recordValue.Field(fieldIndex), strings.TrimSpace(values[i])); err != nil {
			line, _ := decoder.reader.FieldPos(i)
			return record, fmt.Errorf("csv: line %d field %q: %w", line, decoder.header[i], err)
		}
	}

	return record, nil
}

func setGTFSCSVField(field reflect.Value, value string) error {
	if value == "" {
		return nil
	}

	switch field.Kind() {
	case reflect.String:
		field.SetString(value)
	case reflect.Bool:
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return err
		}
		field.SetBool(parsed)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		parsed, err := strconv.ParseInt(value, 10, field.Type().Bits())
		if err != nil {
			return err
		}
		field.SetInt(parsed)
	case reflect.Float32, reflect.Float64:
		parsed, err := strconv.ParseFloat(value, field.Type().Bits())
		if err != nil {
			return err
		}
		field.SetFloat(parsed)
	default:
		return fmt.Errorf("cannot decode GTFS CSV value into %s", field.Type())
	}
	return nil
}
