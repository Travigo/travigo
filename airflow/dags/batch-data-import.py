"""
This is an example dag for using the KubernetesPodOperator.
"""

from kubernetes.client import models as k8s
from airflow import DAG
from airflow.providers.cncf.kubernetes.operators.kubernetes_pod import KubernetesPodOperator
from airflow.utils.dates import days_ago
from airflow.hooks.base_hook import BaseHook
from airflow.contrib.operators.slack_webhook_operator import SlackWebhookOperator

default_args = {
    'owner': 'airflow'
}

with DAG(
    dag_id='batch-data-import',
    default_args=default_args,
    schedule_interval="0 7 * * *",
    start_date=days_ago(2),
    catchup=False,
) as dag:
    k = KubernetesPodOperator(
      namespace='default',
      image='ghcr.io/travigo/travigo:main',
      image_pull_policy='Always',
      arguments=["data-importer", "dataset", "--id", "ie-tfi-gtfs-schedule"],
      name="data-import",
      task_id="task",
      is_delete_operator_pod=True,
      hostnetwork=False,
      startup_timeout_seconds=1000,
      env_vars = [
        k8s.V1EnvVar(
            name = "TRAVIGO_IE_NATIONALTRANSPORT_API_KEY"
            value_from = k8s.V1EnvVarSource(secret_key_ref="travigo-ie-nationaltransport-api", field_ref="api_key")
        ),
        k8s.V1EnvVar(
            name = "TRAVIGO_MONGODB_CONNECTION"
            value_from = k8s.V1EnvVarSource(secret_key_ref="travigo-mongodb-admin-travigo", field_ref="connectionString.standard")
        ),
        k8s.V1EnvVar(
            name = "TRAVIGO_ELASTICSEARCH_ADDRESS"
            value = "https://primary-es-http.elastic:9200"
        ),
        k8s.V1EnvVar(
            name = "TRAVIGO_ELASTICSEARCH_USERNAME"
            value_from = k8s.V1EnvVarSource(secret_key_ref="travigo-elasticsearch-user", field_ref="username")
        ),
        k8s.V1EnvVar(
            name = "TRAVIGO_ELASTICSEARCH_PASSWORD"
            value_from = k8s.V1EnvVarSource(secret_key_ref="travigo-elasticsearch-user", field_ref="password")
        ),
        k8s.V1EnvVar(
            name = "TRAVIGO_REDIS_ADDRESS"
            value = "redis-headless.redis:6379"
        ),
        k8s.V1EnvVar(
            name = "TRAVIGO_REDIS_PASSWORD"
            value_from = k8s.V1EnvVarSource(secret_key_ref="redis-password", field_ref="password")
        )
      ]
    )
