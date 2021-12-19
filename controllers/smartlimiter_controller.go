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
	stderrors "errors"
	cmap "github.com/orcaman/concurrent-map"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"reflect"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
	"slime.io/slime/framework/apis/config/v1alpha1"
	"slime.io/slime/framework/bootstrap"
	slime_model "slime.io/slime/framework/model"
	"slime.io/slime/framework/model/metric"
	"slime.io/slime/framework/model/trigger"
	microservicev1alpha2 "slime.io/slime/modules/limiter/api/v1alpha2"
	"slime.io/slime/modules/limiter/model"
	"sync"
	"time"
)

// SmartLimiterReconciler reconciles a SmartLimiter object
type SmartLimiterReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	cfg    *v1alpha1.Limiter
	env    bootstrap.Environment
	scheme *runtime.Scheme


	interest cmap.ConcurrentMap
	// reuse, or use anther filed to store interested nn
	// key is the interested namespace/name
	// value is the metricInfo
	metricInfo   cmap.ConcurrentMap
	MetricSource source.Source

	metricInfoLock sync.RWMutex

	lastUpdatePolicy     microservicev1alpha2.SmartLimiterSpec
	lastUpdatePolicyLock *sync.RWMutex

	watcherMetricChan    <-chan metric.Metric
	tickerMetricChan     <-chan metric.Metric
	//Interest     cmap.ConcurrentMap
}

// +kubebuilder:rbac:groups=microservice.slime.io,resources=smartlimiters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=microservice.slime.io,resources=smartlimiters/status,verbs=get;update;patch

func (r *SmartLimiterReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	_ = context.Background()

	instance := &microservicev1alpha2.SmartLimiter{}
	if err := r.Client.Get(context.TODO(), req.NamespacedName, instance); err != nil {
		if errors.IsNotFound(err) {
			instance = nil
			err = nil
			log.Infof("smartlimiter %v not found", req.NamespacedName)
		} else {
			log.Errorf("get smartlimiter %v err, %s",req.NamespacedName,err)
			return reconcile.Result{},err
		}
	}

	// deleted
	if instance == nil {
		log.Infof("metricInfo.Pop, name %s, namespace,%s", req.Name, req.Namespace)
		r.metricInfo.Pop(req.Namespace + "/" + req.Name)
		r.interest.Pop(req.Namespace + "/" + req.Name)
		r.lastUpdatePolicyLock.Lock()
		r.lastUpdatePolicy = microservicev1alpha2.SmartLimiterSpec{}
		r.lastUpdatePolicyLock.Unlock()
		//if contain global smart limiter, should delete info in configmap
		refreshConfigMap([]*model.Descriptor{}, r, req.NamespacedName)
		return reconcile.Result{}, nil
	} else {
		// add or update

		if !r.env.RevInScope(slime_model.IstioRevFromLabel(instance.Labels)) {
			log.Debugf("existing smartlimiter %v istiorev %s but our %s, skip ...",
				req.NamespacedName, slime_model.IstioRevFromLabel(instance.Labels), r.env.IstioRev())
			return ctrl.Result{}, nil
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
			r.interest.Set(req.Namespace+"/"+req.Name, struct {}{})
			//r.handleEvent(types.NamespacedName{
			//	Namespace: req.Namespace,
			//	Name:      req.Name,
			//})
		}
	}
	return ctrl.Result{}, nil
}

func (r *SmartLimiterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&microservicev1alpha2.SmartLimiter{}).
		Complete(r)
}


func NewReconciler(cfg *v1alpha1.Limiter, mgr ctrl.Manager, env bootstrap.Environment) *SmartLimiterReconciler {
	log := log.WithField("controllers", "SmartLimiter")
	// generate producer config
	pc, err := newProducerConfig(env)
	if err != nil {
		log.Errorf("%v", err)
		return nil
	}
	r := &SmartLimiterReconciler{
		Client:               mgr.GetClient(),
		scheme:               mgr.GetScheme(),
		metricInfo:           cmap.New(),
		interest: 			  cmap.New(),
		env:                  env,
		lastUpdatePolicyLock: &sync.RWMutex{},
		watcherMetricChan:    pc.WatcherProducerConfig.MetricChan,
		tickerMetricChan:     pc.TickerProducerConfig.MetricChan,
	}
	// reconciler defines producer metric handler
	pc.WatcherProducerConfig.NeedUpdateMetricHandler = r.handleWatcherEvent
	pc.TickerProducerConfig.NeedUpdateMetricHandler = r.handleTickerEvent
	// start producer
	metric.NewProducer(pc)
	log.Infof("producers starts")

	if env.Config.Metric != nil {
		go r.WatchMetric()
	}

	return r
}

//func NewReconciler(cfg *v1alpha1.Limiter, mgr ctrl.Manager, env *bootstrap.Environment) *SmartLimiterReconciler {
//	log := log.WithField("controllers", "SmartLimiter")
//	eventChan := make(chan event_source.Event)
//	src := &aggregate.Source{}
//	ms, err := k8s.NewMetricSource(eventChan, env)
//	if err != nil {
//		log.Errorf("failed to create slime-metric,%+v", err)
//		return nil
//	}
//	src.AppendSource(ms)
//	f := ms.SourceClusterHandler()
//	if cfg.Multicluster {
//		mc := multicluster.New(env, []func(*kubernetes.Clientset){f}, nil)
//		go mc.Run()
//	}
//	r := &SmartLimiterReconciler{
//		Client:               mgr.GetClient(),
//		scheme:               mgr.GetScheme(),
//		metricInfo:           cmap.New(),
//		eventChan:            eventChan,
//		source:               src,
//		env:                  env,
//		lastUpdatePolicyLock: &sync.RWMutex{},
//		//globalRateLimitInfo: cmap.New(),
//	}
//	r.source.Start(env.Stop)
//	r.WatchSource(env.Stop)
//	return r
//}


func newProducerConfig(env bootstrap.Environment) (*metric.ProducerConfig, error) {

	// init metric source
	var enablePrometheusSource bool
	var prometheusSourceConfig metric.PrometheusSourceConfig
	var err error

	switch env.Config.Global.Misc[model.MetricSourceType] {
	case model.MetricSourceTypePrometheus:
		enablePrometheusSource = true
		prometheusSourceConfig, err = newPrometheusSourceConfig(env)
		if err != nil {
			return nil, err
		}
	default:
		return nil, stderrors.New("wrong metric_source_type")
	}

	// init whole producer config
	pc := &metric.ProducerConfig{
		EnablePrometheusSource: enablePrometheusSource,
		PrometheusSourceConfig: prometheusSourceConfig,
		EnableWatcherProducer:  true,
		WatcherProducerConfig: metric.WatcherProducerConfig{
			Name:       "smartLimiter-watcher",
			MetricChan: make(chan metric.Metric),
			WatcherTriggerConfig: trigger.WatcherTriggerConfig{
				Kinds: []schema.GroupVersionKind{
					{
						Group:   "",
						Version: "v1",
						Kind:    "Endpoints",
					},
				},
				EventChan:     make(chan trigger.WatcherEvent),
				DynamicClient: env.DynamicClient,
			},
		},
		EnableTickerProducer: true,
		TickerProducerConfig: metric.TickerProducerConfig{
			Name:       "smartLimiter-ticker",
			MetricChan: make(chan metric.Metric),
			TickerTriggerConfig: trigger.TickerTriggerConfig{
				Durations: []time.Duration{
					10 * time.Second,
				},
				EventChan: make(chan trigger.TickerEvent),
			},
		},
		StopChan: env.Stop,
	}

	return pc, nil

}
