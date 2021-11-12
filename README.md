- [Adaptive rate limiting overview](#adaptive-rate-limiting-overview)
- [Features](#features)
- [Background](#background)
- [Thinking](#thinking)
- [Architecture](#architecture)
- [Functionality](#functionality)
- [Installation and Usage](#installation-and-usage)
- [Example](#example)
- [E2E testing](#e2e-testing)
## Adaptive rate limiting overview

## Features

1. Easy to use, just submit `SmartLimiter` resources to achieve the purpose of service rate limiting.
2. Adaptive rate limiting, which dynamically triggers rate limiting based on the service's resource content and metrics.

## Background

On the one hand, with the removal of `Mixer`, users have to face the exceptionally complicated `EnvoyFilter` configuration, on the other hand, the fixed rate limiting policy is not flexible enough, and the ordinary rate limiting cannot cover the scenario that the service `CPU` usage reaches the threshold to open the rate limiting. In order to solve these two pain points, we introduced the adaptive rate limiting component `slime/limiter`. Users can simply submit a `SmartLimiter` that meets our definition to complete flexible service rate limiting requirements.

## Thinking

The primary goal of adaptive rate limiting is to free users from the tedious `EnvoyFilter` configuration, so we take advantage of `kubernetes` `CRD` mechanism and we define an easy `API`, the `SmartLimiter` resource within `kubernetes`. Users only need to submit a `CR` according to the `SmartLimiter` specification. 

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

Another goal of adaptive rate limiting is to make the rate limiting policy flexible enough, for example, to turn on rate limiting when the `CPU` usage of a service reaches a threshold, for this we introduce the `prometheus` component, which uses `PromQL` to query the relevant `metric` and produce an `envoyfilter` when the metric reaches the threshold.

## Architecture

The main architecture of adaptive rate limiting is divided into two parts, one part includes the logical transformation from `SmartLimiter` to `EnvoyFilter`, and the other part includes the acquisition of monitoring data within the cluster, including the number of `CPU`, `Memory`, `POD` and other metric data of the service.

! [](./media/smartlimiter.jpg)

## Functionality

The Adaptive rate limiting module can limit the rate of a service or a group under a service, see [Adaptive Limiter based on monitoring](./document/smart_limiter_tutorials.md#adaptive-ratelimit-based-on-metrics), [subset rate limiting](./document/smart_limiter_tutorials.md#subset-ratelimit), [service_limiters](./document/smart_limiter_tutorials.md#service-ratelimit)

## Installation and Usage

`smartlimiter` depends on the `prometheus` component and some necessary `CRD` declarations, see  [Installation and Usage](./document/smart_limiter_tutorials.md#install--use)

## Example

Enable adaptive limiting for `bookinfo`'s `reviews` service, see [example](./document/smart_limiter_tutorials.md#example)

## E2E testing

Module functionality can be verified by the E2E module when the functionality is developed