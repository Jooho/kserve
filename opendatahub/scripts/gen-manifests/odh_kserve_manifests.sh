#!/bin/bash

# Comment out cluster serving runtime crd
echo -n ".. Comment out cluster serving runtime crd"
sed "s/- serving.kserve.io_clusterservingruntimes.yaml/#- serving.kserve.io_clusterservingruntimes.yaml/g"  -i ${KSERVE_CONTROLLER_DIR}/crd/kustomization.yaml 
echo -e "\r ✓"

# Delete caBundle from webhook because it will use serving certificate"
echo -n ".. Delete caBundle from webhook because it will use serving certificate"
sed '/caBundle/d' -i ${KSERVE_CONTROLLER_DIR}/default/inferenceservice_conversion_webhook.yaml
sed '/caBundle/d' -i ${KSERVE_CONTROLLER_DIR}/webhook/manifests.yaml
echo -e "\r ✓"

# Update images to adopt dynamic value using params.env
echo -n ".. Replace each image of the parameter variable 'images'"
sed 's+kserve/alibi-explainer+$(kserve-alibi-explainer)+g' -i ${KSERVE_CONTROLLER_DIR}/configmap/inferenceservice.yaml
sed 's+kserve/art-explainer+$(kserve-art-explainer)+g' -i ${KSERVE_CONTROLLER_DIR}/configmap/inferenceservice.yaml
sed 's+kserve/storage-initializer:latest+$(kserve-storage-initializer)+g' -i ${KSERVE_CONTROLLER_DIR}/configmap/inferenceservice.yaml
sed 's+kserve/agent:latest+$(kserve-agent)+g' -i ${KSERVE_CONTROLLER_DIR}/configmap/inferenceservice.yaml
sed 's+kserve/router:latest+$(kserve-router)+g' -i ${KSERVE_CONTROLLER_DIR}/configmap/inferenceservice.yaml
sed 's+"defaultImageVersion": "latest"+"defaultImageVersion": "$(kserve-explainer-version)"+g' -i ${KSERVE_CONTROLLER_DIR}/configmap/inferenceservice.yaml
sed 's+image: ko://github.com/kserve/kserve/cmd/manager+image: $(kserve-controller)+g -i ${KSERVE_CONTROLLER_DIR}/manager/manager.yaml
echo -e "\r ✓"

echo -n ".. Remove CertManager related from default/kustomization.yaml"
replacementsNum=$(grep -n replacements ${KSERVE_CONTROLLER_DIR}/default/kustomization.yaml |cut -d':' -f1)
replacementsNumBeforeLine=$((replacementsNum-1))
protectStartLine=$(grep -n Protect  ${KSERVE_CONTROLLER_DIR}/default/kustomization.yaml |cut -d':' -f1)
protectBeforeLine=$((protectStartLine-1))
sed -i "${replacementsNumBeforeLine},${protectBeforeLine}d"  ${KSERVE_CONTROLLER_DIR}/default/kustomization.yaml
sed '/certmanager/d' -i ${KSERVE_CONTROLLER_DIR}/default/kustomization.yaml

for n in $(grep -n cainjection ${KSERVE_CONTROLLER_DIR}/default/kustomization.yaml |cut -d: -f1)
do 
  sed "${n}s/^/#/"  -i ${KSERVE_CONTROLLER_DIR}/default/kustomization.yaml 
done
echo -e "\r ✓"

echo -n ".. Add serving-cert-secret-name to webhook/service.yaml"
yq eval '.metadata.annotations."service.beta.openshift.io/serving-cert-secret-name"="kserve-webhook-server-cert"' -i  ${KSERVE_CONTROLLER_DIR}/webhook/service.yaml
echo -e "\r ✓"

echo -n ".. Add inject-cabundle into webhook/kustomization.yaml"
yq eval '.commonAnnotations += {"service.beta.openshift.io/inject-cabundle": "true"}' -i ${KSERVE_CONTROLLER_DIR}/webhook/kustomization.yaml
echo -e "\r ✓"sed 's+name: $(webhookServiceName)+name: kserve-webhook-server-service+g' -i ${KSERVE_CONTROLLER_DIR}/webhook/manifests.yaml

echo -n ".. Replace \$(webhookServiceName) to 'kserve-webhook-server-service' from webhook/manifests.yaml, default/inferenceservice_conversion_webhook.yaml"
sed 's+name: $(webhookServiceName)+name: kserve-webhook-server-service+g' -i ${KSERVE_CONTROLLER_DIR}/webhook/manifests.yaml
sed 's+name: $(webhookServiceName)+name: kserve-webhook-server-service+g' -i ${KSERVE_CONTROLLER_DIR}/default/inferenceservice_conversion_webhook.yaml
echo -e "\r ✓"

echo -n ".. Remove namespace: \$(kserveNamespace) from webhook/manifests.yaml"
sed '/namespace: $(kserveNamespace)/d' -i ${KSERVE_CONTROLLER_DIR}/webhook/manifests.yaml
echo -e "\r ✓"

echo -n ".. Replace labels of commonLabels because kfctl does not support 'labels'"
labelNum=$(grep -n 'labels:' ${KSERVE_CONTROLLER_DIR}/overlays/kubeflow/kustomization.yaml |cut -d':' -f1)
sed -i '/labels:/,/pairs:/d' ${KSERVE_CONTROLLER_DIR}/overlays/kubeflow/kustomization.yaml
sed -i "${labelNum}"'i\'"commonLabels:" ${KSERVE_CONTROLLER_DIR}/overlays/kubeflow/kustomization.yaml
echo -e "\r ✓"
