/*
* @Author: yangdihang
* @Date: 2020/11/19
 */

package controllers

import (
	"context"
	"fmt"
	"reflect"

	networking "istio.io/api/networking/v1alpha3"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"slime.io/slime/framework/apis/networking/v1alpha3"
	"slime.io/slime/framework/model"
	event_source "slime.io/slime/framework/model/source"
	"slime.io/slime/framework/util"
	microservicev1alpha1 "slime.io/slime/modules/limiter/api/v1alpha1"
)

func (r *SmartLimiterReconciler) getMaterial(loc types.NamespacedName) map[string]string {
	if i, ok := r.metricInfo.Get(loc.Namespace + "/" + loc.Name); ok {
		if ep, ok := i.(*model.Endpoints); ok {
			return util.CopyMap(ep.Info)
		}
	}
	return nil
}

func refreshEnvoyFilter(instance *microservicev1alpha1.SmartLimiter, r *SmartLimiterReconciler, obj *v1alpha3.EnvoyFilter) (reconcile.Result, error) {
	name := obj.GetName()
	namespace := obj.GetNamespace()

	if err := controllerutil.SetControllerReference(instance, obj, r.scheme); err != nil {
		return reconcile.Result{}, err
	}
	istioRev := model.IstioRevFromLabel(instance.Labels)
	model.PatchIstioRevLabel(&obj.Labels, istioRev)

	found := &v1alpha3.EnvoyFilter{}
	nsName := types.NamespacedName{Name: name, Namespace: namespace}
	err := r.Client.Get(context.TODO(), nsName, found)

	if err != nil {
		if errors.IsNotFound(err) {
			found = nil
			err = nil
		} else {
			return reconcile.Result{}, err
		}
	}

	if found == nil {
		if obj.Spec != nil {
			// create
			log.Infof("Creating a new EnvoyFilter %v", nsName)
			err = r.Client.Create(context.TODO(), obj)
			return reconcile.Result{}, err
		}
	} else if model.IstioRevFromLabel(found.Labels) != istioRev {
		log.Errorf("existing envoyfilter %v istioRev %s but our %s, skip ...",
			nsName, model.IstioRevFromLabel(found.Labels), istioRev)
		return reconcile.Result{}, nil
	} else if obj.Spec == nil { // del
		err = r.Client.Delete(context.TODO(), obj)
		if errors.IsNotFound(err) {
			err = nil
		}
		return reconcile.Result{}, err
	} else { // update
		if !reflect.DeepEqual(found.Spec, obj.Spec) {
			log.Infof("Update a new EnvoyFilter,%s:%s", namespace, name)
			obj.ResourceVersion = found.ResourceVersion
			err = r.Client.Update(context.TODO(), obj)

			// Pod created successfully - don't requeue
			return reconcile.Result{}, err
		}
		// no change no update
	}

	return reconcile.Result{}, nil
}

func (r *SmartLimiterReconciler) WatchSource(stop <-chan struct{}) {
	go func() {
		for {
			select {
			case <-stop:
				return
			case e := <-r.eventChan:
				switch e.EventType {
				case event_source.Update, event_source.Add:
					if _, err := r.Refresh(reconcile.Request{NamespacedName: e.Loc}, e.Info); err != nil {
						fmt.Printf("error:%v", err)
					}
				}
			}
		}
	}()
}

func (r *SmartLimiterReconciler) Refresh(request reconcile.Request, args map[string]string) (reconcile.Result, error) {
	_, ok := r.metricInfo.Get(request.Namespace + "/" + request.Name)
	if !ok {
		r.metricInfo.Set(request.Namespace+"/"+request.Name, &model.Endpoints{
			Location: request.NamespacedName,
			Info:     args,
		})
	} else {
		if i, ok := r.metricInfo.Get(request.Namespace + "/" + request.Name); ok {
			if ep, ok := i.(*model.Endpoints); ok {
				ep.Lock.Lock()
				for key, value := range args {
					ep.Info[key] = value
				}
				ep.Lock.Unlock()
			}
		}
	}

	instance := &microservicev1alpha1.SmartLimiter{}
	err := r.Client.Get(context.TODO(), request.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		} else {
			// Error reading the object - requeue the request.
			return reconcile.Result{}, err
		}
	}

	if r.env.RevInScope(model.IstioRevFromLabel(instance.Labels)) {
		log.Debugf("existing smartlimter %v istiorev %s but our %s, skip ...",
			request.NamespacedName, model.IstioRevFromLabel(instance.Labels), r.env.IstioRev())
		return reconcile.Result{}, nil
	}

	if result, err := r.refresh(instance); err == nil {
		return result, nil
	} else {
		return reconcile.Result{}, err
	}
}

func (r *SmartLimiterReconciler) refresh(instance *microservicev1alpha1.SmartLimiter) (reconcile.Result, error) {
	loc := types.NamespacedName{
		Namespace: instance.Namespace,
		Name:      instance.Name,
	}
	material := r.getMaterial(loc)
	if instance.Spec.Sets == nil {
		return reconcile.Result{}, util.Error{M: "invalid rateLimit spec"}
	}
	rateLimitConf := instance.Spec

	var efs map[string]*networking.EnvoyFilter
	var descriptor map[string]*microservicev1alpha1.SmartLimitDescriptors

	// TODO: Since the com.netease.local_flow_control has not yet been opened, this function is disabled
	/*if backend == config.Limiter_netEaseLocalFlowControl {
		ef, descriptor = r.GenerateNeteaseFlowControl(rateLimitConf, material, instance)
	}*/

	efs, descriptor = r.GenerateEnvoyLocalLimit(rateLimitConf, material, instance)
	for k, ef := range efs {
		var efcr *v1alpha3.EnvoyFilter
		if k == util.Wellkonw_BaseSet {
			efcr = &v1alpha3.EnvoyFilter{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("%s.%s.ratelimit", instance.Name, instance.Namespace),
					Namespace: instance.Namespace,
				},
			}
		} else {
			efcr = &v1alpha3.EnvoyFilter{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("%s.%s.%s.ratelimit", instance.Name, instance.Namespace, k),
					Namespace: instance.Namespace,
				},
			}
		}
		if ef != nil {
			if mi, err := util.ProtoToMap(ef); err == nil {
				efcr.Spec = mi
			}
		}

		_, err := refreshEnvoyFilter(instance, r, efcr)
		if err != nil {
			log.Errorf("generated/deleted EnvoyFilter %s failed:%+v", efcr.Name, err)
		}
	}

	instance.Status = microservicev1alpha1.SmartLimiterStatus{
		RatelimitStatus: descriptor,
		MetricStatus:    material,
	}
	if err := r.Client.Status().Update(context.TODO(), instance); err != nil {
		return reconcile.Result{}, err
	}
	return reconcile.Result{}, nil
}

func (r *SmartLimiterReconciler) subscribe(host string, subset interface{}) {
	if name, ns, ok := util.IsK8SService(host); ok {
		loc := types.NamespacedName{Name: name, Namespace: ns}
		instance := &microservicev1alpha1.SmartLimiter{}
		err := r.Client.Get(context.TODO(), loc, instance)
		if err != nil {
			if !errors.IsNotFound(err) {
				log.Errorf("failed to get smartlimiter, host:%s, %v", host, err)
			}
		} else if !r.env.RevInScope(model.IstioRevFromLabel(instance.Labels)) {
			log.Debugf("existing smartlimter %v istiorev %s but our %s, skip...",
				loc, model.IstioRevFromLabel(instance.Labels), r.env.IstioRev())
		} else {
			_, err = r.refresh(instance)
			if err != nil {
				log.Errorf("failed to refresh instance %v, err %v", loc, err)
			}
		}
	}
}
