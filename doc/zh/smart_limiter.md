
### 自适应限流

#### 安装和使用

请先按照安装slime-boot小节的指引安装`slime-boot`  。

使用Slime的自适应限流功能需打开Limiter模块：

```yaml
apiVersion: config.netease.com/v1alpha1
kind: SlimeBoot
metadata:
  name: smartlimiter
  namespace: mesh-operator
spec:
  image:
    pullPolicy: Always
    repository: docker.io/slimeio/slime-limiter
    tag: {{your_limiter_tag}}
  module:
    - limiter:
        enable: true
        backend: 1
      metric:
        prometheus:
          address: {{prometheus_address}} # replace to your prometheus address
          handlers:
            cpu.sum:
              query: |
                sum(container_cpu_usage_seconds_total{namespace="$namespace",pod=~"$pod_name",image=""})
            cpu.max:
              query: |
                max(container_cpu_usage_seconds_total{namespace="$namespace",pod=~"$pod_name",image=""})
            rt99:
              query: |
                histogram_quantile(0.99, sum(rate(istio_request_duration_milliseconds_bucket{kubernetes_pod_name=~"$pod_name"}[2m]))by(le))
        k8s:
          handlers:
            - pod # inline
      name: limiter
```

[完整样例](../../install/samples/smartlimiter/slimeboot_smartlimiter.yaml)

在示例中，我们配置了prometheus作为监控源，prometheus.handlers定义了希望从监控中获取的监控指标，这些监控指标可以作为治理规则中的参数，从而达到自适应限流的目的。
用户也可以根据需要定义limiter模块需要获取的监控指标，以下是一些可以常用的监控指标获取语句：

```
cpu:
总和：
sum(container_cpu_usage_seconds_total{namespace="$namespace",pod=~"$pod_name",image=""})
最大值：
max(container_cpu_usage_seconds_total{namespace="$namespace",pod=~"$pod_name",image=""})
limit:
container_spec_cpu_quota{pod=~"$pod_name"}

内存：
总和：
sum(container_memory_usage_bytes{namespace="$namespace",pod=~"$pod_name",image=""})
最大值：
max(container_memory_usage_bytes{namespace="$namespace",pod=~"$pod_name",image=""})
limit:
sum(container_spec_memory_limit_bytes{pod=~"$pod_name"})

请求时延：
90值：
histogram_quantile(0.90, sum(rate(istio_request_duration_milliseconds_bucket{kubernetes_pod_name=~"$pod_name"}[2m]))by(le))
95值：
histogram_quantile(0.95, sum(rate(istio_request_duration_milliseconds_bucket{kubernetes_pod_name=~"$pod_name"}[2m]))by(le))
99值：
histogram_quantile(0.99, sum(rate(istio_request_duration_milliseconds_bucket{kubernetes_pod_name=~"$pod_name"}[2m]))by(le))
```



#### 概念以及原理

从限流的生效范围可以分为服务限流和分组限流。

##### 服务限流

对整个svc所代表的服务进行限流。

~~~yaml
apiVersion: microservice.slime.io/v1alpha1
kind: SmartLimiter
metadata:
  name: reviews
  namespace: default
spec:
  sets:
    _base:  ## 由关键字_base表示，对reviews 这个svc的所有pod进行限流
      descriptor:
~~~

##### 分组限流

与服务限流对应的是分组限流对这个svc所代表的服务中某个版本进行限流，对应着istio中的subset

~~~yaml
apiVersion: microservice.slime.io/v1alpha1
kind: SmartLimiter
metadata:
  name: reviews
  namespace: default
spec:
  sets:
    v1:  ## 对reviews 这个svc的v1分组进行限流,前提是destinationrule设置了该分组
      descriptor:
~~~



从限流的策略来看，可以分为单机限流，均分限流，全局共享限流。

##### 单机限流

每个pod限制一定个数的请求，限制的数目由自身记录。

在action中指定strategy为"single"，limiter controller 监听到该资源后，会计算condition是否为true, 如果为true那么会为该SmartLimiter生成envoyfilter资源，该envoyfilter资源告知envoy, 利用envoy.filters.http.local_ratelimit插件配合ratelimite bucket进行限流，具体参数由envoyfilter指定。

~~~yaml
#每个pod 10/min
apiVersion: microservice.slime.io/v1alpha1
kind: SmartLimiter
metadata:
  name: reviews
  namespace: default
spec:
  sets:
    _base:
      descriptor:
      - action:
          fill_interval:
            seconds: 60  #时间
          quota: "10"  #限流的具体个数
          strategy: "single"  #单机限流，代表每个pod
        condition: "true"  #这里同样可以写成 "{{._base.cpu.sum}}>10" 代表对服务的自适应限流
        target:
          port: 9080
~~~

##### 均分限流

假定负载均衡理想的情况下，实例限流数=服务限流总数/实例个数。reviews的服务限流数为3，那么可以将quota字段配置为3/{{._base.pod}}以实现服务级别的限流。即每个pod进行quota为1/min的限流，服务共计3/min的限流。其实均分限流实际也是一种单机限流，在limiter组件进行了均分计算。

在action中指定strategy为"average"，limiter controller 监听到该资源后，会计算condition是否为true, 如果为true,那么会利用metric信息计算真实的quota,之后生成envoyfilter资源，之后同单机限流一样。

~~~yaml
#每个pod 1/min，整个服务共计3/min（如果pod=3）
apiVersion: microservice.slime.io/v1alpha1
kind: SmartLimiter
metadata:
  name: reviews
  namespace: default
spec:
  sets:
    _base:
      descriptor:
      - action:
          fill_interval:
            seconds: 60 
          quota: "3/{{_base.pod}}" 
          strategy: "average"
        condition: "true"
        target:
          port: 9080
~~~

##### 全局共享限流

全局共享限流指的是每一个pod的访问次数都会在全局计数器中累加，一旦超过设定阈值，就会对所有pod进行限流。

共享限流依赖着全局计数器，本服务中利用的是 rls, 该服务依赖着redis做全局计数。所以使用该功能前，需要部署rls以及 reids服务。当配置下发至envoy后，当一个请求访问设置限流的服务时，该服务的envoy会请求rate limit service，让其决定是否进行限流。而在单机和均分限流中，当一个请求访问设置限流的服务时，该服务只会通过自身的ratelimit bucket进行计算。

~~~yaml
#所有相关pod 共计 10/min ，计数器的服务地址由rateLimitService指定
apiVersion: microservice.slime.io/v1alpha1
kind: SmartLimiter
metadata:
  name: reviews
  namespace: default
spec:
  sets:
    _base:
      descriptor:
      - action:
          fill_interval:
            seconds: 60  #时间
          quota: "10"  #限流的具体个数
          strategy: "global"  #单机限流，代表每个pod
          rateLimitService: "outbound|18081||rate-limit.istio-system.svc.cluster.local"
        condition: "true"
        target:
          port: 9080
~~~

#### 原理

#### 单机限流
















#### 示例：为bookinfo的reviews服务开启自适应限流

##### 安装 istio (1.8+)



##### 设定tag

$latest_tag获取最新tag。默认执行的shell脚本和yaml文件均是$latest_tag版本。

```sh
$ export latest_tag=$(curl -s https://api.github.com/repos/slime-io/slime/tags | grep 'name' | cut -d\" -f4 | head -1)
```



##### 安装 slime 

```shell
$ /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/slime-io/slime/$latest_tag/install/samples/smartlimiter/easy_install_limiter.sh)"
```

确认所有组件已正常运行

```sh
$ kubectl get slimeboot -n mesh-operator
NAME           AGE
smartlimiter   6s
$ kubectl get pod -n mesh-operator
NAME                                    READY   STATUS    RESTARTS   AGE
limiter-6cb886d74-82hxg                 1/1     Running   0          26s
slime-boot-5977685db8-lnltl             1/1     Running   0          6m22s
```



##### 安装bookinfo

   创建前请将current-context中namespace切换到你想部署bookinfo的namespace，使bookinfo创建在其中。此处以default为例。

```sh
$ kubectl label namespace default istio-injection=enabled
$ kubectl apply -f "https://raw.githubusercontent.com/slime-io/slime/$latest_tag/install/config/bookinfo.yaml"
```

此样例中可以在pod/ratings中发起对productpage的访问，`curl productpage:9080/productpage`。

另外也可参考 [对外开放应用程序](https://istio.io/latest/zh/docs/setup/getting-started/#ip) 给应用暴露外访接口。



为reviews创建DestinationRule

```sh
$ kubectl apply -f "https://raw.githubusercontent.com/slime-io/slime/$latest_tag/install/config/reviews-destination-rule.yaml"
```



##### 为reviews设置限流规则

```sh
$ kubectl apply -f "https://raw.githubusercontent.com/slime-io/slime/$latest_tag/install/samples/smartlimiter/smartlimiter_reviews.yaml"
```



##### 确认smartlimiter已经创建

```sh
$ kubectl get smartlimiter reviews -oyaml
apiVersion: microservice.slime.io/v1alpha1
kind: SmartLimiter
metadata:
  annotations:
    kubectl.kubernetes.io/last-applied-configuration: |
      {"apiVersion":"microservice.slime.io/v1alpha1","kind":"SmartLimiter","metadata":{"annotations":{},"name":"reviews","namespace":"default"},"spec":{"sets":{"v2":{"descriptor":[{"action":{"fill_interval":{"seconds":60},"quota":"1"},"condition":"{{.v2.cpu.sum}}\u003e100"}]}}}}
  creationTimestamp: "2021-08-09T07:54:24Z"
  generation: 1
  name: reviews
  namespace: default
  resourceVersion: "63006"
  uid: e6bf5121-6126-40db-8f5c-e124894b5d54
spec:
  sets:
    v2:
      descriptor:
      - action:
          fill_interval:
            seconds: 60
          quota: "1"
        condition: '{{.v2.cpu.sum}}>100'
status:
  metricStatus:
    _base.cpu.max: "181.591794594"
    _base.cpu.sum: "536.226646691"
    _base.pod: "3"
    v1.cpu.max: "176.285405855"
    v1.cpu.sum: "176.285405855"
    v1.pod: "1"
    v2.cpu.max: "178.349446242"
    v2.cpu.sum: "178.349446242"
    v2.pod: "1"
    v3.cpu.max: "181.591794594"
    v3.cpu.sum: "181.591794594"
    v3.pod: "1"
  ratelimitStatus:
    v2:
      descriptor:
      - action:
          fill_interval:
            seconds: 60
          quota: "1"
```

该配置表明review-v2服务会被限制为60s访问1次。



##### 确认EnvoyFilter已创建

```sh
$ kubectl get envoyfilter reviews.default.v2.ratelimit -oyaml
apiVersion: networking.istio.io/v1alpha3
kind: EnvoyFilter
metadata:
  creationTimestamp: "2021-08-09T07:54:25Z"
  generation: 1
  name: reviews.default.v2.ratelimit
  namespace: default
  ownerReferences:
  - apiVersion: microservice.slime.io/v1alpha1
    blockOwnerDeletion: true
    controller: true
    kind: SmartLimiter
    name: reviews
    uid: e6bf5121-6126-40db-8f5c-e124894b5d54
  resourceVersion: "62926"
  uid: cbea7344-3fda-4a1d-b3d2-93649ce76129
spec:
  configPatches:
  - applyTo: HTTP_FILTER
    match:
      context: SIDECAR_INBOUND
      listener:
        filterChain:
          filter:
            name: envoy.http_connection_manager
            subFilter:
              name: envoy.router
    patch:
      operation: INSERT_BEFORE
      value:
        name: envoy.filters.http.local_ratelimit
        typed_config:
          '@type': type.googleapis.com/udpa.type.v1.TypedStruct
          type_url: type.googleapis.com/envoy.extensions.filters.http.local_ratelimit.v3.LocalRateLimit
          value:
            filter_enabled:
              default_value:
                numerator: 100
              runtime_key: local_rate_limit_enabled
            filter_enforced:
              default_value:
                numerator: 100
              runtime_key: local_rate_limit_enforced
            stat_prefix: http_local_rate_limiter
            token_bucket:
              fill_interval:
                seconds: "60"
              max_tokens: 1
  workloadSelector:
    labels:
      app: reviews
      version: v2
```


##### **访问观察**

触发限流时，访问productpage，productpage的sidecar观察日志如下

```
[2021-08-09T08:01:04.872Z] "GET /reviews/0 HTTP/1.1" 429 - via_upstream - "-" 0 18 1 1 "-" "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/92.0.4515.107 Safari/537.36" "19605264-5868-9c90-8b42-424790fad1b2" "reviews:9080" "172.17.0.11:9080" outbound|9080||reviews.default.svc.cluster.local 172.17.0.16:41342 10.100.112.212:9080 172.17.0.16:41378 - default
```

reviews-v2的sidecar观察日志如下

```
[2021-08-09T08:01:04.049Z] "GET /reviews/0 HTTP/1.1" 429 - local_rate_limited - "-" 0 18 0 - "-" "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/92.0.4515.107 Safari/537.36" "3f0a65c8-1c66-9994-a0cf-1d7ae0446371" "reviews:9080" "-" inbound|9080|| - 172.17.0.11:9080 172.17.0.16:41342 outbound_.9080_._.reviews.default.svc.cluster.local -
```

触发限流-“local_rate_limited”，返回429。



##### **卸载**

卸载bookinfo

```sh
$ kubectl delete -f "https://raw.githubusercontent.com/slime-io/slime/$latest_tag/install/config/bookinfo.yaml"
$ kubectl delete -f "https://raw.githubusercontent.com/slime-io/slime/$latest_tag/install/config/reviews-destination-rule.yaml"
```

卸载slime相关

```sh
$ /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/slime-io/slime/$latest_tag/install/samples/smartlimiter/easy_uninstall_limiter.sh)"
```



##### 补充说明

如想要使用其他tag或commit_id的shell脚本和yaml文件，请显示指定$custom_tag_or_commit。

```sh
$ export custom_tag_or_commit=xxx
```

执行的命令涉及到yaml文件，用$custom_tag_or_commit替换$latest_tag，如下

```sh
#$ kubectl apply -f "https://raw.githubusercontent.com/slime-io/slime/$latest_tag/install/config/bookinfo.yaml"
$ kubectl apply -f "https://raw.githubusercontent.com/slime-io/slime/$custom_tag_or_commit/install/config/bookinfo.yaml"
```

执行的命令涉及到shell文件，将$custom_tag_or_commit作为shell文件的参数，如下

```sh
#$ /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/slime-io/slime/$latest_tag/install/samples/smartlimiter/easy_install_limiter.sh)"
$ /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/slime-io/slime/$latest_tag/install/samples/smartlimiter/easy_install_limiter.sh)" $custom_tag_or_commit
```





------





#### 基于监控的自适应限流

在示例的slimeboot中，我们获取了服务容器的cpu总和以及最大值作为limiter模块所关心的监控指标。从prometheus获取的监控数据会被显示在metricStatus中，我们可以采用这些指标作为触发限流的条件，如下所示：

部署

```yaml
apiVersion: microservice.slime.io/v1alpha1
kind: SmartLimiter
metadata:
  name: reviews
  namespace: default
spec:
  sets:
    v2:
      descriptor:
      - action:
          fill_interval:
            seconds: 60
          quota: "1"
          strategy: "single"
        condition: '{{.v2.cpu.sum}}>10'
        target:
          port: 9080
```

得到

```yaml
apiVersion: microservice.slime.io/v1alpha1
kind: SmartLimiter
metadata:
  annotations:
    kubectl.kubernetes.io/last-applied-configuration: |
      {"apiVersion":"microservice.slime.io/v1alpha1","kind":"SmartLimiter","metadata":{"annotations":{},"name":"reviews","namespace":"default"},"spec":{"sets":{"v2":{"descriptor":[{"action":{"fill_interval":{"seconds":60},"quota":"1"},"condition":"{{.v2.cpu.sum}}\u003e100"}]}}}}
  creationTimestamp: "2021-08-09T07:54:24Z"
  generation: 1
  name: reviews
  namespace: default
  resourceVersion: "63006"
  uid: e6bf5121-6126-40db-8f5c-e124894b5d54
spec:
  sets:
    v2:
      descriptor:
      - action:
          fill_interval:
            seconds: 60
          quota: "1"
        condition: '{{.v2.cpu.sum}}>100'
status:
  metricStatus:
    _base.cpu.max: "181.591794594"
    _base.cpu.sum: "536.226646691"
    _base.pod: "3"
    v1.cpu.max: "176.285405855"
    v1.cpu.sum: "176.285405855"
    v1.pod: "1"
    v2.cpu.max: "178.349446242"
    v2.cpu.sum: "178.349446242"
    v2.pod: "1"
    v3.cpu.max: "181.591794594"
    v3.cpu.sum: "181.591794594"
    v3.pod: "1"
  ratelimitStatus:
    v2:
      descriptor:
      - action:
          fill_interval:
            seconds: 60
          quota: "1"
```

condition中的算式会根据metricStatus的条目进行渲染，渲染后的算式若计算结果为true，则会触发限流。



#### 分组限流

在istio的体系中，用户可以通过DestinationRule为服务定义subset，并为其定制负载均衡，连接池等服务治理规则。限流同样属于此类服务治理规则，通过slime框架，我们不仅可以为服务，也可以为subset定制限流规则。如下所示：

```yaml
apiVersion: microservice.slime.io/v1alpha1
kind: SmartLimiter
metadata:
  name: reviews
  namespace: default
spec:
  sets:
    v1: # reviews v1
      descriptor:
      - action:
          fill_interval:
            seconds: 60
          quota: "1"
        condition: "true"
```

上述配置为reviews服务的v1版本限制了每分钟1次的请求。将配置提交之后，该服务下实例的状态信息以及限流信息会显示在`status`中，如下：

```yaml
apiVersion: microservice.slime.io/v1alpha1
kind: SmartLimiter
metadata:
  annotations:
    kubectl.kubernetes.io/last-applied-configuration: |
      {"apiVersion":"microservice.slime.io/v1alpha1","kind":"SmartLimiter","metadata":{"annotations":{},"name":"reviews","namespace":"default"},"spec":{"sets":{"v1":{"descriptor":[{"action":{"fill_interval":{"seconds":60},"quota":"1"},"condition":"true"}]}}}}
  creationTimestamp: "2021-08-09T07:06:42Z"
  generation: 1
  name: reviews
  namespace: default
  resourceVersion: "59635"
  uid: ba62ff40-3c9f-427d-959b-6a416d54f24c
spec:
  sets:
    v1: # reviews v1
      descriptor:
      - action:
          fill_interval:
            seconds: 60
          quota: "1"
        condition: "true"
status:
  metricStatus:
    _base.cpu.max: "158.942939832"
    _base.cpu.sum: "469.688066909"
    _base.pod: "3"
    v1.cpu.max: "154.605786157"
    v1.cpu.sum: "154.605786157"
    v1.pod: "1"
    v2.cpu.max: "156.13934092"
    v2.cpu.sum: "156.13934092"
    v2.pod: "1"
    v3.cpu.max: "158.942939832"
    v3.cpu.sum: "158.942939832"
    v3.pod: "1"
  ratelimitStatus:
    v1:
      descriptor:
      - action:
          fill_interval:
            seconds: 60
          quota: "1"
```


#### 