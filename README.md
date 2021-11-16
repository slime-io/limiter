# 自适应限流概述

## 特点

1. 方便使用，只需提交`SmartLimiter`资源即可达到服务限流的目的。
2. 自适应限流，根据`pod`的资源使用量动态的触发限流规则。
3. 覆盖场景多，支持全局共享限流，全局均分限流，单机限流。

## 背景

一方面，随着`Mixer`的移除，用户不得不面对异常复杂的`EnvoyFilter`配置，另一方面固定的限流策略不够灵活，如用户只想在服务的`CPU`使用量达到某个值后才开启限流。为了解决这两个痛点，我们推出了自适应限流组件`slime/limiter`。用户只需要提交符合我们定义的`SmartLimiter`，即可完成灵活的服务限流要求。

## 思路

自适应限流的首要目标是为了让用户从繁琐的`EnvoyFilter`配置中脱离出来，所以我们利用`kubernetes`的`CRD`机制，我们定义了一套简便的`API`，即`kubernetes`内的`SmartLimiter`资源。用户只需要按照`SmartLimiter`的规范提交一个`CR`。

自适应限流的另一个目标是让限流策略足够的灵活，比如当服务的`CPU`使用量达到某个值后才开启限流，为此我们引入了

自适应限流组件会将结合`metric`和用户提交的内容生成一份`EnvoyFilter`,并将该`EnvoyFilter`提交到`istio`，`istio`按照`EnvoyFilter`要求将其下发给`envoy`。为了丰富实用场景，`SmartLimiter`支持三种限流策略全局共享限流`global`，均分限流 `average`和单机限流`single`。

`SmartLimiter`的CRD定义的比较接近自然语义，例如，希望当`reviews`服务的`v1`版本的服务消耗`cpu`总量大于10的时候，触发限流，让其每个`POD`的9080端口的服务每秒钟只能处理10次请求。

~~~yaml
apiVersion: microservice.slime.io/v1alpha1
kind: SmartLimiter
metadata:
  name: review
  namespace: default
spec:
  sets:
    v1:
      descriptor:
      - action:
          fill_interval:
            seconds: 1
          quota: "10"
          strategy: "single"
        condition: "{{.v1.cpu.sum}}>10"
        target:
          port: 9080
~~~

## 架构

自适应限流的主体架构分为两个部分，一部分包括`SmartLimiter`到`EnvoyFilter`的逻辑转化，另一部分包括集群内监控数据的获取，包括服务的`CPU`, `Memory`,`POD`数量等数据。

<img src="./media/SmartLimiter.png" style="zoom:80%;" />