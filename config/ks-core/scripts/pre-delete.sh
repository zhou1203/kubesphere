#!/usr/bin/env bash

set -x

# Blocks and waits for all extension resources to be deleted
kubectl delete repositories.kubesphere.io --all
kubectl delete categories.kubesphere.io --all
kubectl delete extensions.kubesphere.io --all
kubectl delete installplans.kubesphere.io --all

kubectl patch workspaces.tenant.kubesphere.io system-workspace -p '{"metadata":{"finalizers":[]}}' --type=merge
kubectl patch workspacetemplates.tenant.kubesphere.io system-workspace -p '{"metadata":{"finalizers":[]}}' --type=merge
for ns in $(kubectl get ns -o jsonpath='{.items[*].metadata.name}')
do
  kubectl label ns $ns kubesphere.io/workspace- && \
  kubectl patch ns $ns -p '{"metadata":{"ownerReferences":[]}}' --type=merge && \
  echo "{\"kind\":\"Namespace\",\"apiVersion\":\"v1\",\"metadata\":{\"name\":\"$ns\",\"finalizers\":null}}" | kubectl replace --raw "/api/v1/namespaces/$ns/finalize" -f -
done
