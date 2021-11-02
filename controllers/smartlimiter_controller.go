/*


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

package controllers

import (
	"context"
	cmap "github.com/orcaman/concurrent-map"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"reflect"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
	"slime.io/slime/framework/apis/config/v1alpha1"
	"slime.io/slime/framework/bootstrap"
	event_source "slime.io/slime/framework/model/source"
	"slime.io/slime/framework/model/source/aggregate"
	"slime.io/slime/framework/model/source/k8s"
	microserviceslimeiov1alpha1 "slime.io/slime/modules/limiter/api/v1alpha1"
	"slime.io/slime/modules/limiter/controllers/multicluster"
	"slime.io/slime/modules/limiter/model"
	"sync"
)

// SmartLimiterReconciler reconciles a SmartLimiter object
type SmartLimiterReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	cfg    *v1alpha1.Limiter
	env    *bootstrap.Environment
	scheme *runtime.Scheme

	metricInfo   cmap.ConcurrentMap
	MetricSource source.Source

	metricInfoLock sync.RWMutex
	eventChan      chan event_source.Event
	source         event_source.Source

	lastUpdatePolicy     microserviceslimeiov1alpha1.SmartLimiterSpec
	lastUpdatePolicyLock *sync.RWMutex

	//globalRateLimitInfo cmap.ConcurrentMap
}

// +kubebuilder:rbac:groups=microservice.slime.io,resources=smartlimiters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=microservice.slime.io,resources=smartlimiters/status,verbs=get;update;patch

func (r *SmartLimiterReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	_ = context.Background()

	instance := &microserviceslimeiov1alpha1.SmartLimiter{}
	err := r.Client.Get(context.TODO(), req.NamespacedName, instance)

	// 异常分支
	if err != nil && !errors.IsNotFound(err) {
		return reconcile.Result{}, err
	}

	// 资源删除
	if err != nil && errors.IsNotFound(err) {
		log.Infof("metricInfo.Pop, name %s, namespace,%s", req.Name, req.Namespace)
		r.metricInfo.Pop(req.Namespace + "/" + req.Name)
		r.source.WatchRemove(req.NamespacedName)
		r.lastUpdatePolicyLock.Lock()
		r.lastUpdatePolicy = microserviceslimeiov1alpha1.SmartLimiterSpec{}
		r.lastUpdatePolicyLock.Unlock()
		//if contain global smart limiter, should delete info in configmap
		refreshConfigMap([]*model.Descriptor{}, r, req.NamespacedName)


		return reconcile.Result{}, err
	}

	if len(instance.Spec.Sets) == 0 {
		log.Infof("sets is nil,continue")
		return reconcile.Result{},nil
	}

	// 资源更新
	r.lastUpdatePolicyLock.RLock()
	if reflect.DeepEqual(instance.Spec, r.lastUpdatePolicy) {
		r.lastUpdatePolicyLock.RUnlock()
		return reconcile.Result{}, nil
	} else {
		r.lastUpdatePolicyLock.RUnlock()
		r.lastUpdatePolicyLock.Lock()
		r.lastUpdatePolicy = instance.Spec
		r.lastUpdatePolicyLock.Unlock()
		r.source.WatchAdd(req.NamespacedName)
	}

	return ctrl.Result{}, nil
}

func (r *SmartLimiterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&microserviceslimeiov1alpha1.SmartLimiter{}).
		Complete(r)
}

func NewReconciler(cfg *v1alpha1.Limiter, mgr ctrl.Manager, env *bootstrap.Environment) *SmartLimiterReconciler {
	log := log.WithField("controllers", "SmartLimiter")
	eventChan := make(chan event_source.Event)
	src := &aggregate.Source{}
	ms, err := k8s.NewMetricSource(eventChan, env)
	if err != nil {
		log.Errorf("failed to create slime-metric,%+v", err)
		return nil
	}
	src.AppendSource(ms)
	f := ms.SourceClusterHandler()
	if cfg.Multicluster {
		mc := multicluster.New(env, []func(*kubernetes.Clientset){f}, nil)
		go mc.Run()
	}
	r := &SmartLimiterReconciler{
		Client:               mgr.GetClient(),
		scheme:               mgr.GetScheme(),
		metricInfo:           cmap.New(),
		eventChan:            eventChan,
		source:               src,
		env:                  env,
		lastUpdatePolicyLock: &sync.RWMutex{},
		//globalRateLimitInfo: cmap.New(),
	}
	r.source.Start(env.Stop)
	r.WatchSource(env.Stop)
	return r
}
