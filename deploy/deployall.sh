#!/bin/bash
helm upgrade -i travigo-data-importer ./deploy/charts/travigo-data-importer
helm upgrade -i travigo-realtime ./deploy/charts/travigo-realtime
helm upgrade -i travigo-web-api ./deploy/charts/travigo-web-api
helm upgrade -i travigo-stats ./deploy/charts/travigo-stats
helm upgrade -i travigo-events ./deploy/charts/travigo-consumer -f ./deploy/events.yaml
helm upgrade -i travigo-notify ./deploy/charts/travigo-consumer -f ./deploy/notify.yaml
helm upgrade -i travigo-dbwatch ./deploy/charts/travigo-dbwatch