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

package xds

import (
	"context"
	"fmt"
	"net"
	"sort"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"

	clusterv3service "github.com/envoyproxy/go-control-plane/envoy/service/cluster/v3"
	discoveryv3 "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
	endpointv3 "github.com/envoyproxy/go-control-plane/envoy/service/endpoint/v3"
	listenerv3service "github.com/envoyproxy/go-control-plane/envoy/service/listener/v3"
	routev3service "github.com/envoyproxy/go-control-plane/envoy/service/route/v3"
	runtimev3 "github.com/envoyproxy/go-control-plane/envoy/service/runtime/v3"
	secretv3 "github.com/envoyproxy/go-control-plane/envoy/service/secret/v3"
	envoyproxytypes "github.com/envoyproxy/go-control-plane/pkg/cache/types"
	cachev3 "github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	resourcev3 "github.com/envoyproxy/go-control-plane/pkg/resource/v3"
	serverv3 "github.com/envoyproxy/go-control-plane/pkg/server/v3"
	"k8s.io/klog/v2"
)

const (
	grpcKeepaliveTime        = 30 * time.Second
	grpcKeepaliveTimeout     = 5 * time.Second
	grpcKeepaliveMinTime     = 30 * time.Second
	grpcMaxConcurrentStreams = 1000000
)

// Server is the xDS server.
type Server struct {
	cache   cachev3.SnapshotCache
	server  serverv3.Server
	version atomic.Uint64
	Address string
	Port    int
}

// NewServer creates a new xDS server.
func NewServer(ctx context.Context) *Server {
	cache := cachev3.NewSnapshotCache(false, cachev3.IDHash{}, nil)
	server := serverv3.NewServer(ctx, cache, &callbacks{})
	return &Server{
		cache:  cache,
		server: server,
	}
}

// Run starts the xDS server.
func (s *Server) Run(ctx context.Context) error {
	var grpcOptions []grpc.ServerOption
	grpcOptions = append(grpcOptions,
		grpc.MaxConcurrentStreams(grpcMaxConcurrentStreams),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    grpcKeepaliveTime,
			Timeout: grpcKeepaliveTimeout,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             grpcKeepaliveMinTime,
			PermitWithoutStream: true,
		}),
	)
	grpcServer := grpc.NewServer(grpcOptions...)

	discoveryv3.RegisterAggregatedDiscoveryServiceServer(grpcServer, s.server)
	endpointv3.RegisterEndpointDiscoveryServiceServer(grpcServer, s.server)
	clusterv3service.RegisterClusterDiscoveryServiceServer(grpcServer, s.server)
	routev3service.RegisterRouteDiscoveryServiceServer(grpcServer, s.server)
	listenerv3service.RegisterListenerDiscoveryServiceServer(grpcServer, s.server)
	secretv3.RegisterSecretDiscoveryServiceServer(grpcServer, s.server)
	runtimev3.RegisterRuntimeDiscoveryServiceServer(grpcServer, s.server)

	address, err := getControlPlaneAddress()
	if err != nil {
		return err
	}
	// Listen on a random available port.
	listener, err := net.Listen("tcp", fmt.Sprintf("%s:0", address))
	if err != nil {
		return err
	}

	addr := listener.Addr()
	tcpAddr, ok := addr.(*net.TCPAddr)
	if !ok {
		return fmt.Errorf("could not assert listener address to TCPAddr: %s", addr.String())
	}

	s.Address = address
	s.Port = tcpAddr.Port

	klog.Infof("xDS management server listening on %s:%d", s.Address, s.Port)
	go func() {
		if err = grpcServer.Serve(listener); err != nil {
			klog.Errorln("gRPC server error:", err)
		}
	}()

	go func() {
		<-ctx.Done()
		grpcServer.Stop()
	}()

	return nil
}

// UpdateXDSServer updates the xDS server with new resources.
func (s *Server) UpdateXDSServer(ctx context.Context, nodeid string, resources map[resourcev3.Type][]envoyproxytypes.Resource) error {
	s.version.Add(1)
	version := s.version.Load()

	snapshot, err := cachev3.NewSnapshot(fmt.Sprintf("%d", version), resources)
	if err != nil {
		return fmt.Errorf("failed to create new snapshot cache: %v", err)
	}

	if err := s.cache.SetSnapshot(ctx, nodeid, snapshot); err != nil {
		return fmt.Errorf("failed to update resource snapshot in management server: %v", err)
	}
	klog.V(4).Infof("Updated snapshot cache for node %s with version %d", nodeid, version)
	return nil
}

func getControlPlaneAddress() (string, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}

	sort.Slice(interfaces, func(i, j int) bool {
		nameI := interfaces[i].Name
		nameJ := interfaces[j].Name

		if nameI == "docker0" {
			return true
		}
		if nameJ == "docker0" {
			return false
		}

		if nameI == "eth0" {
			return nameJ != "docker0"
		}
		if nameJ == "eth0" {
			return false
		}

		return nameI < nameJ
	})

	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if ok && ipNet.IP.To4() != nil && !ipNet.IP.IsLinkLocalUnicast() && !ipNet.IP.IsLoopback() {
				return ipNet.IP.String(), nil
			}
		}
	}

	return "", fmt.Errorf("no suitable global unicast IPv4 address found on any active non-loopback interface")
}
