#!/usr/bin/env python3
"""Publish host disk and heartbeat metrics to OCI Monitoring."""

import json
import os
import urllib.request
from datetime import datetime, timezone

import oci
from oci.auth.signers import InstancePrincipalsSecurityTokenSigner
from oci.monitoring.models import Datapoint, MetricData, PostMetricDataDetails


METADATA_URL = "http://169.254.169.254/opc/v2/instance/"


def read_instance_id():
    request = urllib.request.Request(METADATA_URL, headers={"Authorization": "Bearer Oracle"})
    with urllib.request.urlopen(request, timeout=3) as response:
        return json.load(response)["id"]


def used_percent(path):
    values = os.statvfs(path)
    total = values.f_blocks * values.f_frsize
    available = values.f_bavail * values.f_frsize
    if total <= 0:
        raise RuntimeError("filesystem reports no capacity")
    return round(100 * (total - available) / total, 2)


def main():
    region = os.environ["OCI_REGION"]
    compartment = os.environ["INSPECTION_COMPARTMENT_OCID"]
    instance_id = read_instance_id()
    signer = InstancePrincipalsSecurityTokenSigner()
    client = oci.monitoring.MonitoringClient(
        {"region": region},
        signer=signer,
        service_endpoint=f"https://telemetry-ingestion.{region}.oraclecloud.com",
    )
    now = datetime.now(timezone.utc)

    def metric(name, value, dimensions):
        return MetricData(
            compartment_id=compartment,
            namespace="inspection_host",
            name=name,
            dimensions={"instanceId": instance_id, **dimensions},
            datapoints=[Datapoint(timestamp=now, value=value)],
        )

    details = PostMetricDataDetails(
        metric_data=[
            metric("DataVolumeUsedPercent", used_percent("/srv/inspection"), {"mountpoint": "/srv/inspection"}),
            metric("HostHeartbeat", 1, {"host": "inspection-runtime"}),
        ]
    )
    client.post_metric_data(details)


if __name__ == "__main__":
    main()
