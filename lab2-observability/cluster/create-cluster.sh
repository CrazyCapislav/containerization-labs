#!/usr/bin/env bash
# Создаёт локальный кластер k3d для лабы 2.
set -euo pipefail

CLUSTER=${CLUSTER:-obs}
# Прокси хоста (Throne). Провайдер режет Docker Hub, напрямую образы тянутся
# через раз; весь трафик containerd за образами идёт через туннель.
PROXY=${PROXY:-http://172.29.208.1:2080}
NOPROXY="localhost,127.0.0.1,0.0.0.0,::1,10.42.0.0/16,10.43.0.0/16,172.18.0.0/16,.svc,.cluster.local,k3d-${CLUSTER}-server-0,k3d-${CLUSTER}-agent-0"

k3d cluster create "$CLUSTER" \
  --agents 1 \
  --k3s-arg "--disable=traefik@server:0" \
  --env "HTTP_PROXY=$PROXY@all" \
  --env "HTTPS_PROXY=$PROXY@all" \
  --env "NO_PROXY=$NOPROXY@all" \
  -p "30000-30010:30000-30010@server:0" \
  --wait

kubectl wait --for=condition=Ready pods --all -n kube-system --timeout=300s
kubectl get nodes
