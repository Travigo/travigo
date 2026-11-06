package tdmappings

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	networkrailcorpus "github.com/travigo/travigo/pkg/dataimporter/formats/networkrail-corpus"
	"github.com/travigo/travigo/pkg/railtd"
	"github.com/urfave/cli/v2"
)

// Main stations and intermediate calls on the Cambridge to King's Cross pilot.
const pilotStations = "CBN,CBG,CBS,FXN,SHH,MEL,ROY,ASH,BAL,LCH,HIT,SVG,FPK,KGX"

func candidatesCommand() *cli.Command {
	return &cli.Command{
		Name: "candidates", Usage: "List SMART steps at selected CORPUS stations for manual review",
		Flags: []cli.Flag{
			&cli.PathFlag{Name: "smart", Usage: "Uncompressed Network Rail SMART JSON", Required: true},
			&cli.PathFlag{Name: "corpus", Usage: "Uncompressed Network Rail CORPUS JSON", Required: true},
			&cli.PathFlag{Name: "output", Usage: "Write candidate CSV to this file instead of stdout"},
			&cli.StringFlag{Name: "stations", Usage: "Comma-separated CRS codes", Value: pilotStations},
		},
		Action: func(c *cli.Context) error {
			smart, err := os.ReadFile(c.Path("smart"))
			if err != nil {
				return err
			}
			corpus, err := os.ReadFile(c.Path("corpus"))
			if err != nil {
				return err
			}
			var content bytes.Buffer
			output := csv.NewWriter(&content)
			if err := writeCandidates(output, smart, corpus, c.String("stations")); err != nil {
				return err
			}
			output.Flush()
			if err := output.Error(); err != nil {
				return err
			}
			if path := c.Path("output"); path != "" {
				return os.WriteFile(path, content.Bytes(), 0644)
			}
			_, err = content.WriteTo(c.App.Writer)
			return err
		},
	}
}

func readSMART(raw []byte) ([]railtd.SMARTStep, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, fmt.Errorf("SMART file is empty")
	}
	if raw[0] == '{' {
		var envelope map[string]json.RawMessage
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return nil, err
		}
		var array []byte
		for _, value := range envelope {
			if len(value) == 0 || value[0] != '[' {
				continue
			}
			if array != nil {
				return nil, fmt.Errorf("SMART file has multiple data arrays")
			}
			array = value
		}
		raw = array
	}
	var steps []railtd.SMARTStep
	if err := json.Unmarshal(raw, &steps); err != nil {
		return nil, fmt.Errorf("decode SMART: %w", err)
	}
	for _, step := range steps {
		if strings.TrimSpace(step.TD) != "" && strings.TrimSpace(step.STANOX) != "" {
			return steps, nil
		}
	}
	return nil, fmt.Errorf("SMART file has no step records with TD and STANOX")
}

func writeCandidates(output *csv.Writer, smartRaw, corpusRaw []byte, stationList string) error {
	steps, err := readSMART(smartRaw)
	if err != nil {
		return err
	}
	var corpus networkrailcorpus.Corpus
	if err := json.Unmarshal(corpusRaw, &corpus); err != nil {
		return fmt.Errorf("decode CORPUS: %w", err)
	}
	stations := map[string]bool{}
	for _, code := range strings.Split(stationList, ",") {
		if code = strings.TrimSpace(strings.ToUpper(code)); code != "" {
			stations[code] = true
		}
	}
	locations := map[string][]networkrailcorpus.TiplocData{}
	for _, location := range corpus.TiplocData {
		stanox := strings.TrimSpace(location.STANOX)
		if stanox != "" && stations[strings.TrimSpace(location.ThreeAlpha)] {
			locations[stanox] = append(locations[stanox], location)
		}
	}
	if len(locations) == 0 {
		return fmt.Errorf("no CORPUS STANOX codes found for the selected stations")
	}
	if err := output.Write([]string{"area", "from", "to", "stanox", "tiploc", "crs", "event", "berth_offset_seconds"}); err != nil {
		return err
	}
	seen := map[[8]string]bool{}
	for _, step := range steps {
		area, from, to := strings.TrimSpace(step.TD), strings.TrimSpace(step.FROMBERTH), strings.TrimSpace(step.TOBERTH)
		if strings.TrimSpace(step.STEPTYPE) != "B" || area == "" || from == "" || to == "" || from == to {
			continue
		}
		stanox := strings.TrimSpace(step.STANOX)
		for _, location := range locations[stanox] {
			offset := ""
			if step.BERTHOFFSET != nil {
				offset = fmt.Sprint(step.BERTHOFFSET)
			}
			row := [8]string{area, from, to, stanox, strings.TrimSpace(location.TIPLOC), strings.TrimSpace(location.ThreeAlpha), strings.TrimSpace(step.EVENT), offset}
			if seen[row] {
				continue
			}
			if err := output.Write(row[:]); err != nil {
				return err
			}
			seen[row] = true
		}
	}
	return nil
}
