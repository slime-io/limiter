package main

import (
	"flag"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
	"path/filepath"
	microserviceslimeiov1alpha1 "slime.io/slime/modules/limiter/api/v1alpha1"
	envoypluginsv1 "slime.io/slime/modules/limiter/oldversion/envoyplugins"
	v1alpha1 "slime.io/slime/modules/limiter/oldversion/v1alpha1"
)

var dynamicClient dynamic.Interface

func init() {
	var kubeconfig *string

	if home:=homedir.HomeDir(); home != "" {
		kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else {
		kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	}
	flag.Parse()

	config, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
	if err != nil {
		panic(err.Error())
	}
	dynamicClient,err= dynamic.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}
}

func main() {

	gvr := schema.GroupVersionResource{Version: "v1alpha1", Resource: "smartlimiters",Group: "microservice.netease.com"}
	namespaces := listAllNameSpace()

	oldVersionLimits := make(map[string]*v1alpha1.SmartLimiterList)
	for _, ns := range namespaces {
		unstructObj, err := dynamicClient.Resource(gvr).Namespace(ns).List(metav1.ListOptions{})
		if err != nil {
			panic(err.Error())
		}
		limiterList := &v1alpha1.SmartLimiterList{}
		err = runtime.DefaultUnstructuredConverter.FromUnstructured(unstructObj.UnstructuredContent(), limiterList)
		if err != nil {
			panic(err.Error())
		}
		oldVersionLimits[ns] = limiterList
	}

	convert2new(oldVersionLimits)
}


func getEnvoyplugins (ns2oldlist map[string]*v1alpha1.SmartLimiterList)map[*v1alpha1.SmartLimiterList]*envoypluginsv1.EnvoyPlugin {

	limiter2plugins := make(map[*v1alpha1.SmartLimiterList]*envoypluginsv1.EnvoyPlugin)
	for _,oldlist := range ns2oldlist {
		for _,oldlimiter := range oldlist.Items {

			name := oldlimiter.Name
			namespace := oldlimiter.Namespace
			envoyplugins := &envoypluginsv1.EnvoyPlugin{}

			gvr := schema.GroupVersionResource{Version: "v1alpha1", Resource: "envoyplugins",Group: "microservice.slime.io/v1alpha1"}
			unstructObj, err := dynamicClient.Resource(gvr).Namespace(namespace).Get(name,metav1.GetOptions{},"")
			if err != nil {
				panic(err.Error())
			}
			err = runtime.DefaultUnstructuredConverter.FromUnstructured(unstructObj.UnstructuredContent(), envoyplugins)
			if err != nil {
				panic(err.Error())
			}
			limiter2plugins[oldlist] = envoyplugins
		}
	}
	return limiter2plugins
}


func convert2new(ns2oldlist map[string]*v1alpha1.SmartLimiterList) {

	for _,oldlist := range ns2oldlist {
		for _, oldlimiter := range oldlist.Items {
			descriptors := oldlimiter.Spec.RatelimitConfig.RateLimitConf.Descriptors
			newlimiter := &microserviceslimeiov1alpha1.SmartLimiter{}

			sds := make([]*microserviceslimeiov1alpha1.SmartLimitDescriptor,0)
			for _,desc := range descriptors {
				quota, second , strategy := handleThenUnit(desc.Then,int32(desc.Unit))
				action := &microserviceslimeiov1alpha1.SmartLimitDescriptor_Action{
					Quota:                quota ,
					FillInterval:         &microserviceslimeiov1alpha1.Duration{Seconds: second} ,
					Strategy:             strategy,
				}
				sd := &microserviceslimeiov1alpha1.SmartLimitDescriptor{
					Condition:            "true",
					Action:               action,
					Target: 		getTarget(),
				}
				sds = append(sds,sd)
			}
			newlimiter.Kind = "SmartLimiter"
			newlimiter.APIVersion = "microservice.slime.io/v1alpha1"
			newlimiter.Name = oldlimiter.Name+"."+oldlimiter.Namespace
			newlimiter.Namespace = oldlimiter.Namespace
			newlimiter.Spec.Sets = make(map[string]*microserviceslimeiov1alpha1.SmartLimitDescriptors)
			newlimiter.Spec.Sets["_base"]=&microserviceslimeiov1alpha1.SmartLimitDescriptors{Descriptor_:sds}
			gvr := schema.GroupVersionResource{Version: "v1alpha1", Resource: "smartlimiters",Group: "microservice.slime.io"}
			obj,err := runtime.DefaultUnstructuredConverter.ToUnstructured(newlimiter)
			if err != nil {
				panic(err)
			}
			_, err = dynamicClient.Resource(gvr).Create(&unstructured.Unstructured{Object: obj},metav1.CreateOptions{},"")
			if err != nil {
				panic(err)
			}
		}
	}

}

func getTarget() *microserviceslimeiov1alpha1.SmartLimitDescriptor_Target {
	return &microserviceslimeiov1alpha1.SmartLimitDescriptor_Target{Port:9080}
}


func handleThenUnit(then string,unit int32) (string,int64,string){
	return then,unit2Seconds(unit),"average"
}

func listAllNameSpace() []string{
	return []string{"temp"}
}

func unit2Seconds(unit int32) int64 {

	switch unit {
	case 1:
		return 1
	case 2:
		return 60
	case 3:
		return 3600
	case 4:
		return 3600*6
	}
	return 1
}
