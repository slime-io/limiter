- [自适应限流概述](#自适应限流概述)
  - [特点](#特点)
  - [背景](#背景)
  - [思路](#思路)
  - [架构](#架构)
  - [功能介绍](#功能介绍)
  - [安装和使用](#安装和使用)
  - [示例](#示例)
  - [E2E测试](#e2e测试)
# 自适应限流概述

[EN](./README.md)

## 特点

1. 使用方便，只需提交`SmartLimiter`资源即可达到服务限流的目的。
2. 自适应限流，根据服务的资源使用量等指标动态的触发限流规则。

## 背景

一方面，随着`Mixer`的移除，用户不得不面对异常复杂的`EnvoyFilter`配置，另一方面固定的限流策略不够灵活，普通限流无法覆盖服务`CPU`使用量达到阈值后开启限流等场景。为了解决这两个痛点，我们推出了自适应限流组件`slime/limiter`。用户只需提交符合我们定义的`SmartLimiter`，即可完成灵活的服务限流要求。

## 思路

自适应限流的首要目标是为了让用户从繁琐的`EnvoyFilter`配置中脱离出来，所以我们利用`kubernetes`的`CRD`机制，我们定义了一套简便的`API`，即`kubernetes`内的`SmartLimiter`资源。用户只需要按照`SmartLimiter`的规范提交一个`CR`。

```yaml
apiVersion: microservice.slime.io/v1alpha1
kind: SmartLimiter
metadata:
  name: productpage
  namespace: default
spec:
  sets:
    v1:
      descriptor:
      - action:
          fill_interval:
            seconds: 1
          quota: "10"
        condition: "{{.v1.cpu.sum}}>10"
```

自适应限流的另一个目标是让限流策略足够的灵活，比如当服务的`CPU`使用量达到阈值后开启限流，为此我们引入了`prometheus`组件，利用`PromQL`查询相关`metric`，当指标达到阈值后下发一份`envoyfilter`.

## 架构

自适应限流的主体架构分为两个部分，一部分包括`SmartLimiter`到`EnvoyFilter`的逻辑转化，另一部分包括集群内监控数据的获取，包括服务的`CPU`, `Memory`,`POD`数量等数据，具体细节说明参见架构

![](.\media\smartlimiter.jpg)

## 功能介绍

自适应限流模块可以对整个服务或者服务下的分组进行限流，详见[基于监控的自适应限流](./smart_limiter_tutorials_zh.md#基于监控的自适应限流)，[分组限流](./document/smart_limiter_tutorials_zh.md#分组限流)，[服务限流](./document/smart_limiter_tutorials_zh.md#服务限流)

## 安装和使用

`smartlimiter`依赖`prometheus`组件和一些必要的`CRD`声明，详见[安装和使用](./document/smart_limiter_tutorials_zh.md#安装和使用)

## 示例

为`bookinfo`的`reviews`服务开启自适应限流，详见[示例](./document/smart_limiter_tutorials_zh.md#示例)

## E2E测试

功能开发时候，可以通过E2E模块进行模块功能验证。
自适应限流的另一个目标是让限流策略足够的灵活，比如当服务的`CPU`使用量达到阈值后开启限流，为此我们引入了`prometheus`组件，利用`PromQL`查询相关`metric`，当指标达到阈值后下发一份`envoyfilter`.
