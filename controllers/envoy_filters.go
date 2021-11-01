package controllers

import (
	"context"
	"fmt"
	networking "istio.io/api/networking/v1alpha3"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"slime.io/slime/framework/controllers"
	"slime.io/slime/framework/util"
	microservicev1alpha2 "slime.io/slime/modules/limiter/api/v1alpha2"
	"slime.io/slime/modules/limiter/model"
)

func (r *SmartLimiterReconciler) GenerateEnvoyConfigs(spec microservicev1alpha2.SmartLimiterSpec,
	material map[string]string, instance *microservicev1alpha2.SmartLimiter) (
	map[string]*networking.EnvoyFilter, map[string]*microservicev1alpha2.SmartLimitDescriptors, []*model.Descriptor,error) {

	materialInterface := util.MapToMapInterface(material)
	globalDescriptors := make([]*model.Descriptor, 0)
	setsEnvoyFilter := make(map[string]*networking.EnvoyFilter)
	setsSmartLimitDescriptor := make(map[string]*microservicev1alpha2.SmartLimitDescriptors)
	host := util.UnityHost(instance.Name, instance.Namespace)

	var sets []*networking.Subset
	if controllers.HostSubsetMapping.Get(host) != nil {
		sets = controllers.HostSubsetMapping.Get(host).([]*networking.Subset)
	} else {
		sets = make([]*networking.Subset, 0, 1)
	}
	sets = append(sets, &networking.Subset{Name: util.Wellkonw_BaseSet})

	loc := types.NamespacedName{Namespace: instance.Namespace, Name: instance.Name}
	svc := &v1.Service{}
	if err := r.Client.Get(context.TODO(), loc, svc); err != nil {
		if errors.IsNotFound(err) {
			log.Errorf("svc %s:%s is not found", loc.Name, loc.Namespace)
		} else {
			log.Errorf("get svc %s:%s err: %+v", loc.Name, loc.Namespace, err.Error())
		}
		return setsEnvoyFilter, setsSmartLimitDescriptor, globalDescriptors,err
	}
	svcSelector := svc.Spec.Selector

	for _, set := range sets {
		if setDescriptor, ok := spec.Sets[set.Name]; ok {

			validDescriptor := &microservicev1alpha2.SmartLimitDescriptors{}
			for _, des := range setDescriptor.Descriptor_ {
				if shouldUpdate, _ := util.CalculateTemplateBool(des.Condition, materialInterface); shouldUpdate {
					if des.Action != nil {
						if rateLimitValue, err := util.CalculateTemplate(des.Action.Quota, materialInterface); err == nil {
							validDescriptor.Descriptor_ = append(validDescriptor.Descriptor_, &microservicev1alpha2.SmartLimitDescriptor{
								Action: &microservicev1alpha2.SmartLimitDescriptor_Action{
									Quota:        fmt.Sprintf("%d", rateLimitValue),
									FillInterval: des.Action.FillInterval,
									Strategy:     des.Action.Strategy,
								},
								Match:  des.Match,
								Target: des.Target,
							})
						}
					}
				}
			}
			selector := util.CopyMap(svcSelector)
			for k, v := range set.Labels {
				selector[k] = v
			}
			if len(validDescriptor.Descriptor_) > 0 {
				ef := descriptorsToEnvoyFilter(validDescriptor.Descriptor_, selector, loc)
				setsEnvoyFilter[set.Name] = ef
				setsSmartLimitDescriptor[set.Name] = validDescriptor

				desc := descriptorsToGlobalRateLimit(validDescriptor.Descriptor_, loc)
				globalDescriptors = append(globalDescriptors, desc...)
			} else {
				// Used to delete
				setsEnvoyFilter[set.Name] = nil
			}
		} else {
			// Used to delete
			setsEnvoyFilter[set.Name] = nil
		}
	}
	return setsEnvoyFilter, setsSmartLimitDescriptor, globalDescriptors,nil
}

func descriptorsToEnvoyFilter(descriptors []*microservicev1alpha2.SmartLimitDescriptor, labels map[string]string, loc types.NamespacedName) *networking.EnvoyFilter {

	ef := &networking.EnvoyFilter{
		WorkloadSelector: &networking.WorkloadSelector{
			Labels: labels,
		},
	}
	ef.ConfigPatches = make([]*networking.EnvoyFilter_EnvoyConfigObjectPatch, 0)
	globalDescriptors := make([]*microservicev1alpha2.SmartLimitDescriptor, 0)
	localDescriptors := make([]*microservicev1alpha2.SmartLimitDescriptor, 0)

	// split descriptors due to different envoy plugins
	for _, descriptor := range descriptors {
		if descriptor.Action != nil {
			if descriptor.Action.Strategy == model.GlobalSmartLimiter {
				globalDescriptors = append(globalDescriptors, descriptor)
			} else {
				localDescriptors = append(localDescriptors, descriptor)
			}
		}
	}

	// http router
	httpRouterPatches, err := generateHttpRouterPatch(descriptors, loc)
	if err != nil {
		log.Errorf("generateHttpRouterPatch err: %+v", err.Error())
		return nil
	} else if len(httpRouterPatches) > 0 {
		ef.ConfigPatches = append(ef.ConfigPatches, httpRouterPatches...)
	}

	// config plugin envoy.filters.http.ratelimit
	if len(globalDescriptors) > 0 {
		var rlServer string
		for _, item := range globalDescriptors {
			if item.Action.RateLimitService != "" {
				rlServer = item.Action.RateLimitService
				break
			}
		}
		server := getRateLimiterServerCluster(rlServer)
		httpFilterEnvoyRateLimitPatch := generateEnvoyHttpFilterGlobalRateLimitPatch(server)
		if httpFilterEnvoyRateLimitPatch != nil {
			ef.ConfigPatches = append(ef.ConfigPatches, httpFilterEnvoyRateLimitPatch)
		}
	}

	// enable and config plugin envoy.filters.http.local_ratelimit
	if len(localDescriptors) > 0 {
		httpFilterLocalRateLimitPatch := generateHttpFilterLocalRateLimitPatch()
		ef.ConfigPatches = append(ef.ConfigPatches, httpFilterLocalRateLimitPatch)

		perFilterPatch := generateLocalRateLimitPerFilterPatch(localDescriptors, loc)
		ef.ConfigPatches = append(ef.ConfigPatches, perFilterPatch...)
	}
	return ef
}

func descriptorsToGlobalRateLimit(descriptors []*microservicev1alpha2.SmartLimitDescriptor, loc types.NamespacedName) []*model.Descriptor {

	globalDescriptors := make([]*microservicev1alpha2.SmartLimitDescriptor, 0)
	for _, descriptor := range descriptors {
		if descriptor.Action.Strategy == model.GlobalSmartLimiter {
			globalDescriptors = append(globalDescriptors, descriptor)
		}
	}
	return generateGlobalRateLimitDescriptor(globalDescriptors, loc)
}
