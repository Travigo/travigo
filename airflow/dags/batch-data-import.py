"""
This is an example dag for using the KubernetesPodOperator.
"""

from kubernetes.client import models as k8s
from airflow import DAG
from airflow.providers.cncf.kubernetes.operators.kubernetes_pod import KubernetesPodOperator
from airflow.utils.dates import days_ago
from airflow.hooks.base_hook import BaseHook
from airflow.contrib.operators.slack_webhook_operator import SlackWebhookOperator

import yaml

default_args = {
    'owner': 'airflow'
}

def generate_data_job(dataset : str):
    return generate_job(dataset, ["data-importer", "dataset", "--id", dataset])

def generate_job(name : str, command : str):
    name = f"data-import-{name}"

    k = KubernetesPodOperator(
      namespace='default',
      image='ghcr.io/travigo/travigo:main',
      image_pull_policy='Always',
      arguments=command,
      name=name,
      task_id=name,
      is_delete_operator_pod=True,
      hostnetwork=False,
      startup_timeout_seconds=1000,
      env_vars = [
        k8s.V1EnvVar(
            name = "TRAVIGO_BODS_API_KEY",
            value_from = k8s.V1EnvVarSource(secret_key_ref=k8s.V1SecretKeySelector(name="travigo-bods-api", key="api_key"))
        ),
        k8s.V1EnvVar(
            name = "TRAVIGO_IE_NATIONALTRANSPORT_API_KEY",
            value_from = k8s.V1EnvVarSource(secret_key_ref=k8s.V1SecretKeySelector(name="travigo-ie-nationaltransport-api", key="api_key"))
        ),
        k8s.V1EnvVar(
            name = "TRAVIGO_NATIONALRAIL_USERNAME",
            value_from = k8s.V1EnvVarSource(secret_key_ref=k8s.V1SecretKeySelector(name="travigo-nationalrail-credentials", key="username"))
        ),
        k8s.V1EnvVar(
            name = "TRAVIGO_NATIONALRAIL_PASSWORD",
            value_from = k8s.V1EnvVarSource(secret_key_ref=k8s.V1SecretKeySelector(name="travigo-nationalrail-credentials", key="password"))
        ),
        k8s.V1EnvVar(
            name = "TRAVIGO_NETWORKRAIL_USERNAME",
            value_from = k8s.V1EnvVarSource(secret_key_ref=k8s.V1SecretKeySelector(name="travigo-networkrail-credentials", key="username"))
        ),
        k8s.V1EnvVar(
            name = "TRAVIGO_NETWORKRAIL_PASSWORD",
            value_from = k8s.V1EnvVarSource(secret_key_ref=k8s.V1SecretKeySelector(name="travigo-networkrail-credentials", key="password"))
        ),
        k8s.V1EnvVar(
            name = "TRAVIGO_SE_TRAFIKLAB_STATIC_API_KEY",
            value_from = k8s.V1EnvVarSource(secret_key_ref=k8s.V1SecretKeySelector(name="travigo-trafiklab-sweden-static", key="api_key"))
        ),
        k8s.V1EnvVar(
            name = "TRAVIGO_SE_TRAFIKLAB_REALTIME_API_KEY",
            value_from = k8s.V1EnvVarSource(secret_key_ref=k8s.V1SecretKeySelector(name="travigo-trafiklab-sweden-realtime", key="api_key"))
        ),

        k8s.V1EnvVar(
            name = "TRAVIGO_MONGODB_CONNECTION",
            value_from = k8s.V1EnvVarSource(secret_key_ref=k8s.V1SecretKeySelector(name="travigo-mongodb-admin-travigo", key="connectionString.standard"))
        ),
        k8s.V1EnvVar(
            name = "TRAVIGO_ELASTICSEARCH_ADDRESS",
            value = "https://primary-es-http.elastic:9200"
        ),
        k8s.V1EnvVar(
            name = "TRAVIGO_ELASTICSEARCH_USERNAME",
            value_from = k8s.V1EnvVarSource(secret_key_ref=k8s.V1SecretKeySelector(name="travigo-elasticsearch-user", key="username"))
        ),
        k8s.V1EnvVar(
            name = "TRAVIGO_ELASTICSEARCH_PASSWORD",
            value_from = k8s.V1EnvVarSource(secret_key_ref=k8s.V1SecretKeySelector(name="travigo-elasticsearch-user", key="password"))
        ),
        k8s.V1EnvVar(
            name = "TRAVIGO_REDIS_ADDRESS",
            value = "redis-headless.redis:6379"
        ),
        k8s.V1EnvVar(
            name = "TRAVIGO_REDIS_PASSWORD",
            value_from = k8s.V1EnvVarSource(secret_key_ref=k8s.V1SecretKeySelector(name="redis-password", key="password"))
        )
      ]
    )

    return k

with DAG(
    dag_id='batch-data-import',
    default_args=default_args,
    schedule_interval="0 7 * * *",
    start_date=days_ago(2),
    catchup=False,
) as dag:
    ie = generate_data_job("ie-tfi-gtfs-schedule")
    fr = generate_data_job("fr-ilevia-lille-gtfs-schedule")

    stop_linker = generate_job("stop-linker", [ "data-linker", "run", "--type", "stops" ])

    ie >> stop_linker
    fr >> stop_linker

    with open("../../data/datasources/gb-dft.yaml") as stream:
        try:
            print(yaml.safe_load(stream))
            noc = generate_data_job("gb-traveline-noc")

            noc >> ie
            noc >> fr
        except yaml.YAMLError as exc:
            print(exc)