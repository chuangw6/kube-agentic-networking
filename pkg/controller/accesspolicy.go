package controller

import (
	"fmt"

	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	agenticv0alpha0 "sigs.k8s.io/kube-agentic-networking/api/v0alpha0"
	agenticinformers "sigs.k8s.io/kube-agentic-networking/k8s/client/informers/externalversions/api/v0alpha0"
)

func (c *Controller) setupAccessPolicyEventHandlers(accessPolicyInformer agenticinformers.AccessPolicyInformer) error {
	_, err := accessPolicyInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    c.addAccessPolicy,
		UpdateFunc: c.updateAccessPolicy,
		DeleteFunc: c.deleteAccessPolicy,
	})
	return err
}

func (c *Controller) addAccessPolicy(obj interface{}) {
	policy := obj.(*agenticv0alpha0.AccessPolicy)
	klog.V(4).InfoS("Adding AccessPolicy", "accesspolicy", klog.KObj(policy))
	c.enqueueAccessPolicy(policy)
}

func (c *Controller) updateAccessPolicy(old, new interface{}) {
	oldPolicy := old.(*agenticv0alpha0.AccessPolicy)
	newPolicy := new.(*agenticv0alpha0.AccessPolicy)
	klog.V(4).InfoS("Updating AccessPolicy", "accesspolicy", klog.KObj(oldPolicy))
	c.enqueueAccessPolicy(newPolicy)
}

func (c *Controller) deleteAccessPolicy(obj interface{}) {
	policy, ok := obj.(*agenticv0alpha0.AccessPolicy)
	if !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			runtime.HandleError(fmt.Errorf("couldn't get object from tombstone %#v", obj))
			return
		}
		policy, ok = tombstone.Obj.(*agenticv0alpha0.AccessPolicy)
		if !ok {
			runtime.HandleError(fmt.Errorf("tombstone contained object that is not a AccessPolicy %#v", obj))
			return
		}
	}
	klog.V(4).InfoS("Deleting AccessPolicy", "accesspolicy", klog.KObj(policy))
	c.enqueueAccessPolicy(policy)
}

func (c *Controller) enqueueAccessPolicy(policy *agenticv0alpha0.AccessPolicy) {
	// TODO: Find the Backends that are targeted by this AccessPolicy, then find the HTTPRoutes that reference those Backends, then find the Gateways that reference those HTTPRoutes, and enqueue them.
}
