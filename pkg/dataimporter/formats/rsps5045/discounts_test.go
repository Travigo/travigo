package rsps5045

import (
	"archive/zip"
	"bytes"
	"fmt"
	"testing"
	"time"
)

func TestWalkRailStatusDiscountsWithSnapshotOrder(t *testing.T) {
	status := bytes.Repeat([]byte{' '}, 99)
	copy(status, "S0033112203001012020")
	copy(status[47:55], "00004500")
	copy(status[95:99], "YYYY")
	discount := "D0033112203002F000"

	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	member, err := writer.Create("RJFAF123.DIS")
	if err != nil {
		t.Fatal(err)
	}
	// The source snapshot sorts DIS:D ahead of DIS:S.
	_, err = fmt.Fprintf(member, "/!! Start of file\n/!! Content type: DIS\n/!! Sequence: 123\n/!! Records: 2\n/!! Generated: 27/11/2026\n/!! Exporter: Travigo\n%s\n%s\n/!! End of file (27/11/2026)\n", discount, status)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(archive.Bytes()), int64(archive.Len()))
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	err = walkRailStatusDiscounts(reader.File[0], func(status railStatusRecord, discount railStatusDiscountRecord) error {
		count++
		if status.Code != "003" || status.StandardSingleMaxFlat != 4500 || discount.Category != "02" {
			t.Fatalf("joined status = %#v, discount = %#v", status, discount)
		}
		return nil
	})
	if err != nil || count != 1 {
		t.Fatalf("walking DIS snapshot visited %d discounts, error = %v", count, err)
	}
}

func TestParseRailStatusRecord(t *testing.T) {
	line := make([]byte, 99)
	for index := range line {
		line[index] = ' '
	}
	line[0] = 'S'
	copy(line[1:4], "003")
	copy(line[4:12], "31122030")
	copy(line[12:20], "01012020")
	copy(line[31:39], "00004500")
	copy(line[95:99], "YYYY")

	got, err := parseRailStatusRecord(line)
	if err != nil {
		t.Fatalf("parseRailStatusRecord() error = %v", err)
	}
	if got.Code != "003" || got.FirstSingleMaxFlat != 4500 || got.ClassMarkers != "YYYY" {
		t.Fatalf("parsed status = %#v", got)
	}
	if !got.Start.Equal(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)) || !got.End.Equal(time.Date(2030, 12, 31, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("status validity = [%s, %s]", got.Start, got.End)
	}
}

func TestParseRailStatusDiscountRecord(t *testing.T) {
	line := make([]byte, 18)
	for index := range line {
		line[index] = ' '
	}
	line[0] = 'D'
	copy(line[1:4], "003")
	copy(line[4:12], "31122030")
	copy(line[12:14], "02")
	line[14] = '0'
	copy(line[15:18], "300")

	got, err := parseRailStatusDiscountRecord(line)
	if err != nil {
		t.Fatalf("parseRailStatusDiscountRecord() error = %v", err)
	}
	if got.StatusCode != "003" || got.Category != "02" || got.Indicator != "0" || got.PercentageTenths != 300 {
		t.Fatalf("parsed status discount = %#v", got)
	}
	if !got.End.Equal(time.Date(2030, 12, 31, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("status discount end = %s", got.End)
	}
}

func TestParseRailcardMinimumFareRecord(t *testing.T) {
	line := []byte("EXMSVS311220302711202600008000")
	got, err := parseRailcardMinimumFareRecord(line)
	if err != nil {
		t.Fatalf("parseRailcardMinimumFareRecord() error = %v", err)
	}
	if got.RailcardCode != "EXM" || got.TicketCode != "SVS" || got.AmountPence != 8000 {
		t.Fatalf("parsed RCM record = %#v", got)
	}
	if !got.Start.Equal(time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC)) || !got.End.Equal(time.Date(2030, 12, 31, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("RCM validity = [%s, %s]", got.Start, got.End)
	}
}

func TestIsNonStandardDiscountFlag(t *testing.T) {
	for _, test := range []struct {
		flag string
		want bool
	}{{"0", false}, {"1", true}, {"2", false}, {"3", true}, {"", false}} {
		if got := isNonStandardDiscountFlag(test.flag); got != test.want {
			t.Errorf("isNonStandardDiscountFlag(%q) = %t, want %t", test.flag, got, test.want)
		}
	}
}

func TestParseRailcardRecord(t *testing.T) {
	line := make([]byte, 127)
	for index := range line {
		line[index] = ' '
	}
	copy(line[0:3], "YNG")
	copy(line[3:11], "31122030")
	copy(line[11:19], "01012020")
	line[27] = 'A'
	copy(line[28:48], "Young Person Card")
	line[49], line[50], line[51] = 'N', 'N', 'Y'
	copy(line[59:62], "001")
	copy(line[65:68], "001")
	copy(line[77:80], "001")
	copy(line[83:86], "000")
	copy(line[118:121], "003")
	copy(line[121:124], "004")

	got, err := parseRailcardRecord(line)
	if err != nil {
		t.Fatalf("parseRailcardRecord() error = %v", err)
	}
	if got.Code != "YNG" || got.Name != "Young Person Card" || got.HolderType != "A" || got.AdultStatusCode != "003" || got.ChildStatusCode != "004" {
		t.Fatalf("parsed railcard identity and status = %#v", got)
	}
	if got.RestrictedByArea || got.RestrictedByTrain || !got.RestrictedByDate || got.MinimumPassengers != 1 || got.MinimumHolders != 1 || got.MinimumAdults != 1 || got.MinimumChildren != 0 {
		t.Fatalf("parsed railcard eligibility = %#v", got)
	}
	if !got.EffectiveFrom.Equal(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)) || !got.EffectiveTo.Equal(time.Date(2030, 12, 31, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("railcard validity = [%s, %s]", got.EffectiveFrom, got.EffectiveTo)
	}
}
