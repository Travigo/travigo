package manager

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/dataimporter/datasets"
	"github.com/travigo/travigo/pkg/util"
)

// Just a static list for now
func GetRegisteredDataSets() []datasets.DataSet {
	return []datasets.DataSet{
		{
			Identifier: "gb-traveline-noc",
			Format:     datasets.DataSetFormatTravelineNOC,
			Provider: datasets.Provider{
				Name:    "Traveline",
				Website: "https://www.travelinedata.org.uk/",
			},
			Source:       "https://www.travelinedata.org.uk/noc/api/1.0/nocrecords.xml",
			UnpackBundle: datasets.BundleFormatNone,
			SupportedObjects: datasets.SupportedObjects{
				Operators:      true,
				OperatorGroups: true,
			},
		},
		{
			Identifier: "gb-dft-naptan",
			Format:     datasets.DataSetFormatNaPTAN,
			Provider: datasets.Provider{
				Name:    "Department for Transport",
				Website: "https://www.gov.uk/government/organisations/department-for-transport",
			},
			Source:       "https://naptan.api.dft.gov.uk/v1/access-nodes?dataFormat=xml",
			UnpackBundle: datasets.BundleFormatNone,
			SupportedObjects: datasets.SupportedObjects{
				Stops:      true,
				StopGroups: true,
			},
		},
		{
			Identifier: "gb-nationalrail-toc",
			Format:     datasets.DataSetFormatNationalRailTOC,
			Provider: datasets.Provider{
				Name:    "National Rail",
				Website: "https://nationalrail.co.uk",
			},
			Source:       "https://opendata.nationalrail.co.uk/api/staticfeeds/4.0/tocs",
			UnpackBundle: datasets.BundleFormatNone,
			SupportedObjects: datasets.SupportedObjects{
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
			Format:     datasets.DataSetFormatNetworkRailCorpus,
			Provider: datasets.Provider{
				Name:    "Network Rail",
				Website: "https://networkrail.co.uk",
			},
			Source:       "https://publicdatafeeds.networkrail.co.uk/ntrod/SupportingFileAuthenticate?type=CORPUS",
			UnpackBundle: datasets.BundleFormatGZ,
			SupportedObjects: datasets.SupportedObjects{
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
			Identifier: "gb-dft-bods-sirivm-all",
			Format:     datasets.DataSetFormatSiriVM,
			Provider: datasets.Provider{
				Name:    "Department for Transport",
				Website: "https://www.gov.uk/government/organisations/department-for-transport",
			},
			Source:       "https://data.bus-data.dft.gov.uk/avl/download/bulk_archive",
			UnpackBundle: datasets.BundleFormatZIP,
			SupportedObjects: datasets.SupportedObjects{
				RealtimeJourneys: true,
			},
			ImportDestination: datasets.ImportDestinationRealtimeQueue,

			LinkedDataset: "gb-dft-bods-gtfs-schedule",

			DownloadHandler: func(r *http.Request) {
				env := util.GetEnvironmentVariables()
				if env["TRAVIGO_BODS_API_KEY"] == "" {
					log.Fatal().Msg("TRAVIGO_BODS_API_KEY must be set")
				}

				r.URL.Query().Add("api_key", env["TRAVIGO_BODS_API_KEY"])
			},
		},
		{
			Identifier: "gb-dft-bods-gtfs-realtime",
			Format:     datasets.DataSetFormatGTFSRealtime,
			Provider: datasets.Provider{
				Name:    "Department for Transport",
				Website: "https://www.gov.uk/government/organisations/department-for-transport",
			},
			Source:       "https://data.bus-data.dft.gov.uk/avl/download/gtfsrt",
			UnpackBundle: datasets.BundleFormatZIP,
			SupportedObjects: datasets.SupportedObjects{
				RealtimeJourneys: true,
			},
			ImportDestination: datasets.ImportDestinationRealtimeQueue,

			LinkedDataset: "gb-dft-bods-gtfs-schedule",

			DownloadHandler: func(r *http.Request) {
				env := util.GetEnvironmentVariables()
				if env["TRAVIGO_BODS_API_KEY"] == "" {
					log.Fatal().Msg("TRAVIGO_BODS_API_KEY must be set")
				}

				r.URL.Query().Add("api_key", env["TRAVIGO_BODS_API_KEY"])
			},
		},
		{
			Identifier: "gb-dft-bods-gtfs-schedule",
			Format:     datasets.DataSetFormatGTFSSchedule,
			Provider: datasets.Provider{
				Name:    "Department for Transport",
				Website: "https://www.gov.uk/government/organisations/department-for-transport",
			},
			Source:       "https://data.bus-data.dft.gov.uk/timetable/download/gtfs-file/all/",
			UnpackBundle: datasets.BundleFormatNone,
			SupportedObjects: datasets.SupportedObjects{
				Services: true,
				Journeys: true,
			},
			IgnoreObjects: datasets.IgnoreObjects{
				Services: datasets.IgnoreObjectServiceJourney{
					ByOperator: []string{"GB:NOC:NATX"},
				},
				Journeys: datasets.IgnoreObjectServiceJourney{
					ByOperator: []string{"GB:NOC:NATX"},
				},
			},

			DownloadHandler: func(r *http.Request) {
				env := util.GetEnvironmentVariables()
				if env["TRAVIGO_BODS_API_KEY"] == "" {
					log.Fatal().Msg("TRAVIGO_BODS_API_KEY must be set")
				}

				r.URL.Query().Add("api_key", env["TRAVIGO_BODS_API_KEY"])
			},
		},
		{
			Identifier: "gb-dft-bods-transxchange-coach",
			Format:     datasets.DataSetFormatTransXChange,
			Provider: datasets.Provider{
				Name:    "Department for Transport",
				Website: "https://www.gov.uk/government/organisations/department-for-transport",
			},
			Source:       "https://coach.bus-data.dft.gov.uk/TxC-2.4.zip",
			UnpackBundle: datasets.BundleFormatNone,
			SupportedObjects: datasets.SupportedObjects{
				Services: true,
				Journeys: true,
			},
		},
		{
			Identifier: "gb-nationalrail-timetable",
			Format:     datasets.DataSetFormatCIF,
			Provider: datasets.Provider{
				Name:    "National Rail",
				Website: "https://nationalrail.co.uk",
			},
			Source:       "https://opendata.nationalrail.co.uk/api/staticfeeds/3.0/timetable",
			UnpackBundle: datasets.BundleFormatNone,
			SupportedObjects: datasets.SupportedObjects{
				Services: true,
				Journeys: true,
			},

			DownloadHandler: func(r *http.Request) {
				token := nationalRailLogin()
				r.Header.Set("X-Auth-Token", token)
			},
		},
		{
			Identifier: "ie-gtfs-schedule",
			Format:     datasets.DataSetFormatGTFSSchedule,
			Provider: datasets.Provider{
				Name:    "Transport for Ireland",
				Website: "https://www.transportforireland.ie",
			},
			Source: "https://www.transportforireland.ie/transitData/Data/GTFS_Realtime.zip",
			// Source:       "/Users/aaronclaydon/Downloads/GTFS_Realtime.zip",
			UnpackBundle: datasets.BundleFormatNone,
			SupportedObjects: datasets.SupportedObjects{
				Operators: true,
				Stops:     true,
				Services:  true,
				Journeys:  true,
			},
		},
		{
			Identifier: "ie-gtfs-realtime",
			Format:     datasets.DataSetFormatGTFSRealtime,
			Provider: datasets.Provider{
				Name:    "Transport for Ireland",
				Website: "https://www.transportforireland.ie",
			},
			Source: "https://api.nationaltransport.ie/gtfsr/v2/gtfsr",
			// Source:       "/Users/aaronclaydon/Downloads/GTFS_Realtime.zip",
			UnpackBundle: datasets.BundleFormatNone,
			SupportedObjects: datasets.SupportedObjects{
				RealtimeJourneys: true,
			},
			ImportDestination: datasets.ImportDestinationRealtimeQueue,
			LinkedDataset:     "ie-gtfs-schedule",

			DownloadHandler: func(r *http.Request) {
				env := util.GetEnvironmentVariables()
				if env["TRAVIGO_IE_NATIONALTRANSPORT_API_KEY"] == "" {
					log.Fatal().Msg("TRAVIGO_IE_NATIONALTRANSPORT_API_KEY must be set")
				}

				r.Header.Set("x-api-key", env["TRAVIGO_IE_NATIONALTRANSPORT_API_KEY"])
			},
		},
		{
			Identifier: "us-nyc-subway-schedule",
			Format:     datasets.DataSetFormatGTFSSchedule,
			Provider: datasets.Provider{
				Name:    "Metropolitan Transportation Authority",
				Website: "https://mta.info",
			},
			Source:       "http://web.mta.info/developers/data/nyct/subway/google_transit.zip",
			UnpackBundle: datasets.BundleFormatNone,
			SupportedObjects: datasets.SupportedObjects{
				Operators: true,
				Stops:     true,
				Services:  true,
				Journeys:  true,
			},
		},
		// {
		// 	Identifier: "us-nyc-subway-relatime-1-2-3-4-5-6-7",
		// 	Format:     datasets.DataSetFormatGTFSRealtime,
		// 	Provider: datasets.Provider{
		// 		Name:    "Metropolitan Transportation Authority",
		// 		Website: "https://mta.info",
		// 	},
		// 	Source:       "https://api-endpoint.mta.info/Dataservice/mtagtfsfeeds/nyct%2Fgtfs",
		// 	UnpackBundle: datasets.BundleFormatNone,
		// 	SupportedObjects: datasets.SupportedObjects{
		// 		RealtimeJourneys: true,
		// 	},
		// 	ImportDestination: datasets.ImportDestinationRealtimeQueue,
		// 	LinkedDataset:     "us-nyc-subway-schedule",

		// 	DownloadHandler: func(r *http.Request) {
		// 		env := util.GetEnvironmentVariables()
		// 		if env["TRAVIGO_US_NYC_MTA_API_KEY"] == "" {
		// 			log.Fatal().Msg("TRAVIGO_US_NYC_MTA_API_KEY must be set")
		// 		}

		// 		r.Header.Set("x-api-key", env["TRAVIGO_US_NYC_MTA_API_KEY"])
		// 	},
		// },
		{
			Identifier: "eu-flixbus-gtfs-schedule",
			Format:     datasets.DataSetFormatGTFSSchedule,
			Provider: datasets.Provider{
				Name:    "FlixBus",
				Website: "https://global.flixbus.com",
			},
			Source:       "http://gtfs.gis.flix.tech/gtfs_generic_eu.zip",
			UnpackBundle: datasets.BundleFormatNone,
			SupportedObjects: datasets.SupportedObjects{
				Operators: false,
				Stops:     false,
				Services:  false,
				Journeys:  true,
			},
		},
		{
			Identifier: "us-bart-gtfs-schedule",
			Format:     datasets.DataSetFormatGTFSSchedule,
			Provider: datasets.Provider{
				Name:    "BART",
				Website: "http://www.bart.gov",
			},
			Source:       "https://www.bart.gov/dev/schedules/google_transit.zip",
			UnpackBundle: datasets.BundleFormatNone,
			SupportedObjects: datasets.SupportedObjects{
				Operators: true,
				Stops:     true,
				Services:  true,
				Journeys:  true,
			},
		},
		{
			Identifier: "us-bart-gtfs-realtime",
			Format:     datasets.DataSetFormatGTFSRealtime,
			Provider: datasets.Provider{
				Name:    "BART",
				Website: "http://www.bart.gov",
			},
			Source:       "https://api.bart.gov/gtfsrt/tripupdate.aspx", // https://api.bart.gov/gtfsrt/alerts.aspx
			UnpackBundle: datasets.BundleFormatNone,
			SupportedObjects: datasets.SupportedObjects{
				RealtimeJourneys: true,
			},
			ImportDestination: datasets.ImportDestinationRealtimeQueue,
			LinkedDataset:     "us-bart-gtfs-schedule",
		},
		{
			Identifier: "fr-ilevia-lille-gtfs-schedule",
			Format:     datasets.DataSetFormatGTFSSchedule,
			Provider: datasets.Provider{
				Name:    "Ilévia",
				Website: "http://www.ilevia.fr",
			},
			Source:       "https://opendata.lillemetropole.fr/api/datasets/1.0/transport_arret_transpole-point/alternative_exports/gtfszip",
			UnpackBundle: datasets.BundleFormatNone,
			SupportedObjects: datasets.SupportedObjects{
				Operators: true,
				Stops:     true,
				Services:  true,
				Journeys:  true,
			},
		},
		{
			Identifier: "fr-ilevia-lille-gtfs-realtime",
			Format:     datasets.DataSetFormatGTFSRealtime,
			Provider: datasets.Provider{
				Name:    "Ilévia",
				Website: "http://www.ilevia.fr",
			},
			Source:       "https://proxy.transport.data.gouv.fr/resource/ilevia-lille-gtfs-rt",
			UnpackBundle: datasets.BundleFormatNone,
			SupportedObjects: datasets.SupportedObjects{
				RealtimeJourneys: true,
			},
			ImportDestination: datasets.ImportDestinationRealtimeQueue,
			LinkedDataset:     "fr-ilevia-lille-gtfs-schedule",
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
