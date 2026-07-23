from __future__ import annotations

from time import perf_counter

from fastapi import Request
from prometheus_client import CONTENT_TYPE_LATEST, Counter, Histogram, generate_latest
from starlette.responses import Response


REQUESTS = Counter(
    "openim_intelligence_http_requests_total",
    "Intelligence Worker requests by stable route, method, and status.",
    ("route", "method", "status"),
)
REQUEST_DURATION = Histogram(
    "openim_intelligence_http_request_duration_seconds",
    "Intelligence Worker request latency by stable route and method.",
    ("route", "method"),
)
EMBEDDING_BATCH = Histogram(
    "openim_intelligence_embedding_batch_size",
    "Texts per embedding request.",
    buckets=(1, 2, 4, 8, 16, 32, 64, 128),
)
RERANKER_BATCH = Histogram(
    "openim_intelligence_reranker_batch_size",
    "Authorized candidate pairs per reranker request.",
    buckets=(1, 2, 4, 8, 16, 32),
)


async def observe_request(request: Request, call_next):
    started = perf_counter()
    status = 500
    try:
        response = await call_next(request)
        status = response.status_code
        return response
    finally:
        route = request.scope.get("route")
        route_path = getattr(route, "path", "unmatched")
        REQUESTS.labels(route_path, request.method, str(status)).inc()
        REQUEST_DURATION.labels(route_path, request.method).observe(perf_counter() - started)


def metrics_response() -> Response:
    return Response(generate_latest(), media_type=CONTENT_TYPE_LATEST)
