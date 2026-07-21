from __future__ import annotations


class ModelProviderError(RuntimeError):
    def __init__(self, code: str, retryable: bool):
        super().__init__(code)
        self.code = code
        self.retryable = retryable


class ModelTimeout(ModelProviderError):
    def __init__(self) -> None:
        super().__init__("model_timeout", True)


class ModelUnavailable(ModelProviderError):
    def __init__(self) -> None:
        super().__init__("model_unavailable", True)


class ModelRateLimited(ModelProviderError):
    def __init__(self) -> None:
        super().__init__("model_rate_limited", True)


class ModelAuthenticationFailed(ModelProviderError):
    def __init__(self) -> None:
        super().__init__("model_authentication_failed", False)


class ModelRequestRejected(ModelProviderError):
    def __init__(self) -> None:
        super().__init__("model_request_rejected", False)


class ModelProtocolError(ModelProviderError):
    def __init__(self) -> None:
        super().__init__("model_protocol_error", False)


class ModelRouteUnavailable(ModelProviderError):
    def __init__(self) -> None:
        super().__init__("model_route_unavailable", False)
