from intelligence_worker.local_bootstrap import LOCAL_EMBEDDING_FORWARD_BASE_URL


def test_local_bootstrap_uses_dedicated_node2_embedding_forward() -> None:
    assert LOCAL_EMBEDDING_FORWARD_BASE_URL == "http://127.0.0.1:11435/v1"
