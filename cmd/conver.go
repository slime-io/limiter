package main

import (
	"flag"
	"fmt"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer/yaml"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
	"k8s.io/client-go/util/retry"
	"log"
	"path/filepath"
	microserviceslimeiov1alpha1 "slime.io/slime/modules/limiter/api/v1alpha1"
)
// 自定义数据
const metaCRD = `
apiVersion: microservice.slime.io/v1alpha1
kind: SmartLimiter
metadata:
  name: productpage
  namespace: temp
spec:
  sets:
    _base:
      descriptor:
      - action:
          fill_interval:
            seconds: 3600
          quota: "10"
          strategy: "global"
        condition: "true"
        target:
          port: 9080      
      - action:
          fill_interval:
            seconds: 60
          quota: "5"
          strategy: "global"
        condition: "true"
        target:
          port: 9080
`


func GetK8sConfig() (config *rest.Config, err error) {
	// 获取k8s rest config
	var kubeconfig *string
	if home := homedir.HomeDir(); home != "" {
		kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else {
		kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	}
	flag.Parse()

	config, err = clientcmd.BuildConfigFromFlags("", *kubeconfig)
	if err != nil {
		panic(err)
	}
	return
}

func GetGVRdyClient(gvk *schema.GroupVersionKind,namespace string) (dr dynamic.ResourceInterface,err error)  {

	config,err := GetK8sConfig()
	if err != nil {
		return
	}

	// 创建discovery客户端
	discoveryClient,err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		return
	}

	// 获取GVK GVR 映射
	mapperGVRGVK := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(discoveryClient))

	// 根据资源GVK 获取资源的GVR GVK映射
	resourceMapper,err := mapperGVRGVK.RESTMapping(gvk.GroupKind(),gvk.Version)
	if err != nil {
		return
	}

	// 创建动态客户端
	dynamicClient,err := dynamic.NewForConfig(config)
	if err != nil {
		return
	}

	if resourceMapper.Scope.Name() == meta.RESTScopeNameNamespace {
		// 获取gvr对应的动态客户端
		dr = dynamicClient.Resource(resourceMapper.Resource).Namespace(namespace)
	} else {
		// 获取gvr对应的动态客户端
		dr = dynamicClient.Resource(resourceMapper.Resource)
	}

	return
}
func main6()  {

	var (
		err error
		objGET *unstructured.Unstructured
		objCreate *unstructured.Unstructured
		objUpdate *unstructured.Unstructured
		gvk *schema.GroupVersionKind
		dr dynamic.ResourceInterface
	)
	obj := &unstructured.Unstructured{}
	_, gvk, err = yaml.NewDecodingSerializer(unstructured.UnstructuredJSONScheme).Decode([]byte(metaCRD), nil, obj)
	if err != nil {
		panic(fmt.Errorf("failed to get GVK: %v", err))
	}

	dr,err = GetGVRdyClient(gvk,obj.GetNamespace())
	if err != nil {
		panic(fmt.Errorf("failed to get dr: %v", err))
	}

	//创建
	objCreate,err = dr.Create(obj,metav1.CreateOptions{})
	if err != nil {
		//panic(fmt.Errorf("Create resource ERROR: %v", err))
		log.Print(err)
	}
	log.Print("Create: : ",objCreate)

	// 查询
	objGET,err = dr.Get(obj.GetName(),metav1.GetOptions{})
	if err != nil {
		panic(fmt.Errorf("select resource ERROR: %v", err))
	}
	log.Print("GET: ",objGET)

	//更新
	retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() (err error) {
		// 查询resource是否存在
		result, getErr := dr.Get(obj.GetName(),metav1.GetOptions{})
		if getErr != nil {
			panic(fmt.Errorf("failed to get latest version of : %v", getErr))
		}


		// 提取obj 的 spec 期望值
		spec, found, err := unstructured.NestedMap(obj.Object, "spec")
		if err != nil || !found || spec == nil {
			panic(fmt.Errorf(" not found or error in spec: %v", err))
		}
		// 更新 存在资源的spec
		if err := unstructured.SetNestedMap(result.Object, spec, "spec", ); err != nil {
			panic(err)
		}
		// 更新资源
		objUpdate, err = dr.Update(result,metav1.UpdateOptions{})
		log.Print("update : ",objUpdate)
		return err
	})
	if retryErr != nil {
		panic(fmt.Errorf("update failed: %v", retryErr))
	} else {
		log.Print("更新成功")
	}


	// 删除
	//err = dr.Delete(obj.GetName(),&metav1.DeleteOptions{})
	//if err != nil {
	//	panic(fmt.Errorf("delete resource ERROR : %v", err))
	//} else {
	//	log.Print("删除成功")
	//}


}


func main() {
	var kubeconfig *string
	// home是家目录，如果能取得家目录的值，就可以用来做默认值
	if home:=homedir.HomeDir(); home != "" {
		// 如果输入了kubeconfig参数，该参数的值就是kubeconfig文件的绝对路径，
		// 如果没有输入kubeconfig参数，就用默认路径~/.kube/config
		kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else {
		// 如果取不到当前用户的家目录，就没办法设置kubeconfig的默认目录了，只能从入参中取
		kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	}
	flag.Parse()

	// 从本机加载kubeconfig配置文件，因此第一个参数为空字符串
	config, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)

	// kubeconfig加载失败就直接退出了
	if err != nil {
		panic(err.Error())
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}

	// dynamicClient的唯一关联方法所需的入参
	gvr := schema.GroupVersionResource{Version: "v1alpha1", Resource: "smartlimiters",Group: "microservice.slime.io"}

	// 使用dynamicClient的查询列表方法，查询指定namespace下的所有pod，
	// 注意此方法返回的数据结构类型是UnstructuredList
	unstructObj, err := dynamicClient.
		Resource(gvr).
		Namespace("temp").
		List(metav1.ListOptions{Limit: 100})
	if err != nil {
		panic(err.Error())
	}
	// 实例化一个PodList数据结构，用于接收从unstructObj转换后的结果
	podList := &microserviceslimeiov1alpha1.SmartLimiterList{}
	// 转换
	err = runtime.DefaultUnstructuredConverter.FromUnstructured(unstructObj.UnstructuredContent(), podList)
	if err != nil {
		panic(err.Error())
	}

	convert()
}


func convert() {

}


