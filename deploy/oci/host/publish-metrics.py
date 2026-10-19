#!/usr/bin/env python3
"""Publish host disk and video worker operational metrics to OCI Monitoring."""

import json
import os
import re
import urllib.request
from pathlib import Path
from datetime import datetime, timezone
import time

import oci
from oci.auth.signers import InstancePrincipalsSecurityTokenSigner
from oci.monitoring.models import Datapoint, MetricData, PostMetricDataDetails


METADATA_URL = "http://169.254.169.254/opc/v2/instance/"
VIDEO_METRICS_URL = "http://127.0.0.1:8091/metrics"
SCHEDULER_METRICS_URL = "http://127.0.0.1:8092/metrics"
API_METRICS_URL = "http://127.0.0.1:8080/metrics"
# F5.8 will publish the video-worker container on host loopback port 8091.
PROVIDER_STATE_PATH = Path("/var/lib/inspection/video-provider-failures.json")
API_HISTOGRAM_STATE_PATH = Path("/var/lib/inspection/api-http-histogram.json")


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


def video_metrics():
    result = {}
    scratch_path = "/var/lib/docker/volumes/inspection_inspection_video_scratch/_data"
    if os.path.isdir(scratch_path):
        values = os.statvfs(scratch_path)
        result["VideoScratchFreeBytes"] = values.f_bavail * values.f_frsize
    try:
        token_path = Path("/etc/inspection/secrets.d/INSPECTION_METRICS_TOKEN")
        token = token_path.read_text().strip()
    except (OSError, ValueError):
        return result
    result.update({"VideoOldestRunnableAgeSeconds": 0.0, "VideoExpirationLagSeconds": 0.0, "VideoWorkHeartbeatAgeSeconds": 1e9})
    provider_failures = 0.0
    heartbeat_timestamp = 0.0
    video_worker_scraped = False
    scheduler_scraped = False
    for endpoint in (VIDEO_METRICS_URL, SCHEDULER_METRICS_URL):
        try:
            request = urllib.request.Request(endpoint, headers={"Authorization": f"Bearer {token}"})
            with urllib.request.urlopen(request, timeout=5) as response:
                body = response.read().decode("utf-8")
        except (OSError, ValueError):
            continue
        if endpoint == VIDEO_METRICS_URL:
            video_worker_scraped = True
        else:
            scheduler_scraped = True
        for line in body.splitlines():
            match = re.match(r"^(inspection_video_oldest_runnable_age_seconds|inspection_video_expiration_lag_seconds|inspection_video_provider_config_failures_total|inspection_video_work_heartbeat_timestamp_seconds)(?:\{[^}]*\})?\s+([0-9.eE+-]+)$", line)
            if match:
                name, value = match.groups()
                numeric = float(value)
                if name == "inspection_video_oldest_runnable_age_seconds":
                    result["VideoOldestRunnableAgeSeconds"] = max(result["VideoOldestRunnableAgeSeconds"], numeric)
                elif name == "inspection_video_expiration_lag_seconds":
                    result["VideoExpirationLagSeconds"] = max(result["VideoExpirationLagSeconds"], numeric)
                elif name == "inspection_video_provider_config_failures_total":
                    provider_failures += numeric
                elif name == "inspection_video_work_heartbeat_timestamp_seconds":
                    heartbeat_timestamp = max(heartbeat_timestamp, numeric)
    if not scheduler_scraped:
        result["VideoExpirationLagSeconds"] = 1e9
    if video_worker_scraped:
        result["VideoWorkHeartbeatAgeSeconds"] = max(0.0, time.time() - heartbeat_timestamp) if heartbeat_timestamp else 1e9
        result["VideoProviderConfigFailures15m"] = provider_failure_window(provider_failures)
    else:
        result["VideoOldestRunnableAgeSeconds"] = 1e9
    return result


def graphql_request_p95(token):
    request = urllib.request.Request(API_METRICS_URL, headers={"Authorization": f"Bearer {token}"})
    with urllib.request.urlopen(request, timeout=5) as response:
        body = response.read().decode("utf-8")

    buckets = {}
    for line in body.splitlines():
        match = re.match(r'^inspection_http_request_duration_seconds_bucket\{([^}]*)\}\s+([0-9.eE+-]+)$', line)
        if not match:
            continue
        labels, raw_count = match.groups()
        route = re.search(r'(?:^|,)route="([^"]*)"(?:,|$)', labels)
        bound = re.search(r'(?:^|,)le="([^"]*)"(?:,|$)', labels)
        if route and route.group(1) == "graphql" and bound:
            value = float("inf") if bound.group(1) == "+Inf" else float(bound.group(1))
            buckets[value] = buckets.get(value, 0.0) + float(raw_count)

    try:
        previous = {float(bound): float(count) for bound, count in json.loads(API_HISTOGRAM_STATE_PATH.read_text()).items()}
    except (OSError, ValueError, TypeError):
        previous = {}
    reset = any(buckets.get(bound, 0.0) < count for bound, count in previous.items())
    deltas = {
        bound: count if reset else max(count - previous.get(bound, 0.0), 0.0)
        for bound, count in buckets.items()
    }
    API_HISTOGRAM_STATE_PATH.parent.mkdir(parents=True, exist_ok=True)
    API_HISTOGRAM_STATE_PATH.write_text(json.dumps({str(bound): count for bound, count in buckets.items()}))

    total = deltas.get(float("inf"), 0.0)
    if total <= 0:
        return None
    rank = total * 0.95
    previous_bound = 0.0
    previous_count = 0.0
    for bound in sorted(deltas):
        count = deltas[bound]
        if count >= rank:
            if bound == float("inf"):
                return previous_bound
            bucket_count = count - previous_count
            if bucket_count <= 0:
                return bound
            fraction = (rank - previous_count) / bucket_count
            return previous_bound + (bound - previous_bound) * fraction
        previous_bound, previous_count = bound, count
    return previous_bound


def api_metrics():
    try:
        token = Path("/etc/inspection/secrets.d/INSPECTION_METRICS_TOKEN").read_text().strip()
        value = graphql_request_p95(token)
    except (OSError, ValueError):
        return {}
    return {} if value is None else {"GraphQLRequestP95Seconds": value}


def provider_failure_window(current):
    now = time.time()
    try:
        previous = json.loads(PROVIDER_STATE_PATH.read_text())
    except (OSError, ValueError):
        previous = {"counter": 0.0, "events": []}
    last = float(previous.get("counter", 0.0))
    delta = current - last if current >= last else current
    events = [stamp for stamp in previous.get("events", []) if now - float(stamp) < 900]
    events.extend([now] * min(int(max(delta, 0)), 100))
    PROVIDER_STATE_PATH.parent.mkdir(parents=True, exist_ok=True)
    PROVIDER_STATE_PATH.write_text(json.dumps({"counter": current, "events": events}))
    return float(len(events))


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
            *[metric(name, value, {"component": "video-worker"}) for name, value in video_metrics().items()],
            *[metric(name, value, {"component": "api"}) for name, value in api_metrics().items()],
        ]
    )
    client.post_metric_data(details)


if __name__ == "__main__":
    main()
