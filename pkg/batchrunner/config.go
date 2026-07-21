package batchrunner

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Address                  string
	StoragePath              string
	Namespace                string
	JobImage                 string
	JobImagePullPolicy       string
	JobServiceAccountName    string
	JobBackoffLimit          int
	JobTTLSeconds            int
	JobActiveDeadlineSeconds int64
	AllowConcurrentRuns      bool

	BodsAPIKeySecret                string
	TfLAPIKeySecret                 string
	IENationalTransportAPIKeySecret string
	NationalRailCredentialsSecret   string
	NetworkRailCredentialsSecret    string
	TrafiklabStaticSecret           string
	TrafiklabRealtimeSecret         string
	OdptAPIKeySecret                string
	MongoDBConnectionSecret         string
	MongoDBDatabase                 string
	ElasticsearchAddress            string
	ElasticsearchUserSecret         string
	RedisAddress                    string
	RedisPasswordSecret             string
}

func ConfigFromEnv() Config {
	return Config{
		Address:                  envOrDefault("TRAVIGO_BATCH_ADDRESS", ":8080"),
		StoragePath:              envOrDefault("TRAVIGO_BATCH_STORAGE_PATH", "/batch-runs"),
		Namespace:                envOrDefault("TRAVIGO_BATCH_NAMESPACE", currentNamespace()),
		JobImage:                 envOrDefault("TRAVIGO_BATCH_JOB_IMAGE", "ghcr.io/travigo/travigo:main"),
		JobImagePullPolicy:       envOrDefault("TRAVIGO_BATCH_JOB_IMAGE_PULL_POLICY", "Always"),
		JobServiceAccountName:    envOrDefault("TRAVIGO_BATCH_JOB_SERVICE_ACCOUNT", "default"),
		JobBackoffLimit:          envIntOrDefault("TRAVIGO_BATCH_JOB_BACKOFF_LIMIT", 0),
		JobTTLSeconds:            envIntOrDefault("TRAVIGO_BATCH_JOB_TTL_SECONDS", 3600),
		JobActiveDeadlineSeconds: int64(envIntOrDefault("TRAVIGO_BATCH_JOB_ACTIVE_DEADLINE_SECONDS", 0)),
		AllowConcurrentRuns:      os.Getenv("TRAVIGO_BATCH_ALLOW_CONCURRENT_RUNS") == "true",

		BodsAPIKeySecret:                envOrDefault("TRAVIGO_BATCH_BODS_API_KEY_SECRET", "travigo-bods-api"),
		TfLAPIKeySecret:                 envOrDefault("TRAVIGO_BATCH_TFL_API_KEY_SECRET", "travigo-tfl-api-key"),
		IENationalTransportAPIKeySecret: envOrDefault("TRAVIGO_BATCH_IE_NATIONALTRANSPORT_API_KEY_SECRET", "travigo-ie-nationaltransport-api"),
		NationalRailCredentialsSecret:   envOrDefault("TRAVIGO_BATCH_NATIONALRAIL_CREDENTIALS_SECRET", "travigo-nationalrail-credentials"),
		NetworkRailCredentialsSecret:    envOrDefault("TRAVIGO_BATCH_NETWORKRAIL_CREDENTIALS_SECRET", "travigo-networkrail-credentials"),
		TrafiklabStaticSecret:           envOrDefault("TRAVIGO_BATCH_TRAFIKLAB_STATIC_SECRET", "travigo-trafiklab-sweden-static"),
		TrafiklabRealtimeSecret:         envOrDefault("TRAVIGO_BATCH_TRAFIKLAB_REALTIME_SECRET", "travigo-trafiklab-sweden-realtime"),
		OdptAPIKeySecret:                envOrDefault("TRAVIGO_BATCH_ODPT_API_KEY_SECRET", "travigo-odtp-japan-gtfs"),
		MongoDBConnectionSecret:         envOrDefault("TRAVIGO_BATCH_MONGODB_CONNECTION_SECRET", "travigo-mongodb-admin-travigo"),
		MongoDBDatabase:                 envOrDefault("TRAVIGO_BATCH_MONGODB_DATABASE", "travigo"),
		ElasticsearchAddress:            envOrDefault("TRAVIGO_BATCH_ELASTICSEARCH_ADDRESS", "https://primary-es-http.elastic:9200"),
		ElasticsearchUserSecret:         envOrDefault("TRAVIGO_BATCH_ELASTICSEARCH_USER_SECRET", "travigo-elasticsearch-user"),
		RedisAddress:                    envOrDefault("TRAVIGO_BATCH_REDIS_ADDRESS", "redis-headless.redis:6379"),
		RedisPasswordSecret:             envOrDefault("TRAVIGO_BATCH_REDIS_PASSWORD_SECRET", "redis-password"),
	}
}

func envOrDefault(name string, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func envIntOrDefault(name string, fallback int) int {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}

	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}

	return parsed
}

func currentNamespace() string {
	data, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err == nil && len(data) > 0 {
		return strings.TrimSpace(string(data))
	}

	return "default"
}
