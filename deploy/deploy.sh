#!/bin/bash
# Script de deploy de identity en discordia-vm.
#
# Se instala en ~/deploy.sh de la VM y es el UNICO comando que puede ejecutar la
# clave SSH del pipeline (authorized_keys con command="..."). El tag de la imagen
# llega en SSH_ORIGINAL_COMMAND. Las variables de entorno del servicio viven en
# ~/identity.env en la VM: nunca se versionan.
set -euo pipefail

IMAGE_REPO=us-central1-docker.pkg.dev/discordia-509215/discordia-servers/servicio-identify
TAG="${SSH_ORIGINAL_COMMAND:-latest}"

# Solo se aceptan SHA de git completos o "latest".
if ! [[ "$TAG" =~ ^([0-9a-f]{40}|latest)$ ]]; then
  echo "tag invalido: $TAG" >&2
  exit 2
fi

export PATH="$PATH:/snap/bin"   # docker-credential-gcloud (auth a Artifact Registry)
IMAGE="$IMAGE_REPO:$TAG"

# ID de la imagen que esta corriendo hoy, para poder volver atras.
PREV_ID=$(docker inspect -f '{{.Image}}' identity 2>/dev/null || true)

docker pull "$IMAGE"
docker rm -f identity >/dev/null 2>&1 || true
docker run -d --name identity --restart unless-stopped \
  --env-file "$HOME/identity.env" -p 8080:8080 "$IMAGE" >/dev/null

# Smoke test: /readyz confirma que el servicio y la base responden.
for _ in $(seq 1 20); do
  if curl -fsS -m 3 http://localhost:8080/readyz >/dev/null 2>&1; then
    echo "deploy OK: $IMAGE"
    exit 0
  fi
  sleep 3
done

echo "ERROR: /readyz no respondio tras el deploy de $IMAGE" >&2
docker logs --tail 30 identity 2>&1 \
  | sed -E 's#(amqps?://)[^@ ]*@#\1***@#g; s#(password=)[^ ]*#\1***#Ig' >&2 || true
docker rm -f identity >/dev/null 2>&1 || true
if [ -n "$PREV_ID" ]; then
  echo "rollback a la imagen anterior ($PREV_ID)" >&2
  docker run -d --name identity --restart unless-stopped \
    --env-file "$HOME/identity.env" -p 8080:8080 "$PREV_ID" >/dev/null
fi
exit 1
