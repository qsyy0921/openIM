#!/usr/bin/env bash
set -euo pipefail

staging_root="${1:?usage: stage-node2-release.sh <staging-root> <deploy-root> <releases-root> <version> <ops-sha256> <platform-sha256> <release-sha256>}"
deploy_root="${2:?deploy root is required}"
releases_root="${3:?releases root is required}"
version="${4:?release version is required}"
ops_sha256="${5:?ops archive digest is required}"
platform_sha256="${6:?platform archive digest is required}"
release_sha256="${7:?release archive digest is required}"
ops_archive="$staging_root/ops-$version.tar.zst"
platform_archive="$staging_root/platform-local-$version.tar.zst"
release_archive="$staging_root/release-$version.tar.zst"
platform_root="$deploy_root/platform"
platform_env="$platform_root/.env"

for digest in "$ops_sha256" "$platform_sha256" "$release_sha256"; do
  [[ "$digest" =~ ^[0-9a-f]{64}$ ]] || {
    echo "archive digest is malformed" >&2
    exit 1
  }
done
for archive in "$ops_archive" "$platform_archive" "$release_archive"; do
  [[ -f "$archive" ]] || {
    echo "archive is missing: $archive" >&2
    exit 1
  }
done

printf '%s  %s\n' \
  "$ops_sha256" "$ops_archive" \
  "$platform_sha256" "$platform_archive" \
  "$release_sha256" "$release_archive" | sha256sum --check

if tar --zstd -tf "$platform_archive" | grep -Eq '(^|/)\.env$'; then
  echo "platform archive contains .env" >&2
  exit 1
fi

platform_env_before=missing
if [[ -f "$platform_env" ]]; then
  platform_env_before="$(sha256sum "$platform_env" | awk '{print $1}')"
fi

mkdir -p "$deploy_root" "$platform_root" "$releases_root"
tar --zstd -xf "$release_archive" -C "$releases_root"
tar --zstd -xf "$ops_archive" -C "$deploy_root"
tar --zstd -xf "$platform_archive" -C "$platform_root"

platform_env_after=missing
if [[ -f "$platform_env" ]]; then
  platform_env_after="$(sha256sum "$platform_env" | awk '{print $1}')"
fi
[[ "$platform_env_before" == "$platform_env_after" ]] || {
  echo "platform .env changed during staging" >&2
  exit 1
}

release_root="$releases_root/$version"
[[ -f "$release_root/SHA256SUMS" ]] || {
  echo "release manifest is missing" >&2
  exit 1
}
(
  cd "$release_root"
  sha256sum --check SHA256SUMS >/dev/null
)

echo "platform_env_preserved=$platform_env_after"
echo "release_hashes=verified"
echo "node2_release=staged"
