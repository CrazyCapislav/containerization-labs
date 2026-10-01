#!/usr/bin/env bash
set -euo pipefail

[ "$(id -u)" -eq 0 ] || { echo "нужен root: sudo $0" >&2; exit 1; }

CG=/sys/fs/cgroup/mydocker

echo "+memory +cpu +pids" > /sys/fs/cgroup/cgroup.subtree_control

mkdir -p "$CG"
echo "256M"          > "$CG/memory.max"
echo 0               > "$CG/memory.swap.max"
echo "50000 100000"  > "$CG/cpu.max"
echo 50              > "$CG/pids.max"
echo $$              > "$CG/cgroup.procs"

cd "$(dirname "$(realpath "$0")")/api"
[ -x ./api ] || { echo "не найден исполняемый ./api в $(pwd)" >&2; exit 1; }

exec unshare -p -f --mount-proc -u -i -n -- \
  bash -c '
    set -euo pipefail
    hostname mydocker
    ip link set lo up
    exec setpriv --reuid 1000 --regid 1000 --clear-groups \
                 --bounding-set -all --inh-caps -all --no-new-privs \
                 ./api
  '
