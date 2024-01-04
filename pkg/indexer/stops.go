package indexer

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"

	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/bson"

	"github.com/elastic/go-elasticsearch/v8/esapi"
	"github.com/kr/pretty"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/elastic_client"
)

func IndexStops() {
	createStopIndex()
	indexStopsFromMongo()
}

func createStopIndex() {
	mapping := `{
		"settings": {
			"number_of_shards": 1,
			"number_of_replicas": 1
		},
		"mappings": {
			"properties": {
				"Active": {
					"type": "boolean"
				},
				"Associations": {
					"properties": {
						"AssociatedIdentifier": {
							"type": "text",
							"fields": {
								"keyword": {
									"type": "keyword",
									"ignore_above": 256
								}
							}
						},
						"Type": {
							"type": "text",
							"fields": {
								"keyword": {
									"type": "keyword",
									"ignore_above": 256
								}
							}
						}
					}
				},
				"CreationDatetime": {
					"type": "date"
				},
				"Datasource": {
					"properties": {
						"Dataset": {
							"type": "text",
							"fields": {
								"keyword": {
									"type": "keyword",
									"ignore_above": 256
								}
							}
						},
						"Identifier": {
							"type": "date"
						},
						"OriginalFormat": {
							"type": "text",
							"fields": {
								"keyword": {
									"type": "keyword",
									"ignore_above": 256
								}
							}
						},
						"Provider": {
							"type": "text",
							"fields": {
								"keyword": {
									"type": "keyword",
									"ignore_above": 256
								}
							}
						}
					}
				},
				"Entrances": {
					"properties": {
						"Location": {
							"properties": {
								"coordinates": {
									"type": "float"
								},
								"type": {
									"type": "text",
									"fields": {
										"keyword": {
											"type": "keyword",
											"ignore_above": 256
										}
									}
								}
							}
						},
						"OtherIdentifiers": {
							"properties": {
								"AtcoCode": {
									"type": "text",
									"fields": {
										"keyword": {
											"type": "keyword",
											"ignore_above": 256
										}
									}
								},
								"NaptanCode": {
									"type": "text",
									"fields": {
										"keyword": {
											"type": "keyword",
											"ignore_above": 256
										}
									}
								}
							}
						},
						"OtherNames": {
							"properties": {
								"Indicator": {
									"type": "text",
									"fields": {
										"keyword": {
											"type": "keyword",
											"ignore_above": 256
										}
									}
								},
								"Landmark": {
									"type": "text",
									"fields": {
										"keyword": {
											"type": "keyword",
											"ignore_above": 256
										}
									}
								},
								"ShortCommonName": {
									"type": "text",
									"fields": {
										"keyword": {
											"type": "keyword",
											"ignore_above": 256
										}
									}
								},
								"Street": {
									"type": "text",
									"fields": {
										"keyword": {
											"type": "keyword",
											"ignore_above": 256
										}
									}
								}
							}
						},
						"PrimaryIdentifier": {
							"type": "text",
							"fields": {
								"keyword": {
									"type": "keyword",
									"ignore_above": 256
								}
							}
						},
						"PrimaryName": {
							"type": "text",
							"fields": {
								"keyword": {
									"type": "keyword",
									"ignore_above": 256
								}
							}
						}
					}
				},
				"Location": {
					"properties": {
						"coordinates": {
							"type": "float"
						},
						"type": {
							"type": "text",
							"fields": {
								"keyword": {
									"type": "keyword",
									"ignore_above": 256
								}
							}
						}
					}
				},
				"ModificationDatetime": {
					"type": "date"
				},
				"OtherIdentifiers": {
					"properties": {
						"AtcoCode": {
							"type": "text",
							"fields": {
								"keyword": {
									"type": "keyword",
									"ignore_above": 256
								}
							}
						},
						"Crs": {
							"type": "text",
							"fields": {
								"keyword": {
									"type": "keyword",
									"ignore_above": 256
								}
							}
						},
						"NaptanCode": {
							"type": "text",
							"fields": {
								"keyword": {
									"type": "keyword",
									"ignore_above": 256
								}
							}
						},
						"STANOX": {
							"type": "text",
							"fields": {
								"keyword": {
									"type": "keyword",
									"ignore_above": 256
								}
							}
						},
						"Tiploc": {
							"type": "text",
							"fields": {
								"keyword": {
									"type": "keyword",
									"ignore_above": 256
								}
							}
						}
					}
				},
				"OtherNames": {
					"properties": {
						"Indicator": {
							"type": "text",
							"fields": {
								"keyword": {
									"type": "keyword",
									"ignore_above": 256
								}
							}
						},
						"Landmark": {
							"type": "text",
							"fields": {
								"keyword": {
									"type": "keyword",
									"ignore_above": 256
								}
							}
						},
						"ShortCommonName": {
							"type": "text",
							"fields": {
								"keyword": {
									"type": "keyword",
									"ignore_above": 256
								}
							}
						},
						"Street": {
							"type": "text",
							"fields": {
								"keyword": {
									"type": "keyword",
									"ignore_above": 256
								}
							}
						}
					}
				},
				"Platforms": {
					"properties": {
						"Location": {
							"properties": {
								"coordinates": {
									"type": "float"
								},
								"type": {
									"type": "text",
									"fields": {
										"keyword": {
											"type": "keyword",
											"ignore_above": 256
										}
									}
								}
							}
						},
						"OtherIdentifiers": {
							"properties": {
								"AtcoCode": {
									"type": "text",
									"fields": {
										"keyword": {
											"type": "keyword",
											"ignore_above": 256
										}
									}
								},
								"NaptanCode": {
									"type": "text",
									"fields": {
										"keyword": {
											"type": "keyword",
											"ignore_above": 256
										}
									}
								}
							}
						},
						"OtherNames": {
							"properties": {
								"Indicator": {
									"type": "text",
									"fields": {
										"keyword": {
											"type": "keyword",
											"ignore_above": 256
										}
									}
								},
								"Landmark": {
									"type": "text",
									"fields": {
										"keyword": {
											"type": "keyword",
											"ignore_above": 256
										}
									}
								},
								"ShortCommonName": {
									"type": "text",
									"fields": {
										"keyword": {
											"type": "keyword",
											"ignore_above": 256
										}
									}
								},
								"Street": {
									"type": "text",
									"fields": {
										"keyword": {
											"type": "keyword",
											"ignore_above": 256
										}
									}
								}
							}
						},
						"PrimaryIdentifier": {
							"type": "text",
							"fields": {
								"keyword": {
									"type": "keyword",
									"ignore_above": 256
								}
							}
						},
						"PrimaryName": {
							"type": "text",
							"fields": {
								"keyword": {
									"type": "keyword",
									"ignore_above": 256
								}
							}
						}
					}
				},
				"PrimaryIdentifier": {
					"type": "text",
					"fields": {
						"keyword": {
							"type": "keyword",
							"ignore_above": 256
						}
					}
				},
				"PrimaryName": {
					"type": "text",
					"fields": {
						"keyword": {
							"type": "keyword",
							"ignore_above": 256
						},
						"search_as_you_type": {
							"type": "search_as_you_type"
						}
					}
				},
				"TransportTypes": {
					"type": "text",
					"fields": {
						"keyword": {
							"type": "keyword",
							"ignore_above": 256
						}
					}
				}
			}
		}
	}`

	indexReq := esapi.IndicesCreateRequest{
		Index: "travigo-stops-001",
		Body:  strings.NewReader(string(mapping)),
	}

	resp, err := indexReq.Do(context.Background(), elastic_client.Client)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create index")
	}

	responseBytes, _ := io.ReadAll(resp.Body)
	pretty.Println(string(responseBytes))
}

func indexStopsFromMongo() {
	stops_collection := database.GetCollection("stops")

	cursor, _ := stops_collection.Find(context.Background(), bson.M{})

	for cursor.Next(context.TODO()) {
		var stop ctdf.Stop
		cursor.Decode(&stop)

		jsonStop, _ := json.Marshal(stop)

		elastic_client.IndexRequest("travigo-stops-001", bytes.NewReader(jsonStop))
	}

	log.Info().Msg("Sent all index requests to queue")
}
