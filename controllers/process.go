/*
* @Author: yangdihang
* @Date: 2020/11/19
 */

package controllers

import (
	"context"
	"fmt"
	"gopkg.in/yaml.v2"
	networking "istio.io/api/networking/v1alpha3"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"reflect"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"slime.io/slime/framework/apis/networking/v1alpha3"
	slime_model "slime.io/slime/framework/model"
	event_source "slime.io/slime/framework/model/source"
	"slime.io/slime/framework/util"
	microservicev1alpha2 "slime.io/slime/modules/limiter/api/v1alpha2"
	"slime.io/slime/modules/limiter/model"
	"strings"
)

func (r *SmartLimiterReconciler) WatchSource(stop <-chan struct{}) {
	//t := time.NewTicker(time.Second * 30)
	go func() {
		for {
			select {
			case <-stop:
				return
			case e := <-r.eventChan:
				switch e.EventType {
				case event_source.Update, event_source.Add:
					if _, err := r.Refresh(reconcile.Request{NamespacedName: e.Loc}, e.Info); err != nil {
						log.Errorf("error:%v", err)
					}
				}
			//case <- t.C:
			//	found := &v1alpha3.EnvoyFilterList{}
			//	if err := r.Client.List(context.TODO(),found,nil); err != nil {
			//
			//	}
			}
		}
	}()
}

func (r *SmartLimiterReconciler) Refresh(request reconcile.Request, args map[string]string) (reconcile.Result, error) {
	_, ok := r.metricInfo.Get(request.Namespace + "/" + request.Name)
	if !ok {
		r.metricInfo.Set(request.Namespace+"/"+request.Name, &slime_model.Endpoints{
			Location: request.NamespacedName,
			Info:     args,
		})
	} else {
		if i, ok := r.metricInfo.Get(request.Namespace + "/" + request.Name); ok {
			if ep, ok := i.(*slime_model.Endpoints); ok {
				ep.Lock.Lock()
				for key, value := range args {
					ep.Info[key] = value
				}
				ep.Lock.Unlock()
			}
		}
	}

	instance := &microservicev1alpha2.SmartLimiter{}
	err := r.Client.Get(context.TODO(), request.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}
	if result, err := r.refresh(instance); err == nil {
		return result, nil
	} else {
		return reconcile.Result{}, err
	}
}

// refresh envoy filters and configmap
func (r *SmartLimiterReconciler) refresh(instance *microservicev1alpha2.SmartLimiter) (reconcile.Result, error) {
	var err error
	loc := types.NamespacedName{
		Namespace: instance.Namespace,
		Name:      instance.Name,
	}
	material := r.getMaterial(loc)
	if instance.Spec.Sets == nil {
		return reconcile.Result{}, util.Error{M: "invalid rateLimit spec with none sets"}
	}
	spec := instance.Spec

	var efs map[string]*networking.EnvoyFilter
	var descriptor map[string]*microservicev1alpha2.SmartLimitDescriptors
	var gdesc []*model.Descriptor

	efs, descriptor, gdesc,err = r.GenerateEnvoyConfigs(spec, material, instance)
	if err != nil {
		return reconcile.Result{},err
	}
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
			} else {
				log.Errorf("proto map err :%+v", err)
			}
		}
		_, err := refreshEnvoyFilter(instance, r, efcr)
		if err != nil {
			log.Errorf("generated/deleted EnvoyFilter %s failed:%+v", efcr.Name, err)
		}
	}
	refreshConfigMap(gdesc, r, loc)
	instance.Status = microservicev1alpha2.SmartLimiterStatus{
		RatelimitStatus: descriptor,
		MetricStatus:    material,
	}
	if err := r.Client.Status().Update(context.TODO(), instance); err != nil {
		return reconcile.Result{}, err
	}
	return reconcile.Result{}, nil
}

// TODO different with old version
// the function will not trigger if subset is changed
// if subset is deleted, how to delete the exist envoyfilters, add anohter function to delete the efs ?
func (r *SmartLimiterReconciler) subscribe(host string, subset interface{}) {
	if name, ns, ok := util.IsK8SService(host); ok {
		loc := types.NamespacedName{Name: name, Namespace: ns}
		instance := &microservicev1alpha2.SmartLimiter{}
		err := r.Client.Get(context.TODO(), loc, instance)
		if err != nil {
			if !errors.IsNotFound(err) {
				log.Errorf("failed to get smartlimiter, host:%s, %+v", host, err)
			}
		} else {
			_, _ = r.refresh(instance)
		}
	}
}

func (r *SmartLimiterReconciler) getMaterial(loc types.NamespacedName) map[string]string {
	if i, ok := r.metricInfo.Get(loc.Namespace + "/" + loc.Name); ok {
		if ep, ok := i.(*slime_model.Endpoints); ok {
			return util.CopyMap(ep.Info)
		}
	}
	return nil
}

func refreshEnvoyFilter(instance *microservicev1alpha2.SmartLimiter, r *SmartLimiterReconciler, obj *v1alpha3.EnvoyFilter) (reconcile.Result, error) {
	name := obj.GetName()
	namespace := obj.GetNamespace()

	if err := controllerutil.SetControllerReference(instance, obj, r.scheme); err != nil {
		return reconcile.Result{}, err
	}
	found := &v1alpha3.EnvoyFilter{}

	err := r.Client.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, found)

	// 如果新构建的envoyfilter 的spec为空， 且查询的有错且不是notfound,那么从新入队列，需要重试，查询没有错误则说明需要删除该obj
	// Delete
	if obj.Spec == nil {
		if err != nil && !errors.IsNotFound(err) {
			return reconcile.Result{}, err
		} else if err == nil {
			err = r.Client.Delete(context.TODO(), obj)
			return reconcile.Result{}, err
		} else {
			// nothing to do
			return reconcile.Result{}, nil
		}
	}

	// Create
	if err != nil && errors.IsNotFound(err) {
		log.Infof("Creating a new EnvoyFilter,%s:%s", namespace, name)
		err = r.Client.Create(context.TODO(), obj)
		if err != nil {
			return reconcile.Result{}, err
		}

		// Pod created successfully - don't requeue
		return reconcile.Result{}, nil
	} else if err != nil {
		return reconcile.Result{}, err
	}

	// TODO: 判断是否需要更新
	// Update
	if !reflect.DeepEqual(found.Spec, obj.Spec) {
		log.Infof("Update a new EnvoyFilter,%s:%s", namespace, name)
		obj.ResourceVersion = found.ResourceVersion
		err = r.Client.Update(context.TODO(), obj)
		if err != nil {
			return reconcile.Result{}, err
		}

		// Pod created successfully - don't requeue
		return reconcile.Result{}, nil
	}
	return reconcile.Result{}, nil
}

// if configmap rate-limit-config not exist, ratelimit server will not running
func refreshConfigMap(desc []*model.Descriptor, r *SmartLimiterReconciler, serviceLoc types.NamespacedName) {

	loc := getConfigMapNamespaceName()

	found := &v1.ConfigMap{}
	err := r.Client.Get(context.TODO(), loc, found)

	if err != nil {
		if errors.IsNotFound(err) {
			log.Errorf("configmap %s:%s is not found, can not refresh configmap", loc.Namespace, loc.Name)
			return
		} else {
			log.Errorf("get configmap %s:%s err: %+v, cant not refresh configmap", loc.Namespace, loc.Name, err.Error())
			return
		}
	}

	config, ok := found.Data[model.ConfigMapConfig]
	if !ok {
		log.Errorf("config.yaml not found in configmap %s:%s", loc.Namespace, loc.Name)
		return
	}
	rc := &model.RateLimitConfig{}
	if err = yaml.Unmarshal([]byte(config), &rc); err != nil {
		log.Infof("unmarshal ratelimitConfig %s err: %+v", config, err.Error())
		return
	}

	newCm := make([]*model.Descriptor, 0)
	serviceInfo := fmt.Sprintf("%s.%s", serviceLoc.Name, serviceLoc.Namespace)
	for _, item := range rc.Descriptors {
		if !strings.Contains(item.Value, serviceInfo) {
			newCm = append(newCm, item)
		}
	}
	newCm = append(newCm, desc...)

	configmap := constructConfigMap(newCm)
	if !reflect.DeepEqual(found.Data, configmap.Data) {
		log.Infof("update configmap %s:%s", loc.Namespace, loc.Name)
		configmap.ResourceVersion = found.ResourceVersion
		err = r.Client.Update(context.TODO(), configmap)
		if err != nil {
			log.Infof("update configmap %s:%s err: %+v", loc.Namespace, loc.Name, err.Error())
			return
		}
	}
}

func constructConfigMap(desc []*model.Descriptor) *v1.ConfigMap {
	rateLimitConfig := &model.RateLimitConfig{
		Domain:      model.Domain,
		Descriptors: desc,
	}
	b, _ := yaml.Marshal(rateLimitConfig)
	loc := getConfigMapNamespaceName()
	configmap := &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      loc.Name,
			Namespace: loc.Namespace,
			Labels:    generateConfigMapLabels(),
		},
		Data: map[string]string{
			model.ConfigMapConfig: string(b),
		},
	}
	return configmap
}

// TODO query from global config
func generateConfigMapLabels() map[string]string {
	labels := make(map[string]string)
	labels["app"] = "rate-limit"
	return labels
}

// TODO
//func (r *SmartLimiterReconciler)SetGlobalConfigMap() {
//	loc := getConfigMapNamespaceName()
//	found := &v1.ConfigMap{}
//	err := r.Client.Get(context.TODO(), loc, found)
//
//	if err != nil && errors.IsNotFound(err) {
//		log.Errorf("configmap %s:%s is not found",loc.Namespace,loc.Name)
//		return
//	}
//	config,ok := found.Data[model.ConfigMapConfig]
//	if !ok {
//		log.Errorf("config.yaml not found in configmap %s:%s",loc.Namespace,loc.Name)
//		return
//	}
//	rc := &model.RateLimitConfig{}
//	if err = yaml.Unmarshal([]byte(config), &rc); err != nil {
//		log.Infof("unmarshal ratelimitConfig %s err: %+v",config, err.Error())
//	}
//}
