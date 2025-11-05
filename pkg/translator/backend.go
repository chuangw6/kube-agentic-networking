package translator

import (
	"fmt"
	"time"

	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	tlsv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/tls/v3"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	corev1listers "k8s.io/client-go/listers/core/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"
	gatewaylistersv1beta1 "sigs.k8s.io/gateway-api/pkg/client/listers/apis/v1beta1"
	agenticv1alpha1 "sigs.k8s.io/kube-agentic-networking/api/agentic/v1alpha1"
	agenticlisters "sigs.k8s.io/kube-agentic-networking/k8s/client/listers/agentic/v1alpha1"
)

const (
	// The timeout for new network connections to hosts in the cluster.
	defaultConnectTimeout = 5 * time.Second
)

func fetchBackend(namespace string, backendRef gatewayv1.BackendRef, backendLister agenticlisters.BackendLister, serviceLister corev1listers.ServiceLister, referenceGrantLister gatewaylistersv1beta1.ReferenceGrantLister) (*agenticv1alpha1.Backend, error) {
	// 1. Validate that the Kind is Backend.
	if backendRef.Kind != nil && *backendRef.Kind != "Backend" {
		return nil, &ControllerError{
			Reason:  string(gatewayv1.RouteReasonInvalidKind),
			Message: fmt.Sprintf("unsupported backend kind: %s", *backendRef.Kind),
		}
	}

	ns := namespace
	if backendRef.Namespace != nil {
		ns = string(*backendRef.Namespace)
	}

	// 2. If it's a cross-namespace reference, we must check for a ReferenceGrant.
	if ns != namespace {
		from := gatewayv1beta1.ReferenceGrantFrom{
			Group:     gatewayv1.GroupName,
			Kind:      "HTTPRoute",
			Namespace: gatewayv1.Namespace(namespace),
		}
		to := gatewayv1beta1.ReferenceGrantTo{
			Group: "", // Core group for Service
			Kind:  "Service",
			Name:  &backendRef.Name,
		}

		if !isCrossNamespaceRefAllowed(from, to, ns, referenceGrantLister) {
			// The reference is not permitted.
			return nil, &ControllerError{
				Reason:  string(gatewayv1.RouteReasonRefNotPermitted),
				Message: "permission error",
			}
		}
	}

	// 3. Fetch the Backend resource.
	backend, err := backendLister.Backends(ns).Get(string(backendRef.Name))
	if err != nil {
		return nil, &ControllerError{
			Reason:  string(gatewayv1.RouteReasonBackendNotFound),
			Message: fmt.Sprintf("failed to get Backend %s/%s: %v", ns, backendRef.Name, err),
		}
	}

	// 4. Check if the referenced Service exists.
	if svc := backend.Spec.MCP.ServiceName; svc != "" {
		if _, err := serviceLister.Services(ns).Get(string(svc)); err != nil {
			fmt.Printf("Service lookup error for backend %s/%s, error: %v\n", ns, backendRef.Name, err)
			return nil, &ControllerError{
				Reason:  string(gatewayv1.RouteReasonBackendNotFound),
				Message: fmt.Sprintf("failed to get Backend service %s/%s: %v", ns, svc, err),
			}
		}
	}

	// TODO: Do we need to check hostname resolution for external MCP backends?
	return backend, nil
}

func convertBackendToCluster(backend *agenticv1alpha1.Backend) (*clusterv3.Cluster, error) {
	clusterName := fmt.Sprintf(ClusterNameFormat, backend.Namespace, backend.Name)

	// Create the base cluster configuration.
	cluster := &clusterv3.Cluster{
		Name:           clusterName,
		ConnectTimeout: durationpb.New(defaultConnectTimeout),
	}

	if backend.Spec.MCP.ServiceName != "" {
		// For in-cluster services, use the FQDN.
		serviceFQDN := fmt.Sprintf("%s.%s.svc.cluster.local", backend.Spec.MCP.ServiceName, backend.Namespace)
		cluster.ClusterDiscoveryType = &clusterv3.Cluster_Type{Type: clusterv3.Cluster_STRICT_DNS}
		cluster.LoadAssignment = createClusterLoadAssignment(clusterName, serviceFQDN, uint32(backend.Spec.MCP.Port))
		return cluster, nil
	}

	// External MCP backend specified via backend.Spec.MCP.Hostname
	cluster.ClusterDiscoveryType = &clusterv3.Cluster_Type{Type: clusterv3.Cluster_LOGICAL_DNS}
	cluster.LoadAssignment = createClusterLoadAssignment(clusterName, backend.Spec.MCP.Hostname, uint32(backend.Spec.MCP.Port))
	// TODO: A new field will probably be added to Backend to allow configuring TLS for external MCP backends.
	// For now, we always enable TLS for external MCP backends.
	if true {
		tlsContext := &tlsv3.UpstreamTlsContext{
			Sni: backend.Spec.MCP.Hostname,
		}
		any, err := anypb.New(tlsContext)
		if err != nil {
			return nil, err
		}
		cluster.TransportSocket = &corev3.TransportSocket{
			Name: "envoy.transport_sockets.tls",
			ConfigType: &corev3.TransportSocket_TypedConfig{
				TypedConfig: any,
			},
		}
	}

	return cluster, nil
}

func buildClustersFromBackends(backends []*agenticv1alpha1.Backend) ([]*clusterv3.Cluster, error) {
	var clusters []*clusterv3.Cluster
	for _, backend := range backends {
		cluster, err := convertBackendToCluster(backend)
		if err != nil {
			return nil, err
		}
		clusters = append(clusters, cluster)
	}
	return clusters, nil
}
