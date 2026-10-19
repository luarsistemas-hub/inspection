import importlib.util
import pathlib
import sys
import tempfile
import types
import unittest
from unittest import mock


SCRIPT = pathlib.Path(__file__).resolve().parents[1] / "host/publish-metrics.py"


def load_collector():
    class Model:
        def __init__(self, **kwargs):
            self.__dict__.update(kwargs)

    class MetricData(Model):
        def __init__(self, **kwargs):
            if "datapoints" in kwargs:
                raise TypeError("Unrecognized keyword arguments: datapoints")
            super().__init__(**kwargs)

    oci = types.ModuleType("oci")
    oci.auth = types.ModuleType("oci.auth")
    oci.auth.signers = types.ModuleType("oci.auth.signers")
    oci.auth.signers.InstancePrincipalsSecurityTokenSigner = object
    oci.monitoring = types.ModuleType("oci.monitoring")
    oci.monitoring.models = types.ModuleType("oci.monitoring.models")
    oci.monitoring.models.Datapoint = Model
    oci.monitoring.models.MetricData = MetricData
    oci.monitoring.models.MetricDataDetails = Model
    oci.monitoring.models.PostMetricDataDetails = Model
    old = {name: sys.modules.get(name) for name in ("oci", "oci.auth", "oci.auth.signers", "oci.monitoring", "oci.monitoring.models")}
    sys.modules.update({"oci": oci, "oci.auth": oci.auth, "oci.auth.signers": oci.auth.signers,
                        "oci.monitoring": oci.monitoring, "oci.monitoring.models": oci.monitoring.models})
    try:
        spec = importlib.util.spec_from_file_location("inspection_publish_metrics_test", SCRIPT)
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        return module
    finally:
        for name, value in old.items():
            if value is None:
                sys.modules.pop(name, None)
            else:
                sys.modules[name] = value


class PublishMetricsTests(unittest.TestCase):
    def setUp(self):
        self.module = load_collector()
        self.temp = tempfile.TemporaryDirectory(prefix="inspection-metrics-")
        self.addCleanup(self.temp.cleanup)
        root = pathlib.Path(self.temp.name)
        self.module.RECEIPT_PATH = root / "current.env"
        self.module.METRICS_TOKEN_PATH = root / "token"
        self.module.METRICS_TOKEN_PATH.write_text("secret")
        self.module.PROVIDER_STATE_PATH = root / "provider.json"
        self.module.API_HISTOGRAM_STATE_PATH = root / "api.json"

    def test_video_absent_does_not_emit_queue_or_scrape_alarm_metric(self):
        self.module.RECEIPT_PATH.write_text("API_IMAGE=image\n")
        metrics = self.module.video_metrics()
        self.assertNotIn("VideoOldestRunnableAgeSeconds", metrics)
        self.assertNotIn("VideoMetricsScrapeOk", metrics)

    def test_video_scrape_failure_is_explicit_and_never_zeroes_queue(self):
        self.module.RECEIPT_PATH.write_text("VIDEO_WORKER_IMAGE=image\n")
        with mock.patch.object(self.module.urllib.request, "urlopen", side_effect=OSError("offline")):
            metrics = self.module.video_metrics()
        self.assertEqual(metrics["VideoMetricsScrapeOk"], 0.0)
        self.assertNotIn("VideoOldestRunnableAgeSeconds", metrics)
        self.assertNotIn("VideoExpirationLagSeconds", metrics)

    def test_video_scrape_requires_both_worker_and_scheduler(self):
        self.module.RECEIPT_PATH.write_text("VIDEO_WORKER_IMAGE=image\n")
        class Response:
            def __enter__(self): return self
            def __exit__(self, *_args): return False
            def read(self): return b"inspection_video_oldest_runnable_age_seconds 0\n"
        with mock.patch.object(self.module.os.path, "isdir", return_value=True), \
             mock.patch.object(self.module.os, "statvfs", return_value=types.SimpleNamespace(f_bavail=9, f_frsize=1024)), \
             mock.patch.object(self.module.urllib.request, "urlopen", side_effect=(Response(), OSError("scheduler offline"))):
            metrics = self.module.video_metrics()
        self.assertEqual(metrics["VideoMetricsScrapeOk"], 0.0)
        self.assertEqual(metrics["VideoOldestRunnableAgeSeconds"], 0.0)

    def test_api_scrape_failure_is_explicit(self):
        with mock.patch.object(self.module.urllib.request, "urlopen", side_effect=OSError("API offline")):
            metrics = self.module.api_metrics()
        self.assertEqual(metrics, {"ApiMetricsScrapeOk": 0.0})

    def test_video_success_exports_observed_queue_metrics(self):
        self.module.RECEIPT_PATH.write_text("VIDEO_WORKER_IMAGE=image\n")
        body = "inspection_video_oldest_runnable_age_seconds 12\ninspection_video_expiration_lag_seconds 4\n"
        class Response:
            def __enter__(self): return self
            def __exit__(self, *_args): return False
            def read(self): return body.encode()
        stat = types.SimpleNamespace(f_bavail=10, f_frsize=1024)
        with mock.patch.object(self.module.os.path, "isdir", return_value=True), \
             mock.patch.object(self.module.os, "statvfs", return_value=stat), \
             mock.patch.object(self.module.urllib.request, "urlopen", return_value=Response()):
            metrics = self.module.video_metrics()
        self.assertEqual(metrics["VideoMetricsScrapeOk"], 1.0)
        self.assertEqual(metrics["VideoOldestRunnableAgeSeconds"], 12.0)
        self.assertEqual(metrics["VideoExpirationLagSeconds"], 4.0)

    def test_metrics_endpoints_and_scratch_path_match_host_ports(self):
        self.assertEqual(self.module.VIDEO_METRICS_URL, "http://127.0.0.1:8091/metrics")
        self.assertEqual(self.module.SCHEDULER_METRICS_URL, "http://127.0.0.1:8092/metrics")
        self.assertEqual(self.module.API_METRICS_URL, "http://127.0.0.1:8093/metrics")
        self.assertIn("/srv/inspection/video-scratch", SCRIPT.read_text())

    def test_main_submits_oci_ingestion_metric_details(self):
        submitted = []

        class Client:
            def __init__(self, *_args, **_kwargs):
                pass

            def post_metric_data(self, details):
                submitted.append(details)

        with mock.patch.dict(self.module.os.environ, {"OCI_REGION": "us-ashburn-1", "INSPECTION_COMPARTMENT_OCID": "test-compartment"}), \
             mock.patch.object(self.module, "read_instance_id", return_value="test-instance"), \
             mock.patch.object(self.module, "used_percent", return_value=25.0), \
             mock.patch.object(self.module, "video_metrics", return_value={"VideoMetricsScrapeOk": 1.0}), \
             mock.patch.object(self.module, "api_metrics", return_value={"ApiMetricsScrapeOk": 1.0}), \
             mock.patch.object(self.module.oci.monitoring, "MonitoringClient", Client, create=True):
            self.module.main()

        self.assertEqual(len(submitted), 1)
        metrics = submitted[0].metric_data
        self.assertEqual({metric.name for metric in metrics}, {"DataVolumeUsedPercent", "HostHeartbeat", "VideoMetricsScrapeOk", "ApiMetricsScrapeOk"})
        self.assertTrue(all(metric.datapoints[0].value is not None for metric in metrics))


if __name__ == "__main__":
    unittest.main()
