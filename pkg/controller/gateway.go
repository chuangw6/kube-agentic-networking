package controller

import (
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	gatewayinformers "sigs.k8s.io/gateway-api/pkg/client/informers/externalversions/apis/v1"
)

func (c *Controller) setupGatewayEventHandlers(gatewayInformer gatewayinformers.GatewayInformer) error {
	_, err := gatewayInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
			if err == nil {
				c.gatewayqueue.Add(key)
			}
			klog.InfoS("Gateway added", "gateway", key)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(newObj)
			if err == nil {
				c.gatewayqueue.Add(key)
			}
			klog.InfoS("Gateway updated", "gateway", key)
		},
		DeleteFunc: func(obj interface{}) {
			key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
			if err == nil {
				c.gatewayqueue.Add(key)
			}
			klog.InfoS("Gateway deleted", "gateway", key)
		},
	})
	return err
}
