package controller

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	cachev3 "github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	serverv3 "github.com/envoyproxy/go-control-plane/pkg/server/v3"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	corev1informers "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	gatewayclient "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned"
	gatewayinformers "sigs.k8s.io/gateway-api/pkg/client/informers/externalversions/apis/v1"
	gatewaylisters "sigs.k8s.io/gateway-api/pkg/client/listers/apis/v1"

	agenticclient "sigs.k8s.io/kube-agentic-networking/k8s/client/clientset/versioned"
	agenticinformers "sigs.k8s.io/kube-agentic-networking/k8s/client/informers/externalversions/api/v0alpha0"
	agenticlisters "sigs.k8s.io/kube-agentic-networking/k8s/client/listers/api/v0alpha0"
)

const (
	controllerName = "sig.k8s.io/kube-agentic-networking-controller"
)

// Controller is the controller implementation for Gateway resources
type Controller struct {
	kubeClient    kubernetes.Interface
	gwClient      gatewayclient.Interface
	agenticClient agenticclient.Interface

	namespaceLister       corev1listers.NamespaceLister
	namespaceListerSynced cache.InformerSynced

	serviceLister       corev1listers.ServiceLister
	serviceListerSynced cache.InformerSynced

	gatewayClassLister       gatewaylisters.GatewayClassLister
	gatewayClassListerSynced cache.InformerSynced

	gatewayLister       gatewaylisters.GatewayLister
	gatewayListerSynced cache.InformerSynced
	gatewayqueue        workqueue.TypedRateLimitingInterface[string]

	httprouteLister       gatewaylisters.HTTPRouteLister
	httprouteListerSynced cache.InformerSynced

	backendLister       agenticlisters.BackendLister
	backendListerSynced cache.InformerSynced

	accessPolicyLister       agenticlisters.AccessPolicyLister
	accessPolicyListerSynced cache.InformerSynced

	xdscache        cachev3.SnapshotCache
	xdsserver       serverv3.Server
	xdsLocalAddress string
	xdsLocalPort    int
	xdsVersion      atomic.Uint64
}

// New returns a new *Controller with the event handlers setup for types we are interested in.
func New(
	ctx context.Context,
	kubeClientSet kubernetes.Interface,
	gwClientSet gatewayclient.Interface,
	agenticClientSet agenticclient.Interface,
	namespaceInformer corev1informers.NamespaceInformer,
	serviceInformer corev1informers.ServiceInformer,
	gatewayClassInformer gatewayinformers.GatewayClassInformer,
	gatewayInformer gatewayinformers.GatewayInformer,
	httprouteInformer gatewayinformers.HTTPRouteInformer,
	backendInformer agenticinformers.BackendInformer,
	accessPolicyInformer agenticinformers.AccessPolicyInformer,
) (*Controller, error) {
	c := &Controller{
		kubeClient:               kubeClientSet,
		gwClient:                 gwClientSet,
		agenticClient:            agenticClientSet,
		namespaceLister:          namespaceInformer.Lister(),
		namespaceListerSynced:    namespaceInformer.Informer().HasSynced,
		serviceLister:            serviceInformer.Lister(),
		serviceListerSynced:      serviceInformer.Informer().HasSynced,
		gatewayClassLister:       gatewayClassInformer.Lister(),
		gatewayClassListerSynced: gatewayClassInformer.Informer().HasSynced,
		gatewayLister:            gatewayInformer.Lister(),
		gatewayListerSynced:      gatewayInformer.Informer().HasSynced,
		gatewayqueue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[string](),
			workqueue.TypedRateLimitingQueueConfig[string]{Name: "gateway"},
		),
		httprouteLister:          httprouteInformer.Lister(),
		httprouteListerSynced:    httprouteInformer.Informer().HasSynced,
		backendLister:            backendInformer.Lister(),
		backendListerSynced:      backendInformer.Informer().HasSynced,
		accessPolicyLister:       accessPolicyInformer.Lister(),
		accessPolicyListerSynced: accessPolicyInformer.Informer().HasSynced,
	}

	// Setup event handlers for all relevant resources.
	if err := c.setupGatewayClassEventHandlers(gatewayClassInformer); err != nil {
		return nil, err
	}
	if err := c.setupGatewayEventHandlers(gatewayInformer); err != nil {
		return nil, err
	}
	if err := c.setupHTTPRouteEventHandlers(httprouteInformer); err != nil {
		return nil, err
	}
	if err := c.setupBackendEventHandlers(backendInformer); err != nil {
		return nil, err
	}
	if err := c.setupAccessPolicyEventHandlers(accessPolicyInformer); err != nil {
		return nil, err
	}

	return c, nil
}

// Run will
// - sync informer caches and start workers.
// - start the xDS server
func (c *Controller) Run(ctx context.Context, workers int) error {
	defer runtime.HandleCrashWithContext(ctx)
	defer c.gatewayqueue.ShutDown()

	// TODO: Start the Envoy xDS server.
	klog.Info("Starting the Envoy xDS server")

	klog.Info("Waiting for informer caches to sync")
	if ok := cache.WaitForCacheSync(ctx.Done(),
		c.namespaceListerSynced,
		c.serviceListerSynced,
		c.gatewayClassListerSynced,
		c.gatewayListerSynced,
		c.httprouteListerSynced,
		c.backendListerSynced,
		c.accessPolicyListerSynced); !ok {
		return errors.New("failed to wait for caches to sync")
	}

	klog.InfoS("Starting workers", "count", workers)
	for i := 0; i < workers; i++ {
		go wait.UntilWithContext(ctx, c.runWorker, time.Second)
	}

	klog.Info("Started workers")
	<-ctx.Done()
	klog.Info("Shutting down workers")

	return nil
}

// runWorker is a long-running function that will continually call the
// processNextWorkItem function in order to read and process a message on the
// workqueue.
func (c *Controller) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

// processNextWorkItem will read a single work item off the workqueue and
// attempt to process it, by calling the syncHandler.
func (c *Controller) processNextWorkItem(ctx context.Context) bool {
	obj, shutdown := c.gatewayqueue.Get()
	if shutdown {
		return false
	}
	defer c.gatewayqueue.Done(obj)

	// We expect strings (namespace/name) to come off the workqueue.
	if err := c.syncHandler(ctx, obj); err != nil {
		// Put the item back on the workqueue to handle any transient errors.
		c.gatewayqueue.AddRateLimited(obj)
		klog.ErrorS(err, "Error syncing", "key", obj)
		return true
	}

	// Finally, if no error occurs we Forget this item so it does not
	// get queued again until another change happens.
	c.gatewayqueue.Forget(obj)
	klog.InfoS("Successfully synced", "key", obj)
	return true
}

// syncHandler compares the actual state with the desired, and attempts to
// converge the two.
func (c *Controller) syncHandler(ctx context.Context, key string) error {
	// Convert the namespace/name string into a distinct namespace and name
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		runtime.HandleError(fmt.Errorf("invalid resource key: %s", key))
		return nil
	}

	klog.InfoS("Syncing gateway", "gateway", klog.KRef(namespace, name))

	// Get the Gateway resource with this namespace/name
	gateway, err := c.gatewayLister.Gateways(namespace).Get(name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			klog.InfoS("Gateway deleted", "gateway", klog.KRef(namespace, name))
			return nil
		}
		return err
	}
	klog.InfoS("Gateway created or updated", "gateway", klog.KObj(gateway))

	// TODO: Implement the reconciliation logic here.
	// This will involve:
	// 1. Finding all relevant resources (HTTPRoutes, Backends, Services, AccessPolicies).
	// 2. Validating them.
	// 3. Generating an Envoy configuration snapshot.
	// 4. Updating the xDS cache with the new snapshot.

	klog.InfoS("Finished syncing gateway", "gateway", klog.KRef(namespace, name))
	return nil
}
