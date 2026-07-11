# Intelligence worker

This Python service is the untrusted intelligence plane. It calls one configured DeepSeek Chat Completions model and returns a validated answer/action candidate; it has no OpenIM, PostgreSQL, Kafka, tool, approval, or business credentials.

```powershell
$env:INTELLIGENCE_DEEPSEEK_BASE_URL = 'https://api.deepseek.com'
$env:INTELLIGENCE_DEEPSEEK_API_KEY = '<scoped-secret>'
$env:INTELLIGENCE_DEEPSEEK_MODEL = 'deepseek-v4-pro'
$env:INTELLIGENCE_DEEPSEEK_TIMEOUT_SECONDS = '90'
$env:INTELLIGENCE_DEEPSEEK_MAX_TOKENS = '1024'
python -m uvicorn intelligence_worker.app:app --app-dir src --host 127.0.0.1 --port 18082
```

Run checks with `python -m pytest -q`. `tests/contract_stub.py` is only an explicit local end-to-end test double; production code has no fallback route to it.
