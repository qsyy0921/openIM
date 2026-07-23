#!/usr/bin/env bash
set -euo pipefail

archive="${1:-}"
destination="${2:-$HOME/MFL/venvs/intelligence-worker-retrieval}"
archive_sha256="46294bfbe1bc3e64ffd0f8af546bae954d640a3fdf474a17851a26c0d1fed151"

[[ -f "$archive" ]] || {
  echo "usage: $0 <verified-site-packages-py314.tar> [venv-destination]" >&2
  exit 1
}
[[ "$destination" == /* && "$destination" != "/" ]] || {
  echo "retrieval runtime destination must be an absolute non-root path" >&2
  exit 1
}
command -v python3 >/dev/null 2>&1 || {
  echo "python3 is required" >&2
  exit 1
}
command -v sha256sum >/dev/null 2>&1 || {
  echo "sha256sum is required" >&2
  exit 1
}

actual_python="$(python3 -c 'import sys; print(".".join(map(str, sys.version_info[:2])))')"
[[ "$actual_python" == "3.14" ]] || {
  echo "retrieval runtime requires Python 3.14, found $actual_python" >&2
  exit 1
}
printf '%s  %s\n' "$archive_sha256" "$archive" | sha256sum -c -

verify_runtime() {
  local python="$1"
  "$python" - <<'PY'
from importlib import metadata

expected = {
    "fastapi": "0.136.0",
    "huggingface-hub": "0.36.0",
    "httpx": "0.28.1",
    "pydantic": "2.12.5",
    "prometheus-client": "0.25.0",
    "PyYAML": "6.0.3",
    "safetensors": "0.7.0",
    "torch": "2.11.0+cpu",
    "transformers": "4.57.1",
    "uvicorn": "0.44.0",
}
for distribution, version in expected.items():
    actual = metadata.version(distribution)
    if actual != version:
        raise SystemExit(
            f"retrieval dependency mismatch: {distribution}={actual}, expected {version}"
        )
PY
}

if [[ -x "$destination/bin/python" ]]; then
  verify_runtime "$destination/bin/python"
  echo "retrieval_runtime=ready path=$destination"
  exit 0
fi
[[ ! -e "$destination" ]] || {
  echo "retrieval runtime destination exists but is incomplete: $destination" >&2
  exit 1
}

parent="$(dirname "$destination")"
install -d -m 0750 "$parent"
staging="$(mktemp -d "$parent/.intelligence-worker-retrieval.XXXXXXXX")"
cleanup() {
  rm -rf -- "$staging"
}
trap cleanup EXIT

python3 -m venv "$staging"
site_packages="$(
  "$staging/bin/python" -c \
    'import sysconfig; print(sysconfig.get_paths()["purelib"])'
)"
archive="$(readlink -f "$archive")"
site_packages="$(readlink -f "$site_packages")"
[[ "$site_packages" == "$staging"/* ]] || {
  echo "retrieval site-packages path escaped its staging runtime" >&2
  exit 1
}
python3 - "$archive" "$site_packages" <<'PY'
from pathlib import Path
import sys
import tarfile

archive = Path(sys.argv[1]).resolve(strict=True)
destination = Path(sys.argv[2]).resolve(strict=True)
with tarfile.open(archive, "r") as bundle:
    for member in bundle.getmembers():
        target = (destination / member.name).resolve()
        if not target.is_relative_to(destination):
            raise SystemExit("retrieval dependency archive contains an unsafe path")
        if member.issym() or member.islnk() or member.isdev():
            raise SystemExit("retrieval dependency archive contains an unsafe entry")
    bundle.extractall(destination, filter="data")
PY
verify_runtime "$staging/bin/python"
mv -- "$staging" "$destination"
trap - EXIT
echo "retrieval_runtime=provisioned path=$destination"
