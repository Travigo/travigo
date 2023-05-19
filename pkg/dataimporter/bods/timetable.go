package bods

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
)

type TimetableDatasetList struct {
	Count int

	Next     string
	Previous string

	Results []*TimetableDataset
}

type TimetableDataset struct {
	ID           int
	Created      string // should be time
	Modified     string // should be time
	OperatorName string
	NOC          []string
	Name         string
	Description  string
	Comment      string
	Status       string
	URL          string
	Extension    string
	// Lines          []string
	// FirstStartDate string
	// FirstEndDate   string
}

func GetTimetableDataset(source string) ([]*TimetableDataset, error) {
	var datasetList TimetableDatasetList

	resp, err := http.Get(source)

	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		io.Copy(os.Stdout, resp.Body)
		return nil, errors.New(fmt.Sprintf("Request failed for %s", source))
	}

	err = json.NewDecoder(resp.Body).Decode(&datasetList)
	if err != nil {
		return nil, err
	}

	if datasetList.Next != "" {
		nextDatasets, err := GetTimetableDataset(datasetList.Next)

		if err != nil {
			return nil, err
		}

		datasetList.Results = append(datasetList.Results, nextDatasets...)
	}

	return datasetList.Results, nil
}
