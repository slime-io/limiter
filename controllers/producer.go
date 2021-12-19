package controllers

import (
	stderrors "errors"
	"fmt"
	prometheusApi "github.com/prometheus/client_golang/api"
	prometheusV1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"istio.io/api/networking/v1alpha3"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/client-go/kubernetes"
	"slime.io/slime/framework/apis/config/v1alpha1"
	"slime.io/slime/framework/bootstrap"
	"slime.io/slime/framework/controllers"
	"slime.io/slime/framework/model/metric"
	"slime.io/slime/framework/model/trigger"
	"slime.io/slime/framework/util"
	"strings"
)

type StaticMeta struct {
	Name string  	`json:"name"`
	Namespace string `json:"namespace"`
	NPod map[string]int `json:"nPod"`
	IsGroup map[string]bool `json:"isGroup"`
}

// String returns the general purpose string representation
func (n StaticMeta) String() string {
	b, err := json.Marshal(n)
	if err != nil {
		log.Errorf("marshal meta err :%v", err.Error())
		return ""
	}
	return string(b)
}


func (r *SmartLimiterReconciler) handleWatcherEvent(event trigger.WatcherEvent) metric.QueryMap {
	log.Infof("trigger watchEvent %v",event)
	return r.handleEvent(event.NN)
}

func (r *SmartLimiterReconciler) handleTickerEvent(event trigger.TickerEvent) metric.QueryMap {

	log.Infof("trigger tickerEvent")
	queryMap := make(map[string][]metric.Handler,0)
	// 遍历感兴趣列表
	for k := range r.interest.Items() {
		item := strings.Split(k, "/")
		namespace, name := item[0], item[1]
		qm := r.handleEvent(types.NamespacedName{
			Namespace: namespace,
			Name:      name,
		})
		for meta,handlers := range qm {
			queryMap[meta] = handlers
		}
	}
	return queryMap
}

func (r *SmartLimiterReconciler) handleEvent(loc types.NamespacedName) metric.QueryMap{

	queryMap := make(map[string][]metric.Handler,0)

	if _, ok := r.interest.Get(loc.Namespace + "/" + loc.Name); !ok {
		//log.Infof("not interested in %+v, skip",loc)
		return queryMap
	}

	// handler is  map["cpu.max"] = > max(container_cpu_usage_seconds_total{namespace="$namespace",pod=~"$pod_name",image=""})
	handler := make(map[string]*v1alpha1.Prometheus_Source_Handler)
	h := r.env.Config.Metric.Prometheus.Handlers
	if h == nil {
		log.Infof("query handler is nil")
		return queryMap
	}
	handler = h

	pods, err := queryServicePods(r.env.K8SClient,loc)
	if err != nil {
		log.Infof("%+v",err.Error())
		return queryMap
	}
	subsetsPods, err := querySubsetPods(pods,loc)
	if err != nil {
		log.Infof("%+v",err.Error())
		return queryMap
	}
	return generateQueryString(subsetsPods,loc,handler)
}

// QueryServicePods query pods related to service
func queryServicePods(c *kubernetes.Clientset,loc types.NamespacedName) ([]v1.Pod,error) {

	var err error
	var service *v1.Service
	pods := make([]v1.Pod, 0)

	service, err = c.CoreV1().Services(loc.Namespace).Get(loc.Name, metav1.GetOptions{})
	if err != nil {
		return pods,fmt.Errorf("query service %+v faild, %s", loc, err.Error())
	}

	ps, err := c.CoreV1().Pods(loc.Namespace).List(metav1.ListOptions{
		LabelSelector: labels.SelectorFromSet(service.Spec.Selector).String(),
	})
	if err != nil {
		return pods,fmt.Errorf("query pod list faild, %+v", err.Error())
	}

	log.Debugf("get pods %+v", ps)
	for _, item := range ps.Items {
		if item.DeletionTimestamp != nil {
			// pod is deleted
			continue
		}
		pods = append(pods, item)
	}
	return pods,nil
}

// QuerySubsetPods  query pods related to subset
func querySubsetPods(pods []v1.Pod,loc types.NamespacedName) (map[string][]string,error) {

	subsetsPods := make(map[string][]string)
	host := util.UnityHost(loc.Name, loc.Namespace)

	for _, pod := range pods {
		if controllers.HostSubsetMapping.Get(host) != nil {
			sbs, ok := controllers.HostSubsetMapping.Get(host).([]*v1alpha3.Subset)
			if ok {
				for _, sb := range sbs {
					if util.IsContain(pod.Labels, sb.Labels) {
						if subsetsPods[sb.Name] != nil {
							subsetsPods[sb.Name] = append(subsetsPods[sb.Name], pod.Name)
						} else {
							subsetsPods[sb.Name] = []string{pod.Name}
						}
					}
				}
			}
		}

		if subsetsPods[util.Wellkonw_BaseSet] != nil {
			subsetsPods[util.Wellkonw_BaseSet] = append(subsetsPods[util.Wellkonw_BaseSet], pod.Name)
		} else {
			subsetsPods[util.Wellkonw_BaseSet] = []string{pod.Name}
		}
	}
	return subsetsPods,nil
}

// GenerateQueryString
func generateQueryString(subsetsPods map[string][]string, loc types.NamespacedName,  handler map[string]*v1alpha1.Prometheus_Source_Handler) map[string][]metric.Handler {

	queryMap := make(map[string][]metric.Handler,0)
	handlers := make([]metric.Handler,0)
	isGroup := make(map[string]bool)

	meta := generateMeta(subsetsPods,loc)

	for customMetricName,h := range handler {
		if h.Query == "" {
			log.Infof("query is emtpy,skip")
			continue
		}
		handlers, isGroup = replaceQueryString(customMetricName,h.Query,h.Type,loc,subsetsPods)
		for name, group := range isGroup {
			meta.IsGroup[name] = group
		}
	}
	metaInfo := meta.String()
	if metaInfo == "" {
		return queryMap
	}
	queryMap[metaInfo] = append(queryMap[metaInfo],handlers...)

	return queryMap
}

func generateMeta(subsetsPods map[string][]string, loc types.NamespacedName) StaticMeta {

	NPod := make(map[string]int)
	for k,v := range subsetsPods {
		if len(v) > 0 {
			NPod[k+".pod"] = len(v)
		}
	}
	meta := StaticMeta{
		Name:         loc.Name,
		Namespace:    loc.Namespace,
		NPod: NPod,
		IsGroup: map[string]bool{},
	}
	return meta
}


func replaceQueryString(metricName string, query string,typ v1alpha1.Prometheus_Source_Type ,loc types.NamespacedName,subsetsPods map[string][]string ) ([]metric.Handler,map[string]bool) {

	handlers := make([]metric.Handler,0)
	isGroup := make(map[string]bool)
	query = strings.ReplaceAll(query, "$namespace", loc.Namespace)

	switch typ {
	case v1alpha1.Prometheus_Source_Value:
		if strings.Contains(query, "$pod_name") {
			for subsetName, subsetPods := range subsetsPods {
				subQuery := strings.ReplaceAll(query, "$pod_name", strings.Join(subsetPods, "|"))
				h := metric.Handler{
					Name:  subsetName+"."+metricName,
					Query: subQuery,
				}
				handlers = append(handlers,h)
				isGroup[h.Name] = false
			}
		} else {
			h := metric.Handler{
				Name:  metricName,
				Query: query,
			}
			handlers = append(handlers,h)
			isGroup[h.Name] = false
		}
	case v1alpha1.Prometheus_Source_Group:
		h := metric.Handler{
			Name:  metricName,
			Query: query,
		}
		handlers = append(handlers,h)
		isGroup[h.Name] = true
	}
	return handlers,isGroup
}


func newPrometheusSourceConfig(env bootstrap.Environment) (metric.PrometheusSourceConfig, error) {

	ps := env.Config.Metric.Prometheus
	if ps == nil {
		return metric.PrometheusSourceConfig{}, stderrors.New("failure create prometheus client, empty prometheus config")
	}
	promClient, err := prometheusApi.NewClient(prometheusApi.Config{
		Address:      ps.Address,
		RoundTripper: nil,
	})
	if err != nil {
		return metric.PrometheusSourceConfig{}, err
	}

	return metric.PrometheusSourceConfig{
		Api: prometheusV1.NewAPI(promClient),
	}, nil
}