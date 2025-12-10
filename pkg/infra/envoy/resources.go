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
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	"sigs.k8s.io/kube-agentic-networking/pkg/constants"
)

const (
	// envoyBootstrapCfgFileName is the name of the Envoy configuration file.
	envoyBootstrapCfgFileName = "envoy.yaml"
)

type resourceRender struct {
	gw         *gatewayv1.Gateway
	nodeID     string
	envoyImage string
}

// Create ConfigMap for envoy bootstrap config
func (r *resourceRender) configMap() (*corev1.ConfigMap, error) {
	bootstrap, err := generateEnvoyBootstrapConfig(types.NamespacedName{
		Namespace: r.gw.Namespace,
		Name:      r.gw.Name,
	}.String(), r.nodeID)
	if err != nil {
		return nil, err
	}

	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.nodeID,
			Namespace: constants.AgenticNetSystemNamespace,
		},
		Data: map[string]string{
			envoyBootstrapCfgFileName: bootstrap,
		},
	}, nil
}

func (r *resourceRender) deployment() *appsv1.Deployment {
	replicas := int32(1)
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.nodeID,
			Namespace: constants.AgenticNetSystemNamespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": r.nodeID,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": r.nodeID,
					},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: r.nodeID,
					Containers: []corev1.Container{
						{
							Name:    "envoy-proxy",
							Image:   r.envoyImage,
							Command: []string{"envoy", "-c", "/etc/envoy/envoy.yaml", "--log-level", "debug"},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "envoy-config",
									MountPath: "/etc/envoy",
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "envoy-config",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: r.nodeID,
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

func (r *resourceRender) service() *corev1.Service {
	ports := []corev1.ServicePort{}
	for _, listener := range r.gw.Spec.Listeners {
		ports = append(ports, corev1.ServicePort{
			Name:     string(listener.Name),
			Port:     int32(listener.Port),
			Protocol: corev1.ProtocolTCP,
		})
	}

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.nodeID,
			Namespace: constants.AgenticNetSystemNamespace,
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP,
			Selector: map[string]string{
				"app": r.nodeID,
			},
			Ports: ports,
		},
	}
}

func (r *resourceRender) serviceAccount() *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.nodeID,
			Namespace: constants.AgenticNetSystemNamespace,
		},
	}
}
