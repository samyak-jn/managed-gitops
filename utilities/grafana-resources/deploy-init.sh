#!/bin/bash

MODE=$1

ROOT="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"/..

if [ "$(oc auth can-i '*' '*' --all-namespaces)" != "yes" ]; then
  echo
  echo "[ERROR] User '$(oc whoami)' does not have the required 'cluster-admin' role." 1>&2
  echo "Log into the cluster with a user with the required privileges (e.g. kubeadmin) and retry."
  exit 1
fi

echo
echo "Installing the OpenShift GitOps operator subscription:"
kubectl apply -f $ROOT/openshift-gitops/subscription-openshift-gitops.yaml

echo
echo -n "Waiting for default project (and namespace) to exist: "
while ! kubectl get appproject/default -n openshift-gitops &> /dev/null ; do
  echo -n .
  sleep 1
done
echo "OK"

echo
echo -n "Waiting for OpenShift GitOps Route: "
while ! kubectl get route/openshift-gitops-server -n openshift-gitops &> /dev/null ; do
  echo -n .
  sleep 1
done
echo "OK"

echo
echo "Patching OpenShift GitOps ArgoCD CR"

# Switch the Route to use re-encryption
kubectl patch argocd/openshift-gitops -n openshift-gitops -p '{"spec": {"server": {"route": {"enabled": true, "tls": {"termination": "reencrypt"}}}}}' --type=merge

# Allow any authenticated users to be admin on the Argo CD instance
# - Once we have a proper access policy in place, this should be updated to be consistent with that policy.
kubectl patch argocd/openshift-gitops -n openshift-gitops -p '{"spec":{"rbac":{"policy":"g, system:authenticated, role:admin"}}}' --type=merge

# Mark Pending PVC as Healthy, workaround for WaitForFirstConsumer StorageClasses.
# If the attachment will fail then it will be visible on the pod anyway.
kubectl patch argocd/openshift-gitops -n openshift-gitops -p '
spec:
  resourceCustomizations: |
    PersistentVolumeClaim:
      health.lua: |
        hs = {}
        if obj.status ~= nil then
          if obj.status.phase ~= nil then
            if obj.status.phase == "Pending" then
              hs.status = "Healthy"
              hs.message = obj.status.phase
              return hs
            end
            if obj.status.phase == "Bound" then
              hs.status = "Healthy"
              hs.message = obj.status.phase
              return hs
            end
          end
        end
        hs.status = "Progressing"
        return hs
' --type=merge

echo 
echo "Add Role/RoleBindings for OpenShift GitOps:"
kubectl apply --kustomize $ROOT/openshift-gitops/cluster-rbac

OPENSSLDIR=`openssl version -d | cut -f2 -d'"'`

echo "Setting secrets for GitOps"
if ! kubectl get namespace gitops &>/dev/null; then
  kubectl create namespace gitops
fi
if ! kubectl get secret -n gitops gitops-postgresql-staging &>/dev/null; then
  kubectl create secret generic gitops-postgresql-staging \
    --namespace=gitops \
    --from-literal=postgresql-password=$(openssl rand -base64 20)
fi

echo "========================================================================="
echo
