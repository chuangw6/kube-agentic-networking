package controller

import (
	"fmt"

	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayinformers "sigs.k8s.io/gateway-api/pkg/client/informers/externalversions/apis/v1"
)

func (c *Controller) setupHTTPRouteEventHandlers(httprouteInformer gatewayinformers.HTTPRouteInformer) error {
	_, err := httprouteInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    c.addHTTPRoute,
		UpdateFunc: c.updateHTTPRoute,
		DeleteFunc: c.deleteHTTPRoute,
	})
	return err
}

func (c *Controller) addHTTPRoute(obj interface{}) {
	route := obj.(*gatewayv1.HTTPRoute)
	klog.V(4).InfoS("Adding HTTPRoute", "httproute", klog.KObj(route))
	c.enqueueGatewaysForHTTPRoute(route.Spec.ParentRefs, route.Namespace)
}

func (c *Controller) updateHTTPRoute(old, new interface{}) {
	oldRoute := old.(*gatewayv1.HTTPRoute)
	newRoute := new.(*gatewayv1.HTTPRoute)
	klog.V(4).InfoS("Updating HTTPRoute", "httproute", klog.KObj(oldRoute))
	c.enqueueGatewaysForHTTPRoute(append(oldRoute.Spec.ParentRefs, newRoute.Spec.ParentRefs...), newRoute.Namespace)
}

func (c *Controller) deleteHTTPRoute(obj interface{}) {
	route, ok := obj.(*gatewayv1.HTTPRoute)
	if !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			runtime.HandleError(fmt.Errorf("couldn't get object from tombstone %#v", obj))
			return
		}
		route, ok = tombstone.Obj.(*gatewayv1.HTTPRoute)
		if !ok {
			runtime.HandleError(fmt.Errorf("tombstone contained object that is not a HTTPRoute %#v", obj))
			return
		}
	}
	klog.V(4).InfoS("Deleting HTTPRoute", "httproute", klog.KObj(route))
	c.enqueueGatewaysForHTTPRoute(route.Spec.ParentRefs, route.Namespace)
}

func (c *Controller) enqueueGatewaysForHTTPRoute(references []gatewayv1.ParentReference, localNamespace string) {
	gatewaysToEnqueue := make(map[string]struct{})
	for _, ref := range references {
		if (ref.Group != nil && string(*ref.Group) != gatewayv1.GroupName) ||
			(ref.Kind != nil && string(*ref.Kind) != "Gateway") {
			continue
		}
		namespace := localNamespace
		if ref.Namespace != nil {
			namespace = string(*ref.Namespace)
		}
		key := namespace + "/" + string(ref.Name)
		gatewaysToEnqueue[key] = struct{}{}
	}

	for key := range gatewaysToEnqueue {
		c.gatewayqueue.Add(key)
	}
}
