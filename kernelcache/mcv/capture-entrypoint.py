#!/usr/bin/env python3
"""Run one capture after the runtime is ready and report its result."""

import json
import logging
import os
import ssl
import subprocess
import time
import urllib.error
import urllib.parse
import urllib.request
from datetime import datetime, timezone


logging.basicConfig(
    level=logging.INFO,
    format="%(levelname)s: %(message)s",
)
logger = logging.getLogger("mcv-capture")

REPORTER_ACCESS_FILE = os.environ.get(
    "MCV_REPORTER_ACCESS_FILE",
    os.environ.get("MCV_REPORTER_CREDENTIAL_FILE", ""),
)
RESULT_PATH = os.environ.get("MCV_RESULT_PATH", "/tmp/mcv/result.json")
SNAPSHOT_PATH = os.environ.get(
    "MCV_SNAPSHOT_PATH", "/tmp/mcv/cache-snapshot.json"
)
KUBERNETES_CA_FILE = os.environ.get(
    "MCV_KUBERNETES_CA_FILE",
    "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt",
)
RUNTIME_INFO_ENV = {
    "MCV_RUNTIME_COMMAND_HASH": "commandHash",
    "MCV_RUNTIME_ARGS_HASH": "argsHash",
    "MCV_RUNTIME_MODEL_URI_HASH": "modelURIHash",
    "MCV_TENSOR_PARALLEL_SIZE": "tensorParallelSize",
}


def timestamp():
    return datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")


def positive_integer(name, default):
    value = os.environ.get(name, "")
    try:
        return max(1, int(value))
    except ValueError:
        return default


def nonnegative_integer(name, default):
    value = os.environ.get(name, "")
    try:
        return max(0, int(value))
    except ValueError:
        return default


def wait_for_json_access(path, description):
    if not path:
        raise RuntimeError(f"{description} access file is required")
    logger.info("Waiting for %s access", description)
    while True:
        access = read_json_access(path)
        if access is not None and access.get("token"):
            logger.info("%s access is available", description)
            return access
        time.sleep(1)


def read_json_access(path):
    try:
        with open(path, encoding="utf-8") as file:
            access = json.load(file)
        if isinstance(access, dict):
            return access
    except (FileNotFoundError, json.JSONDecodeError):
        pass
    return None


def wait_for_readiness():
    if os.environ.get("MCV_READINESS_PROBE_TYPE") != "httpGet":
        raise RuntimeError("MCV_READINESS_PROBE_TYPE must be httpGet")

    scheme = os.environ.get("MCV_READINESS_PROBE_SCHEME", "http")
    host = os.environ.get("MCV_READINESS_PROBE_HOST", "127.0.0.1")
    port = os.environ.get("MCV_READINESS_PROBE_PORT", "")
    path = os.environ.get("MCV_READINESS_PROBE_PATH", "/")
    if not port:
        raise RuntimeError("MCV_READINESS_PROBE_PORT is required")

    initial_delay = nonnegative_integer("MCV_READINESS_PROBE_INITIAL_DELAY_SECONDS", 0)
    period = positive_integer("MCV_READINESS_PROBE_PERIOD_SECONDS", 10)
    timeout = positive_integer("MCV_READINESS_PROBE_TIMEOUT_SECONDS", 1)
    logger.info("Waiting for workload readiness probe")
    time.sleep(initial_delay)
    url = f"{scheme}://{host}:{port}{path}"
    while True:
        try:
            with urllib.request.urlopen(url, timeout=timeout):
                logger.info("Workload readiness probe succeeded")
                return
        except (urllib.error.URLError, TimeoutError):
            time.sleep(period)


def reporter_request(status):
    namespace = os.environ.get("MCV_CAPTURE_NAMESPACE", "")
    name = os.environ.get("MCV_CAPTURE_NAME", "")
    access = read_json_access(REPORTER_ACCESS_FILE)
    if access is None or not access.get("token"):
        raise RuntimeError("reporter access is not available")
    if not namespace or not name:
        raise RuntimeError("MCV_CAPTURE_NAMESPACE and MCV_CAPTURE_NAME are required")

    path = "/apis/serving.kserve.io/v1alpha1/namespaces/{}/kernelcachecaptures/{}/status".format(
        urllib.parse.quote(namespace, safe=""), urllib.parse.quote(name, safe="")
    )
    request = urllib.request.Request(
        "https://kubernetes.default.svc" + path,
        data=json.dumps([
            {"op": "test", "path": "/status/activeSession/id",
             "value": os.environ["MCV_CAPTURE_SESSION_ID"]},
            {"op": "test", "path": "/status/activeSession/podName",
             "value": os.environ["MCV_SOURCE_POD_NAME"]},
            {"op": "add", "path": "/status/runtimeResult", "value": status},
        ]).encode(),
        headers={
            "Authorization": "Bearer " + access["token"],
            "Content-Type": "application/json-patch+json",
        },
        method="PATCH",
    )
    context = ssl.create_default_context(cafile=KUBERNETES_CA_FILE)
    try:
        with urllib.request.urlopen(request, context=context, timeout=10):
            pass
    except urllib.error.HTTPError as error:
        response = error.read().decode("utf-8", errors="replace")
        if error.code in (404, 410):
            raise CaptureTargetGone() from error
        if error.code == 422 and "test failed" in response.lower():
            raise CaptureSessionSuperseded() from error
        raise RuntimeError(f"HTTP Error {error.code}: {response[:1024]}") from error


class CaptureSessionSuperseded(Exception):
    """The operator has selected another producer."""


class CaptureTargetGone(Exception):
    """The capture target was deleted before the report completed."""


def report(status):
    logger.info("Reporting KernelCacheCapture state: %s", status.get("state", "unknown"))
    attempt = 0
    while True:
        attempt += 1
        try:
            reporter_request(status)
            logger.info("KernelCacheCapture state reported: %s", status.get("state", "unknown"))
            return
        except (OSError, urllib.error.URLError, urllib.error.HTTPError, RuntimeError) as error:
            if attempt == 1 or attempt % 12 == 0:
                logger.warning("KernelCacheCapture state report failed; retrying: %s", error)
            time.sleep(5)


def capture_result():
    with open(RESULT_PATH, encoding="utf-8") as file:
        result = json.load(file)
    if not isinstance(result, dict):
        raise RuntimeError("MCV create result must be a JSON object")
    result = {
        key: json.dumps(value, separators=(",", ":"))
        if isinstance(value, (dict, list))
        else str(value)
        for key, value in result.items()
        if value is not None
    }
    result["cachePaths"] = os.environ.get("MCV_CACHE_PATHS", "[]")
    runtime_info = {
        key: os.environ[name]
        for name, key in RUNTIME_INFO_ENV.items()
        if os.environ.get(name)
    }
    if runtime_info:
        result["runtimeInfo"] = json.dumps(runtime_info, separators=(",", ":"), sort_keys=True)
    result["sourcePodName"] = os.environ.get("MCV_SOURCE_POD_NAME", "")
    result["captureSessionID"] = os.environ.get("MCV_CAPTURE_SESSION_ID", "")
    result.setdefault("capturedAt", timestamp())
    return result


def failure_result(reason, message):
    return {
        "state": "Failed",
        "reason": reason,
        "message": message[:1024],
        "completedAt": timestamp(),
        "sourcePodName": os.environ.get("MCV_SOURCE_POD_NAME", ""),
        "captureSessionID": os.environ.get("MCV_CAPTURE_SESSION_ID", ""),
    }


def wait_for_registry_access():
    if os.environ.get("MCV_REGISTRY_AUTH_REQUIRED", "false") == "true":
        logger.info("Waiting for registry access")
        access_file = os.environ.get(
            "MCV_REGISTRY_ACCESS_FILE",
            os.environ.get("MCV_REGISTRY_CREDENTIAL_FILE", ""),
        )
        wait_for_json_access(access_file, "registry")


def create_snapshot(cache_dir):
    logger.info("Creating baseline cache directory snapshot")
    try:
        subprocess.run([
            "/mcv", "--snapshot", "--dir", cache_dir,
            "--snapshot-file", SNAPSHOT_PATH,
        ], check=True)
    except subprocess.CalledProcessError as error:
        raise RuntimeError(
            f"MCV cache snapshot failed with exit code {error.returncode}"
        ) from error
    logger.info("Baseline cache directory snapshot created")


def main():
    logger.info("KernelCache capture sidecar started")
    try:
        cache_dir = os.environ.get("MCV_CACHE_DIR", "")
        if not cache_dir:
            raise RuntimeError("MCV_CACHE_DIR is required for capture")
        create_snapshot(cache_dir)
        wait_for_json_access(REPORTER_ACCESS_FILE, "reporter")
        report({
            "state": "WaitingForWorkload",
            "capturedAt": timestamp(),
            "sourcePodName": os.environ.get("MCV_SOURCE_POD_NAME", ""),
            "captureSessionID": os.environ.get("MCV_CAPTURE_SESSION_ID", ""),
        })
        wait_for_readiness()
        report({
            "state": "Capturing",
            "capturedAt": timestamp(),
            "sourcePodName": os.environ.get("MCV_SOURCE_POD_NAME", ""),
            "captureSessionID": os.environ.get("MCV_CAPTURE_SESSION_ID", ""),
        })
        wait_for_registry_access()

        target_image = os.environ.get("MCV_TARGET_IMAGE", "")
        if not target_image:
            raise RuntimeError("MCV_TARGET_IMAGE is required for capture")
        logger.info("Starting OCI cache image creation")
        subprocess.run([
            "/mcv", "--create", "--builder", "oci", "--image", target_image,
            "--dir", cache_dir, "--result", RESULT_PATH,
            "--delta-from-snapshot", "--snapshot-file", SNAPSHOT_PATH,
        ], check=True)
        result = capture_result()
        if result.get("state") == "Unchanged":
            logger.info("No new cache directories found; OCI image creation skipped")
        else:
            logger.info("OCI cache image creation completed")
        report(result)
    except subprocess.CalledProcessError as error:
        logger.error("OCI cache image creation failed with exit code %s", error.returncode)
        report(failure_result("CaptureFailed", "MCV OCI image creation failed"))
    except (OSError, RuntimeError, json.JSONDecodeError) as error:
        logger.error("KernelCache capture failed: %s", error)
        report(failure_result("CaptureFailed", "KernelCache capture failed"))

    logger.info("KernelCache capture sidecar is idle")
    while True:
        time.sleep(3600)


if __name__ == "__main__":
    try:
        main()
    except CaptureSessionSuperseded:
        logger.info("Capture session superseded; sidecar is idle")
        while True:
            time.sleep(3600)
    except CaptureTargetGone:
        logger.info("KernelCacheCapture no longer exists; sidecar is idle")
        while True:
            time.sleep(3600)
