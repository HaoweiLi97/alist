#!/bin/bash

set -euo pipefail

DEPLOY_DIR="${1:-$HOME/docker/alist}"
ALIST_IMAGE="${ALIST_IMAGE:-haoweil/alist:latest}"
ALIST_DATA_DIR="${ALIST_DATA_DIR:-$DEPLOY_DIR/data}"
ALIST_CONTAINER_NAME="${ALIST_CONTAINER_NAME:-alist}"
ALIST_TIMEZONE="${ALIST_TIMEZONE:-UTC}"
ALIST_PUID="${ALIST_PUID:-0}"
ALIST_PGID="${ALIST_PGID:-0}"
ALIST_UMASK="${ALIST_UMASK:-022}"
COMPOSE_FILE="${DEPLOY_DIR}/docker-compose.yml"
SERVICE_NAME="alist"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

usage() {
  cat <<EOF
Usage:
  ./scripts/one-click-alist-deploy.sh [deploy_dir]

Examples:
  ./scripts/one-click-alist-deploy.sh
  ./scripts/one-click-alist-deploy.sh /opt/alist

Environment variables:
  ALIST_IMAGE              Docker image to run. Default: haoweil/alist:latest
  ALIST_DATA_DIR           Host data directory. Default: <deploy_dir>/data
  ALIST_CONTAINER_NAME     Container name. Default: alist
  ALIST_TIMEZONE           Container timezone. Default: UTC
  ALIST_PUID               Container PUID. Default: 0
  ALIST_PGID               Container PGID. Default: 0
  ALIST_UMASK              Container umask. Default: 022

Running the same command again pulls the newest configured image and updates
the existing container without changing the data directory or compose file.
EOF
}

if [[ "${DEPLOY_DIR}" == "-h" || "${DEPLOY_DIR}" == "--help" ]]; then
  usage
  exit 0
fi

if ! command -v docker >/dev/null 2>&1; then
  echo -e "${RED}Error: docker is not installed.${NC}" >&2
  exit 1
fi

if ! docker info >/dev/null 2>&1; then
  echo -e "${RED}Error: docker is not running.${NC}" >&2
  exit 1
fi

if ! docker compose version >/dev/null 2>&1; then
  echo -e "${RED}Error: docker compose is not available.${NC}" >&2
  exit 1
fi

mkdir -p "${DEPLOY_DIR}" "${ALIST_DATA_DIR}"

IS_UPDATE=false
if [[ -f "${COMPOSE_FILE}" ]]; then
  IS_UPDATE=true
elif grep -Fxq "${ALIST_CONTAINER_NAME}" <<<"$(docker ps -a --format '{{.Names}}')"; then
  echo -e "${RED}Error: container '${ALIST_CONTAINER_NAME}' exists, but ${COMPOSE_FILE} does not.${NC}" >&2
  echo -e "Move the existing deployment under Docker Compose or choose another deploy directory/container name." >&2
  exit 1
fi

if [[ ! -f "${COMPOSE_FILE}" ]]; then
  cat > "${COMPOSE_FILE}" <<EOF
services:
  alist:
    image: ${ALIST_IMAGE}
    container_name: ${ALIST_CONTAINER_NAME}
    restart: always
    network_mode: host
    environment:
      - PUID=${ALIST_PUID}
      - PGID=${ALIST_PGID}
      - UMASK=${ALIST_UMASK}
      - TZ=${ALIST_TIMEZONE}
    volumes:
      - "${ALIST_DATA_DIR}:/opt/alist/data"
EOF
  echo -e "${YELLOW}Created ${COMPOSE_FILE}${NC}"
else
  echo -e "${YELLOW}Keeping existing ${COMPOSE_FILE}${NC}"
fi

compose() {
  docker compose --project-directory "${DEPLOY_DIR}" -f "${COMPOSE_FILE}" "$@"
}

COMPOSE_SERVICES="$(compose config --services)"
if ! grep -Fxq "${SERVICE_NAME}" <<<"${COMPOSE_SERVICES}"; then
  echo -e "${RED}Error: ${COMPOSE_FILE} does not contain the '${SERVICE_NAME}' service.${NC}" >&2
  exit 1
fi

OLD_CONTAINER_ID="$(compose ps -q "${SERVICE_NAME}" 2>/dev/null || true)"
OLD_IMAGE_ID=""
SERVICE_IMAGE="${ALIST_IMAGE}"
if [[ -n "${OLD_CONTAINER_ID}" ]]; then
  OLD_IMAGE_ID="$(docker inspect --format '{{.Image}}' "${OLD_CONTAINER_ID}" 2>/dev/null || true)"
  SERVICE_IMAGE="$(docker inspect --format '{{.Config.Image}}' "${OLD_CONTAINER_ID}" 2>/dev/null || true)"
fi

if [[ -z "${SERVICE_IMAGE}" ]]; then
  echo -e "${RED}Error: unable to determine the image used by the '${SERVICE_NAME}' service.${NC}" >&2
  exit 1
fi

rollback() {
  if [[ -z "${OLD_IMAGE_ID}" || -z "${SERVICE_IMAGE}" || "${SERVICE_IMAGE}" == *@* ]]; then
    echo -e "${RED}Automatic rollback is unavailable. Existing data was not changed.${NC}" >&2
    return 1
  fi

  echo -e "${YELLOW}Update failed; rolling back to the previous image...${NC}" >&2
  if docker image tag "${OLD_IMAGE_ID}" "${SERVICE_IMAGE}" >/dev/null 2>&1 && \
     compose up -d --force-recreate --remove-orphans; then
    echo -e "${YELLOW}Rollback completed. The previous image is running.${NC}" >&2
    return 0
  fi

  echo -e "${RED}Rollback failed. Run 'docker compose -f ${COMPOSE_FILE} logs' for details.${NC}" >&2
  return 1
}

wait_until_running() {
  local attempts=30
  local container_id state
  for ((i = 1; i <= attempts; i++)); do
    container_id="$(compose ps -q "${SERVICE_NAME}" 2>/dev/null || true)"
    if [[ -n "${container_id}" ]]; then
      state="$(docker inspect --format '{{.State.Status}}' "${container_id}" 2>/dev/null || true)"
      if [[ "${state}" == "running" ]]; then
        sleep 2
        state="$(docker inspect --format '{{.State.Status}}' "${container_id}" 2>/dev/null || true)"
        [[ "${state}" == "running" ]] && return 0
      elif [[ "${state}" == "exited" || "${state}" == "dead" ]]; then
        return 1
      fi
    fi
    sleep 1
  done
  return 1
}

echo -e "${GREEN}========================================${NC}"
if [[ "${IS_UPDATE}" == true ]]; then
  echo -e "${GREEN}AList Compose Update${NC}"
else
  echo -e "${GREEN}AList Compose Deploy${NC}"
fi
echo -e "${GREEN}========================================${NC}"
echo -e "Image: ${YELLOW}${ALIST_IMAGE}${NC}"
echo -e "Container: ${YELLOW}${ALIST_CONTAINER_NAME}${NC}"
echo -e "Data dir: ${YELLOW}${ALIST_DATA_DIR}${NC}"
echo -e "Deploy dir: ${YELLOW}${DEPLOY_DIR}${NC}"
echo -e "Default web port: ${YELLOW}5244${NC}"
echo ""

echo -e "${YELLOW}Pulling ${SERVICE_IMAGE}...${NC}"
if ! compose pull "${SERVICE_NAME}"; then
  echo -e "${RED}Image pull failed. The existing container is still running.${NC}" >&2
  exit 1
fi

if ! compose up -d --remove-orphans; then
  rollback || true
  exit 1
fi

if ! wait_until_running; then
  echo -e "${RED}AList did not stay running after the update.${NC}" >&2
  compose logs --tail 50 "${SERVICE_NAME}" >&2 || true
  rollback || true
  exit 1
fi

NEW_CONTAINER_ID="$(compose ps -q "${SERVICE_NAME}")"
NEW_IMAGE_ID="$(docker inspect --format '{{.Image}}' "${NEW_CONTAINER_ID}")"

echo ""
if [[ "${IS_UPDATE}" == true && -n "${OLD_IMAGE_ID}" && "${OLD_IMAGE_ID}" == "${NEW_IMAGE_ID}" ]]; then
  echo -e "${GREEN}AList is already up to date.${NC}"
elif [[ "${IS_UPDATE}" == true ]]; then
  echo -e "${GREEN}AList update complete.${NC}"
  echo -e "Previous image: ${YELLOW}${OLD_IMAGE_ID}${NC}"
  echo -e "Current image:  ${YELLOW}${NEW_IMAGE_ID}${NC}"
else
  echo -e "${GREEN}Deployment complete.${NC}"
fi
echo -e "Open: ${YELLOW}http://<your-server-ip>:5244${NC}"
echo -e "Compose file: ${YELLOW}${COMPOSE_FILE}${NC}"
echo -e "Data dir: ${YELLOW}${ALIST_DATA_DIR}${NC}"
