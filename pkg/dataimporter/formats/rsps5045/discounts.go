package rsps5045

import (
	"archive/zip"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataimporter/datasets"
)

func (format *Fares) importRailStatusDiscounts(dataset datasets.DataSet, record fareRecordFactory) (int, error) {
	files := format.filesByExtension(".DIS")
	if len(files) == 0 {
		return 0, nil
	}
	if len(files) != 1 {
		return 0, fmt.Errorf("RSPS5045 feed has %d DIS files", len(files))
	}
	count := 0
	err := walkRailStatusDiscounts(files[0], func(status railStatusRecord, fields railStatusDiscountRecord) error {
		sourceID := strings.Join([]string{fields.StatusCode, dateKey(fields.End), fields.Category}, "-")
		identifier := fmt.Sprintf("%s-rsps5045-status-discount-%s", dataset.Identifier, safeID(sourceID))
		discount := &ctdf.FareRailStatusDiscount{
			FareRecord:            setRecordPrimary(record("status-discount", sourceID, status.Start, status.End), identifier),
			StatusCode:            fields.StatusCode,
			DiscountCategory:      fields.Category,
			Indicator:             fields.Indicator,
			PercentageTenths:      fields.PercentageTenths,
			FirstSingleMaxFlat:    status.FirstSingleMaxFlat,
			FirstReturnMaxFlat:    status.FirstReturnMaxFlat,
			StandardSingleMaxFlat: status.StandardSingleMaxFlat,
			StandardReturnMaxFlat: status.StandardReturnMaxFlat,
			FirstLowerMinimum:     status.FirstLowerMinimum,
			FirstHigherMinimum:    status.FirstHigherMinimum,
			StandardLowerMinimum:  status.StandardLowerMinimum,
			StandardHigherMinimum: status.StandardHigherMinimum,
			ClassMarkers:          status.ClassMarkers,
		}
		if err := discount.Validate(); err != nil {
			return fmt.Errorf("DIS status discount %s: %w", sourceID, err)
		}
		if err := upsertFare("fare_rail_status_discounts", identifier, discount); err != nil {
			return err
		}
		count++
		return nil
	})
	return count, err
}

func walkRailStatusDiscounts(file *zip.File, visit func(railStatusRecord, railStatusDiscountRecord) error) error {
	statuses := map[string]railStatusRecord{}
	var discounts []railStatusDiscountRecord
	_, _, err := scanFile(file, file.Name, "DIS", nil, func(line []byte, _ int64) error {
		switch line[0] {
		case 'S':
			status, err := parseRailStatusRecord(line)
			if err != nil {
				return err
			}
			statuses[status.Code+"\x00"+dateKey(status.End)] = status
		case 'D':
			discount, err := parseRailStatusDiscountRecord(line)
			if err != nil {
				return err
			}
			discounts = append(discounts, discount)
		default:
			return fmt.Errorf("DIS record has unsupported type %q", line[0])
		}
		return nil
	})
	if err != nil {
		return err
	}

	// The rebuilt archive can sort discount rows ahead of their status rows.
	for _, discount := range discounts {
		key := discount.StatusCode + "\x00" + dateKey(discount.End)
		status, exists := statuses[key]
		if !exists {
			return fmt.Errorf("DIS status discount %s/%s has no matching status record", discount.StatusCode, dateKey(discount.End))
		}
		if err := visit(status, discount); err != nil {
			return err
		}
	}
	return nil
}

func (format *Fares) importRailcardMinimumFares(dataset datasets.DataSet, record fareRecordFactory) (int, error) {
	files := format.filesByExtension(".RCM")
	if len(files) == 0 {
		return 0, nil
	}
	if len(files) != 1 {
		return 0, fmt.Errorf("RSPS5045 feed has %d RCM files", len(files))
	}
	count := 0
	_, _, err := scanFile(files[0], files[0].Name, "RCM", nil, func(line []byte, _ int64) error {
		minimumFields, parseErr := parseRailcardMinimumFareRecord(line)
		if parseErr != nil {
			return parseErr
		}
		sourceID := strings.Join([]string{minimumFields.RailcardCode, minimumFields.TicketCode, dateKey(minimumFields.Start), dateKey(minimumFields.End)}, "-")
		identifier := fmt.Sprintf("%s-rsps5045-railcard-minimum-fare-%s", dataset.Identifier, safeID(sourceID))
		minimum := &ctdf.FareRailcardMinimumFare{
			FareRecord:   setRecordPrimary(record("railcard-minimum-fare", sourceID, minimumFields.Start, minimumFields.End), identifier),
			RailcardCode: minimumFields.RailcardCode,
			TicketCode:   minimumFields.TicketCode,
			AmountPence:  minimumFields.AmountPence,
		}
		if err := minimum.Validate(); err != nil {
			return fmt.Errorf("RCM minimum fare %s: %w", sourceID, err)
		}
		if err := upsertFare("fare_railcard_minimum_fares", identifier, minimum); err != nil {
			return err
		}
		count++
		return nil
	})
	if err != nil {
		return count, err
	}
	return count, nil
}

type railcardMinimumFareRecord struct {
	RailcardCode string
	TicketCode   string
	End          time.Time
	Start        time.Time
	AmountPence  int64
}

func parseRailcardMinimumFareRecord(line []byte) (railcardMinimumFareRecord, error) {
	if len(line) != 30 {
		return railcardMinimumFareRecord{}, fmt.Errorf("RCM record has length %d; expected 30", len(line))
	}
	record := railcardMinimumFareRecord{
		RailcardCode: strings.TrimSpace(string(line[0:3])),
		TicketCode:   strings.TrimSpace(string(line[3:6])),
	}
	if record.RailcardCode == "" || record.TicketCode == "" {
		return railcardMinimumFareRecord{}, fmt.Errorf("RCM record requires railcard and ticket codes")
	}
	var err error
	record.End, err = dateField(line[6:14])
	if err != nil {
		return railcardMinimumFareRecord{}, fmt.Errorf("RCM %s/%s end date: %w", record.RailcardCode, record.TicketCode, err)
	}
	record.Start, err = dateField(line[14:22])
	if err != nil {
		return railcardMinimumFareRecord{}, fmt.Errorf("RCM %s/%s start date: %w", record.RailcardCode, record.TicketCode, err)
	}
	if !record.Start.IsZero() && !record.End.IsZero() && record.End.Before(record.Start) {
		return railcardMinimumFareRecord{}, fmt.Errorf("RCM %s/%s ends before it starts", record.RailcardCode, record.TicketCode)
	}
	amount, err := parseRSPSInt(line[22:30], "RCM minimum fare")
	if err != nil || amount <= 0 {
		return railcardMinimumFareRecord{}, fmt.Errorf("RCM %s/%s has invalid minimum fare %q", record.RailcardCode, record.TicketCode, strings.TrimSpace(string(line[22:30])))
	}
	record.AmountPence = int64(amount)
	return record, nil
}

func parseRailStatusRecord(line []byte) (railStatusRecord, error) {
	if len(line) != 99 {
		return railStatusRecord{}, fmt.Errorf("DIS status record has length %d; expected 99", len(line))
	}
	status := railStatusRecord{Code: strings.TrimSpace(string(line[1:4])), ClassMarkers: string(line[95:99])}
	if status.Code == "" {
		return railStatusRecord{}, fmt.Errorf("DIS status record has no status code")
	}
	var err error
	status.End, err = dateField(line[4:12])
	if err != nil {
		return railStatusRecord{}, fmt.Errorf("DIS status %s end date: %w", status.Code, err)
	}
	status.Start, err = dateField(line[12:20])
	if err != nil {
		return railStatusRecord{}, fmt.Errorf("DIS status %s start date: %w", status.Code, err)
	}
	fields := []struct {
		name  string
		value []byte
		to    *int64
	}{
		{"FIRST_SINGLE_MAX_FLAT", line[31:39], &status.FirstSingleMaxFlat},
		{"FIRST_RETURN_MAX_FLAT", line[39:47], &status.FirstReturnMaxFlat},
		{"STD_SINGLE_MAX_FLAT", line[47:55], &status.StandardSingleMaxFlat},
		{"STD_RETURN_MAX_FLAT", line[55:63], &status.StandardReturnMaxFlat},
		{"FIRST_LOWER_MIN", line[63:71], &status.FirstLowerMinimum},
		{"FIRST_HIGHER_MIN", line[71:79], &status.FirstHigherMinimum},
		{"STD_LOWER_MIN", line[79:87], &status.StandardLowerMinimum},
		{"STD_HIGHER_MIN", line[87:95], &status.StandardHigherMinimum},
	}
	for _, field := range fields {
		parsed, err := parseRSPSInt(field.value, "DIS "+field.name)
		if err != nil {
			return railStatusRecord{}, err
		}
		*field.to = int64(parsed)
	}
	return status, nil
}

type railStatusDiscountRecord struct {
	StatusCode       string
	End              time.Time
	Category         string
	Indicator        string
	PercentageTenths int
}

func parseRailStatusDiscountRecord(line []byte) (railStatusDiscountRecord, error) {
	if len(line) != 18 {
		return railStatusDiscountRecord{}, fmt.Errorf("DIS status discount record has length %d; expected 18", len(line))
	}
	record := railStatusDiscountRecord{StatusCode: strings.TrimSpace(string(line[1:4]))}
	if record.StatusCode == "" {
		return railStatusDiscountRecord{}, fmt.Errorf("DIS status discount record has no status code")
	}
	var err error
	record.End, err = dateField(line[4:12])
	if err != nil {
		return railStatusDiscountRecord{}, fmt.Errorf("DIS status discount %s end date: %w", record.StatusCode, err)
	}
	record.Category = strings.TrimSpace(string(line[12:14]))
	categoryNumber, err := strconv.Atoi(record.Category)
	if err != nil || categoryNumber < 1 || categoryNumber > 20 {
		return railStatusDiscountRecord{}, fmt.Errorf("DIS status %s has invalid discount category %q", record.StatusCode, record.Category)
	}
	record.Indicator = strings.TrimSpace(string(line[14:15]))
	record.PercentageTenths, err = parseRSPSInt(line[15:18], "DIS discount percentage")
	if err != nil || record.PercentageTenths > 1000 {
		return railStatusDiscountRecord{}, fmt.Errorf("DIS status %s category %s has invalid discount percentage %q", record.StatusCode, record.Category, strings.TrimSpace(string(line[15:18])))
	}
	return record, nil
}

func isNonStandardDiscountFlag(flag string) bool {
	return flag == "1" || flag == "3"
}
