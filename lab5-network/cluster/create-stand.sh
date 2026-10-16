#!/usr/bin/env bash
# Пересобирает стенд лабы 5: кластер k3d с Cilium вместо flannel,
# затем возвращает окружение лаб 2-3 и чарт shop.
set -euo pipefail
CLUSTER=obs
REPO=~/labs/containerization-labs
cd "$REPO"

# Адрес Windows-хоста берём из шлюза по умолчанию: подсеть Hyper-V
# переназначается при перезагрузке хоста, и зашитый адрес прокси протухает.
PROXY_HOST=$(ip route | awk '/^default/{print $3}')
PROXY="http://${PROXY_HOST}:2080"
NOPROXY="localhost,127.0.0.1,0.0.0.0,::1,10.42.0.0/16,10.43.0.0/16,172.18.0.0/16,.svc,.cluster.local,k3d-${CLUSTER}-server-0,k3d-${CLUSTER}-agent-0"
echo ">>> прокси: $PROXY"

echo ">>> [1/7] пересоздание кластера"
k3d cluster delete "$CLUSTER" >/dev/null 2>&1 || true
k3d cluster create "$CLUSTER" \
  --agents 1 \
  --k3s-arg "--disable=traefik@server:0" \
  --k3s-arg "--flannel-backend=none@server:0" \
  --k3s-arg "--disable-network-policy@server:0" \
  --env "HTTP_PROXY=$PROXY@all" \
  --env "HTTPS_PROXY=$PROXY@all" \
  --env "NO_PROXY=$NOPROXY@all" \
  -p "30000-30010:30000-30010@server:0" \
  --wait

echo ">>> [2/7] Cilium 1.20.2 + Hubble"
helm upgrade --install cilium cilium/cilium --version 1.20.2 -n kube-system \
  --set ipam.operator.clusterPoolIPv4PodCIDRList=10.42.0.0/16 \
  --set hubble.enabled=true \
  --set hubble.relay.enabled=true \
  --set hubble.ui.enabled=true \
  --set hubble.ui.service.type=NodePort \
  --set hubble.ui.service.nodePort=30007 \
  --wait --timeout 15m
kubectl -n kube-system rollout status ds/cilium --timeout=10m
kubectl wait --for=condition=Ready nodes --all --timeout=5m

echo ">>> [3/7] values Cilium в репозиторий"
mkdir -p lab5-network/helm/values lab5-network/cluster
helm get values cilium -n kube-system | tail -n +2 > lab5-network/helm/values/cilium.yaml

echo ">>> [4/7] Kyverno + политики лабы 3"
helm upgrade --install kyverno kyverno/kyverno --version 3.9.1 -n kyverno --create-namespace \
  --set admissionController.replicas=1 --set backgroundController.replicas=1 \
  --set cleanupController.replicas=1 --set reportsController.replicas=1 \
  --wait --timeout 10m
kubectl apply -f lab3-platform/policies/

echo ">>> [5/7] оператор CNPG"
helm upgrade --install cnpg cnpg/cloudnative-pg --version 0.29.1 -n cnpg-system --create-namespace --wait --timeout 10m
kubectl wait --for=condition=established crd/clusters.postgresql.cnpg.io --timeout=5m

echo ">>> [6/7] стек мониторинга лабы 2"
helm upgrade --install kps prometheus-community/kube-prometheus-stack --version 91.9.0 \
  -n monitoring --create-namespace -f lab2-observability/helm/values/kube-prometheus-stack.yaml --wait --timeout 15m
helm upgrade --install loki   grafana/loki   --version 7.3.0  -n monitoring -f lab2-observability/helm/values/loki.yaml --wait --timeout 10m
helm upgrade --install alloy  grafana/alloy  --version 1.13.0 -n monitoring -f lab2-observability/helm/values/alloy.yaml --wait --timeout 10m
helm upgrade --install jaeger jaegertracing/jaeger --version 4.14.1 -n monitoring -f lab2-observability/helm/values/jaeger.yaml --wait --timeout 10m
helm upgrade --install jaeger-ui     ./lab2-observability/helm/jaeger-nodeport -n monitoring --wait --timeout 5m
helm upgrade --install karma         ./lab2-observability/helm/karma           -n monitoring --wait --timeout 5m
helm upgrade --install alert-webhook ./lab2-observability/helm/alert-webhook    -n monitoring --wait --timeout 5m

echo ">>> [7/7] образы и чарт shop"
k3d image import shop-api:v4 shop-worker:v1 shop-batch:v1 -c "$CLUSTER"
helm upgrade --install shop ./lab3-platform/helm/shop -n shop --create-namespace --wait --timeout 10m

echo ">>> ГОТОВО"
