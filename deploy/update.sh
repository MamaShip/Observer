#!/bin/sh
# Switch the running service to another image version (plan.md §13.6).
#
#   /opt/observer/update.sh v1.2.3          # ghcr.io/mamaship/observer:v1.2.3
#   /opt/observer/update.sh observer:local  # an image loaded with docker load
#
# It is also the forced command of the GitHub Actions deploy key
# (docs/runbook.md), which passes the tag in SSH_ORIGINAL_COMMAND; only
# version tags are accepted from there.
set -eu
cd "$(dirname "$0")"

ref=${1:-${SSH_ORIGINAL_COMMAND:-}}
case "$ref" in
  v[0-9]*.[0-9]*.[0-9]*)
    echo "$ref" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$' || { echo "bad tag: $ref" >&2; exit 2; }
    image=ghcr.io/mamaship/observer:$ref ;;
  *)
    if [ -n "${SSH_ORIGINAL_COMMAND:-}" ] || [ -z "$ref" ]; then
      echo "usage: $0 vX.Y.Z | <local image>" >&2; exit 2
    fi
    image=$ref ;;
esac

previous=$(sed -n 's/^OBSERVER_IMAGE=//p' .env)
log() { echo "$(date -u +%FT%TZ) $*" >> deploy.log; }

# Migrations run when the new version starts, and some cannot be undone:
# back up first, and stop here if that fails. (Backups need S3; without it
# the command says so and fails, so skip it then.)
if [ -n "$(docker compose ps -q app 2>/dev/null)" ] && grep -q '^S3_BUCKET=.' .env; then
  if ! docker compose exec -T app observer admin backup; then
    echo "backup failed; not updating" >&2
    log "update to $image refused: backup failed"
    exit 1
  fi
fi
sed -i "s|^OBSERVER_IMAGE=.*|OBSERVER_IMAGE=$image|" .env
grep -q '^OBSERVER_IMAGE=' .env || echo "OBSERVER_IMAGE=$image" >> .env

case "$image" in ghcr.io/*) docker compose pull app ;; esac
if ! docker compose up -d --wait app; then
  echo "app did not become healthy; rolling back to $previous" >&2
  sed -i "s|^OBSERVER_IMAGE=.*|OBSERVER_IMAGE=$previous|" .env
  docker compose up -d --wait app
  log "update to $image failed; rolled back to $previous"
  exit 1
fi
docker compose up -d
docker image prune -f >/dev/null
log "updated $previous -> $image"
echo "running $image"
