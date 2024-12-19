"""
This is an example dag for using the KubernetesPodOperator.
"""

from kubernetes.client import models as k8s
from airflow import DAG
from airflow.providers.cncf.kubernetes.operators.kubernetes_pod import KubernetesPodOperator
from airflow.utils.dates import days_ago
from airflow.hooks.base_hook import BaseHook
from airflow.providers.slack.notifications.slack_webhook import send_slack_webhook_notification

import yaml

from pathlib import Path

default_args = {
    'owner': 'airflow'
}

def generate_data_job(dataset : str, instance_size : str = "small"):
    return generate_job(dataset, ["data-importer", "dataset", "--id", dataset], instance_size=instance_size)

def generate_job(name : str, command : str, instance_size : str = "small"):
    name = f"data-import-{name}"

    tolerations = []
    node_selector = None
    container_resources = None
    if instance_size == "large":
        node_selector = {"cloud.google.com/gke-nodepool": "batch-burst-node-pool"}
        tolerations.append(k8s.V1Toleration(effect="NoSchedule", key="BATCH_BURST", operator="Equal", value="true"))
        container_resources = k8s.V1ResourceRequirements(requests={"memory": "40Gi"})

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
      tolerations=tolerations,
      node_selector=node_selector,
      container_resources=container_resources,
      trigger_rule="all_done",
    #   on_success_callback=[
    #     send_slack_webhook_notification(
    #         slack_webhook_conn_id="slack-dataimport",
    #         text="The task {{ ti.task_id }} was successful",
    #     )
    #   ],
      on_failure_callback=[
        send_slack_webhook_notification(
            slack_webhook_conn_id="slack-dataimport",
            text="The task {{ ti.task_id }} failed",
        )
      ],
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
    max_active_runs=1,
    concurrency=2,
) as dag:
    stop_linker = generate_job("stop-linker", [ "data-linker", "run", "--type", "stops" ])
    stop_indexer = generate_job("stop-indexer", [ "indexer", "stops" ])

    stop_linker >> stop_indexer

    pathlist = Path("/opt/airflow/dags/repo/data/datasources").glob('**/*.yaml')
    for path in pathlist:
        # because path is object not string
        path_in_str = str(path)   
        
        with open(path_in_str) as stream:
            try:
                yaml_file = yaml.safe_load(stream)

                source_identifier = yaml_file["identifier"]

                for dataset in yaml_file["datasets"]:
                    if "importdestination" in dataset and dataset["importdestination"] == "realtime-queue":
                        continue

                    dataset_identifier = dataset["identifier"]

                    dataset_size = "small"
                    if "datasetsize" in dataset:
                        dataset_size = dataset["datasetsize"]

                    import_job = generate_data_job(f"{source_identifier}-{dataset_identifier}", instance_size=dataset_size)

                    import_job >> stop_linker
            except yaml.YAMLError as exc:
                print(exc)
