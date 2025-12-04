package controller

import (
	"fmt"

	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	agenticv0alpha0 "sigs.k8s.io/kube-agentic-networking/api/v0alpha0"
	agenticinformers "sigs.k8s.io/kube-agentic-networking/k8s/client/informers/externalversions/api/v0alpha0"
)

func (c *Controller) setupBackendEventHandlers(backendInformer agenticinformers.BackendInformer) error {
	_, err := backendInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    c.addBackend,
		UpdateFunc: c.updateBackend,
		DeleteFunc: c.deleteBackend,
	})
	return err
}

func (c *Controller) addBackend(obj interface{}) {
	backend := obj.(*agenticv0alpha0.Backend)
	klog.V(4).InfoS("Adding Backend", "backend", klog.KObj(backend))
	c.enqueueBackend(backend)
}

func (c *Controller) updateBackend(old, new interface{}) {
	oldBackend := old.(*agenticv0alpha0.Backend)
	newBackend := new.(*agenticv0alpha0.Backend)
	klog.V(4).InfoS("Updating Backend", "backend", klog.KObj(oldBackend))
	c.enqueueBackend(newBackend)
}

func (c *Controller) deleteBackend(obj interface{}) {
	backend, ok := obj.(*agenticv0alpha0.Backend)
	if !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			runtime.HandleError(fmt.Errorf("couldn't get object from tombstone %#v", obj))
			return
		}
		backend, ok = tombstone.Obj.(*agenticv0alpha0.Backend)
		if !ok {
			runtime.HandleError(fmt.Errorf("tombstone contained object that is not a Backend %#v", obj))
			return
		}
	}
	klog.V(4).InfoS("Deleting Backend", "backend", klog.KObj(backend))
	c.enqueueBackend(backend)
}

func (c *Controller) enqueueBackend(backend *agenticv0alpha0.Backend) {
	// TODO: Find the HTTPRoutes that reference this Backend, then find the Gateways that reference those HTTPRoutes, and enqueue them.
}
