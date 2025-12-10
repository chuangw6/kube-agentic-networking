/*
Copyright 2025 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package envoy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"text/template"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"

	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	"sigs.k8s.io/kube-agentic-networking/pkg/constants"
	"sigs.k8s.io/kube-agentic-networking/pkg/infra/xds"
)

// proxyName generates a deterministic name for the Envoy proxy resources.
func proxyName(namespace, name string) string {
	namespacedName := types.NamespacedName{
		Namespace: namespace,
		Name:      name,
	}
	hash := sha256.Sum256([]byte(namespacedName.String()))
	return fmt.Sprintf("envoy-proxy-%s", hex.EncodeToString(hash[:6]))
}

const dynamicControlPlaneConfig = `node:
  cluster: {{ .Cluster }}
  id: {{ .ID }}

dynamic_resources:
  ads_config:
    api_type: GRPC
    grpc_services:
    - envoy_grpc:
        cluster_name: xds_cluster
  cds_config:
    ads: {}
  lds_config:
    ads: {}

static_resources:
  clusters:
  - name: xds_cluster
    type: STRICT_DNS
    typed_extension_protocol_options:
      envoy.extensions.upstreams.http.v3.HttpProtocolOptions:
        "@type": type.googleapis.com/envoy.extensions.upstreams.http.v3.HttpProtocolOptions
        explicit_http_config:
          http2_protocol_options: {}
    load_assignment:
      cluster_name: xds_cluster
      endpoints:
      - lb_endpoints:
        - endpoint:
            address:
              socket_address:
                address: {{ .ControlPlaneAddress }}
                port_value: {{ .ControlPlanePort }}

admin:
  access_log_path: /dev/stdout
  address:
    socket_address:
      address: 0.0.0.0
      port_value: 15000
`

type configData struct {
	Cluster             string
	ID                  string
	ControlPlaneAddress string
	ControlPlanePort    int
}

// generateEnvoyBootstrapConfig returns an envoy config generated from config data
func generateEnvoyBootstrapConfig(cluster, id string) (string, error) {
	if cluster == "" || id == "" {
		return "", fmt.Errorf("missing parameters for envoy config")
	}

	data := &configData{
		Cluster:             cluster,
		ID:                  id,
		ControlPlaneAddress: fmt.Sprintf("%s.%s.svc.cluster.local", constants.XDSServerServiceName, constants.AgenticNetSystemNamespace),
		ControlPlanePort:    15001,
	}

	t, err := template.New("gateway-config").Parse(dynamicControlPlaneConfig)
	if err != nil {
		return "", fmt.Errorf("failed to parse config template: %w", err)
	}
	// execute the template
	var buff bytes.Buffer
	err = t.Execute(&buff, data)
	if err != nil {
		return "", fmt.Errorf("error executing config template: %w", err)
	}
	return buff.String(), nil
}

func EnsureProxy(ctx context.Context, client kubernetes.Interface, gw *gatewayv1.Gateway, xdsServer *xds.Server) (string, error) {
	r := &resourceRender{
		gw:     gw,
		nodeID: proxyName(gw.Namespace, gw.Name),
	}
	logger := klog.FromContext(ctx).WithValues("resourceName", klog.KRef(constants.AgenticNetSystemNamespace, r.nodeID))
	ctx = klog.NewContext(ctx, logger)

	if err := ensureSA(ctx, client, r); err != nil {
		return "", err
	}

	if err := ensureConfigMap(ctx, client, r); err != nil {
		return "", err
	}

	if err := ensureDeployment(ctx, client, r); err != nil {
		return "", err
	}

	if err := ensureService(ctx, client, r); err != nil {
		return "", err
	}

	return r.nodeID, nil
}

func ensureSA(ctx context.Context, client kubernetes.Interface, r *resourceRender) error {
	logger := klog.FromContext(ctx)

	sa := r.serviceAccount()
	_, err := client.CoreV1().ServiceAccounts(constants.AgenticNetSystemNamespace).Get(ctx, r.nodeID, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			_, err = client.CoreV1().ServiceAccounts(constants.AgenticNetSystemNamespace).Create(ctx, sa, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("failed to create envoy serviceaccount: %w", err)
			}
		} else {
			return fmt.Errorf("failed to get envoy serviceaccount: %w", err)
		}
	}
	logger.Info("Envoy proxy serviceaccount is ready!")
	return nil
}

func ensureConfigMap(ctx context.Context, client kubernetes.Interface, r *resourceRender) error {
	logger := klog.FromContext(ctx)
	cm, err := r.configMap()
	if err != nil {
		return err
	}

	_, err = client.CoreV1().ConfigMaps(constants.AgenticNetSystemNamespace).Get(ctx, r.nodeID, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			_, err = client.CoreV1().ConfigMaps(constants.AgenticNetSystemNamespace).Create(ctx, cm, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("failed to create envoy configmap: %w", err)
			}
		} else {
			return fmt.Errorf("failed to get envoy configmap: %w", err)
		}
	}

	logger.Info("Envoy bootstrap configmap is ready!")
	return nil
}

func ensureDeployment(ctx context.Context, client kubernetes.Interface, r *resourceRender) error {
	logger := klog.FromContext(ctx)

	deployment := r.deployment()
	_, err := client.AppsV1().Deployments(constants.AgenticNetSystemNamespace).Get(ctx, r.nodeID, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			_, err = client.AppsV1().Deployments(constants.AgenticNetSystemNamespace).Create(ctx, deployment, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("failed to create envoy deployment: %w", err)
			}
		} else {
			return fmt.Errorf("failed to get envoy deployment: %w", err)
		}
	}

	if err := waitForDeploymentAvailable(ctx, client, r.nodeID); err != nil {
		return err
	}
	logger.Info("Envoy proxy deployment is ready!")
	return nil
}

func ensureService(ctx context.Context, client kubernetes.Interface, r *resourceRender) error {
	logger := klog.FromContext(ctx)
	service := r.service()
	_, err := client.CoreV1().Services(constants.AgenticNetSystemNamespace).Get(ctx, r.nodeID, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			_, err = client.CoreV1().Services(constants.AgenticNetSystemNamespace).Create(ctx, service, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("failed to create envoy service: %w", err)
			}
		} else {
			return fmt.Errorf("failed to get envoy service: %w", err)
		}
	}

	if err := waitForServiceReady(ctx, client, r.nodeID); err != nil {
		return err
	}
	logger.Info("Envoy proxy service is ready!")
	return nil
}

func waitForServiceReady(ctx context.Context, client kubernetes.Interface, name string) error {
	logger := klog.FromContext(ctx)
	logger.Info("Waiting for envoy service to be ready...")
	err := wait.PollUntilContextTimeout(ctx, 2*time.Second, 1*time.Minute, true, func(ctx context.Context) (bool, error) {
		svc, err := client.CoreV1().Services(constants.AgenticNetSystemNamespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		if svc.Spec.ClusterIP != "" {
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("waiting for envoy service %s to be ready: %w", name, err)
	}
	return nil
}

func waitForDeploymentAvailable(ctx context.Context, client kubernetes.Interface, name string) error {
	logger := klog.FromContext(ctx)
	logger.Info("Waiting for envoy deployment to be available...")
	err := wait.PollUntilContextTimeout(ctx, 2*time.Second, 1*time.Minute, true, func(ctx context.Context) (bool, error) {
		dep, err := client.AppsV1().Deployments(constants.AgenticNetSystemNamespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		for _, cond := range dep.Status.Conditions {
			if cond.Type == appsv1.DeploymentAvailable && cond.Status == corev1.ConditionTrue {
				return true, nil
			}
		}
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("waiting for envoy deployment %s to be available: %w", name, err)
	}
	return nil
}

func DeleteProxy(ctx context.Context, client kubernetes.Interface, namespace, name string) error {
	nodeID := proxyName(namespace, name)
	logger := klog.FromContext(ctx).WithValues("resourceName", klog.KRef(constants.AgenticNetSystemNamespace, nodeID))

	// Delete Deployment
	err := client.AppsV1().Deployments(constants.AgenticNetSystemNamespace).Delete(ctx, nodeID, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete envoy deployment: %w", err)
	}
	logger.Info("Envoy deployment deleted")

	// Delete Service
	err = client.CoreV1().Services(constants.AgenticNetSystemNamespace).Delete(ctx, nodeID, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete envoy service: %w", err)
	}
	logger.Info("Envoy service deleted")

	// Delete ConfigMap
	err = client.CoreV1().ConfigMaps(constants.AgenticNetSystemNamespace).Delete(ctx, nodeID, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete envoy configmap: %w", err)
	}
	logger.Info("Envoy configmap deleted")

	// Delete ServiceAccount
	err = client.CoreV1().ServiceAccounts(constants.AgenticNetSystemNamespace).Delete(ctx, nodeID, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete envoy serviceaccount: %w", err)
	}
	logger.Info("Envoy serviceaccount deleted")

	// TODO: Clean up xds cache, though it should be ok if we don't.
	return nil
}
