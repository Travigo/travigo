package manager

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/dataimporter/formats"
	"github.com/travigo/travigo/pkg/util"
)

// Just a static list for now
func GetRegisteredDataSets() []DataSet {
	return []DataSet{
		{
			Identifier: "gb-traveline-noc",
			Format:     DataSetFormatTravelineNOC,
			Provider: Provider{
				Name:    "Traveline",
				Website: "https://www.travelinedata.org.uk/",
			},
			Source:       "https://www.travelinedata.org.uk/noc/api/1.0/nocrecords.xml",
			UnpackBundle: BundleFormatNone,
			SupportedObjects: formats.SupportedObjects{
				Operators:      true,
				OperatorGroups: true,
			},
		},
		{
			Identifier: "gb-dft-naptan",
			Format:     DataSetFormatNaPTAN,
			Provider: Provider{
				Name:    "Department for Transport",
				Website: "https://www.gov.uk/government/organisations/department-for-transport",
			},
			Source:       "https://naptan.api.dft.gov.uk/v1/access-nodes?dataFormat=xml",
			UnpackBundle: BundleFormatNone,
			SupportedObjects: formats.SupportedObjects{
				Stops:      true,
				StopGroups: true,
			},
		},
		{
			Identifier: "gb-nationalrail-toc",
			Format:     DataSetFormatNationalRailTOC,
			Provider: Provider{
				Name:    "National Rail",
				Website: "https://nationalrail.co.uk",
			},
			Source:       "https://opendata.nationalrail.co.uk/api/staticfeeds/4.0/tocs",
			UnpackBundle: BundleFormatNone,
			SupportedObjects: formats.SupportedObjects{
				Operators: true,
				Services:  true,
			},

			DownloadHandler: func(r *http.Request) {
				token := nationalRailLogin()
				r.Header.Set("X-Auth-Token", token)
			},
		},
		{
			// Import STANOX Stop IDs to Stops from Network Rail CORPUS dataset
			Identifier: "gb-networkrail-corpus",
			Format:     DataSetFormatNetworkRailCorpus,
			Provider: Provider{
				Name:    "Network Rail",
				Website: "https://networkrail.co.uk",
			},
			Source:       "https://publicdatafeeds.networkrail.co.uk/ntrod/SupportingFileAuthenticate?type=CORPUS",
			UnpackBundle: BundleFormatGZ,
			SupportedObjects: formats.SupportedObjects{
				Stops: true,
			},

			DownloadHandler: func(r *http.Request) {
				env := util.GetEnvironmentVariables()
				if env["TRAVIGO_NETWORKRAIL_USERNAME"] == "" {
					log.Fatal().Msg("TRAVIGO_NETWORKRAIL_USERNAME must be set")
				}
				if env["TRAVIGO_NETWORKRAIL_PASSWORD"] == "" {
					log.Fatal().Msg("TRAVIGO_NETWORKRAIL_PASSWORD must be set")
				}

				r.SetBasicAuth(env["TRAVIGO_NETWORKRAIL_USERNAME"], env["TRAVIGO_NETWORKRAIL_PASSWORD"])
			},
		},
		{
			// Import STANOX Stop IDs to Stops from Network Rail CORPUS dataset
			Identifier: "gb-dft-bods-sirivm-all",
			Format:     DataSetFormatSiriVM,
			Provider: Provider{
				Name:    "Department for Transport",
				Website: "https://www.gov.uk/government/organisations/department-for-transport",
			},
			Source:       "https://data.bus-data.dft.gov.uk/avl/download/bulk_archive",
			UnpackBundle: BundleFormatZIP,
			SupportedObjects: formats.SupportedObjects{
				RealtimeJourneys: true,
			},
			ImportDestination: ImportDestinationRealtimeQueue,

			DownloadHandler: func(r *http.Request) {
				env := util.GetEnvironmentVariables()
				if env["TRAVIGO_BODS_API_KEY"] == "" {
					log.Fatal().Msg("TRAVIGO_BODS_API_KEY must be set")
				}

				r.URL.Query().Add("api_key", env["TRAVIGO_BODS_API_KEY"])
			},
		},
	}
}

func nationalRailLogin() string {
	env := util.GetEnvironmentVariables()
	if env["TRAVIGO_NATIONALRAIL_USERNAME"] == "" {
		log.Fatal().Msg("TRAVIGO_NATIONALRAIL_USERNAME must be set")
	}
	if env["TRAVIGO_NATIONALRAIL_PASSWORD"] == "" {
		log.Fatal().Msg("TRAVIGO_NATIONALRAIL_PASSWORD must be set")
	}

	formData := url.Values{
		"username": {env["TRAVIGO_NATIONALRAIL_USERNAME"]},
		"password": {env["TRAVIGO_NATIONALRAIL_PASSWORD"]},
	}

	client := &http.Client{}
	req, err := http.NewRequest("POST", "https://opendata.nationalrail.co.uk/authenticate", strings.NewReader(formData.Encode()))
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create auth HTTP request")
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to perform auth HTTP request")
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to read auth HTTP request")
	}

	var loginResponse struct {
		Token string `json:"token"`
	}
	json.Unmarshal(body, &loginResponse)

	return loginResponse.Token
}
