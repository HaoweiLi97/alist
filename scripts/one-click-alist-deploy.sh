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

echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}AList Compose Deploy${NC}"
echo -e "${GREEN}========================================${NC}"
echo -e "Image: ${YELLOW}${ALIST_IMAGE}${NC}"
echo -e "Container: ${YELLOW}${ALIST_CONTAINER_NAME}${NC}"
echo -e "Data dir: ${YELLOW}${ALIST_DATA_DIR}${NC}"
echo -e "Deploy dir: ${YELLOW}${DEPLOY_DIR}${NC}"
echo -e "Default web port: ${YELLOW}5244${NC}"
echo ""

cd "${DEPLOY_DIR}"
docker compose up -d

echo ""
echo -e "${GREEN}Deployment complete.${NC}"
echo -e "Open: ${YELLOW}http://<your-server-ip>:5244${NC}"
echo -e "Compose file: ${YELLOW}${COMPOSE_FILE}${NC}"
echo -e "Data dir: ${YELLOW}${ALIST_DATA_DIR}${NC}"
