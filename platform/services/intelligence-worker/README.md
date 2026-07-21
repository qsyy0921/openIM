# Intelligence Worker

This Python service is the untrusted intelligence plane. All generative operations use the single fixed `gpt-5.6-luna` route through `POST /v1/responses`; there is no Chat Completions, alternate model, alternate provider, or success-shaped fallback. The Worker has no OpenIM, PostgreSQL, Kafka, approval, or business-write credential.

On Windows, start it with:

```powershell
uv sync --extra test
uv run openim-intelligence-local
```

`openim-intelligence-local` loads the one usable API key directly from `%USERPROFILE%\.cli-proxy-api\config.yaml` into the child process environment, verifies that `GET /v1/models` contains `gpt-5.6-luna`, and binds the Worker only to `127.0.0.1:18082`. The key is absent from source, sample configuration, command arguments, logs, and databases.

The generation contract is fixed:

- gateway: `http://127.0.0.1:8317/v1`, Windows loopback only;
- endpoint: `POST /v1/responses`;
- model: `gpt-5.6-luna`;
- `stream=false`, `store=false`, strict JSON schema;
- retry only bounded transient transport, timeout, `429`, and selected `5xx` failures;
- typed failure after the retry budget, with no route change.

Run checks with `uv run pytest -q`. `tests/contract_stub.py` remains an explicit test double only and is not reachable from production configuration.
