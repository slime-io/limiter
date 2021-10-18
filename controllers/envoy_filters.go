package controllers

import (
	"context"
	"fmt"
	networking "istio.io/api/networking/v1alpha3"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	microservicev1alpha1 "slime.io/slime/modules/limiter/api/v1alpha1"
	"slime.io/slime/framework/controllers"
	"slime.io/slime/framework/util"
)

func (r *SmartLimiterReconciler) GenerateEnvoyFilters(spec microservicev1alpha1.SmartLimiterSpec,
	material map[string]string, instance *microservicev1alpha1.SmartLimiter) (
	map[string]*networking.EnvoyFilter, map[string]*microservicev1alpha1.SmartLimitDescriptors) {

	materialInterface := util.MapToMapInterface(material)

	setsEnvoyFilter := make(map[string]*networking.EnvoyFilter)
	setsSmartLimitDescriptor := make(map[string]*microservicev1alpha1.SmartLimitDescriptors)
	host := util.UnityHost(instance.Name, instance.Namespace)
	var sets []*networking.Subset
	if controllers.HostSubsetMapping.Get(host) != nil {
		sets = controllers.HostSubsetMapping.Get(host).([]*networking.Subset)
	} else {
		sets = make([]*networking.Subset, 0, 1)
	}
	//sets = append(sets, &networking.Subset{Name: util.Wellkonw_BaseSet})
	loc := types.NamespacedName{
		Namespace: instance.Namespace,
		Name:      instance.Name,
	}
	svc := &v1.Service{}
	_ = r.Client.Get(context.TODO(), loc, svc)
	svcSelector := svc.Spec.Selector
	// 使用base作为key，可以为基础集合配置限流
	sets = append(sets, &networking.Subset{Name: util.Wellkonw_BaseSet})
	for _, set := range sets {
		if setDescriptor, ok := spec.Sets[set.Name]; ok {
			descriptor := &microservicev1alpha1.SmartLimitDescriptors{}
			for _, des := range setDescriptor.Descriptor_ {
				setDes := &microservicev1alpha1.SmartLimitDescriptor{}
				if shouldUpdate, _ := util.CalculateTemplateBool(des.Condition, materialInterface); shouldUpdate {
					if des.Action != nil {
						if rateLimitValue, err := util.CalculateTemplate(des.Action.Quota, materialInterface); err == nil {
							setDes.Action = &microservicev1alpha1.SmartLimitDescriptor_Action{
								Quota:        fmt.Sprintf("%d", rateLimitValue),
								FillInterval: des.Action.FillInterval,
								Stragety: des.Action.Stragety,
							}
							setDes.Match = des.Match
						}
						descriptor.Descriptor_ = append(descriptor.Descriptor_, setDes)
					}
				}
			}
			selector := util.CopyMap(svcSelector)
			for k, v := range set.Labels {
				selector[k] = v
			}
			if len(descriptor.Descriptor_) > 0 {
				// smartlimiter => envoyfilter
				ef := descriptorsToEnvoyFilter(descriptor.Descriptor_, selector)
				setsEnvoyFilter[set.Name] = ef
				setsSmartLimitDescriptor[set.Name] = descriptor
			} else {
				// Used to delete
				setsEnvoyFilter[set.Name] = nil
			}
		} else {
			// Used to delete
			setsEnvoyFilter[set.Name] = nil
		}
	}
	return setsEnvoyFilter, setsSmartLimitDescriptor
}

// DescriptorsToEnvoyFilter  convert SmartLimitDescriptor to envoy filter
func descriptorsToEnvoyFilter(descriptors []*microservicev1alpha1.SmartLimitDescriptor, labels map[string]string) *networking.EnvoyFilter {

	ef := &networking.EnvoyFilter{
		WorkloadSelector: &networking.WorkloadSelector{
			Labels: labels,
		},
	}
	ef.ConfigPatches = make([]*networking.EnvoyFilter_EnvoyConfigObjectPatch, 0)

	shareDescriptors := make([]*microservicev1alpha1.SmartLimitDescriptor,0)
	localDescriptors := make([]*microservicev1alpha1.SmartLimitDescriptor,0)

	for _, descriptor := range descriptors {
		if descriptor.Action.Stragety == "global" {
			shareDescriptors = append(shareDescriptors,descriptor)
		} else {
			localDescriptors = append(localDescriptors,descriptor)
		}
	}

	// http router
	httpRouterPatches,err := generateHttpRouterPatch(descriptors)
	if err != nil {
		log.Errorf("generateHttpRouterPatch err: %+v",err.Error())
		return nil
	} else if len(httpRouterPatches) > 0 {
		ef.ConfigPatches = append(ef.ConfigPatches, httpRouterPatches...)
	}

	// config plugin envoy.filters.http.ratelimit
	if len(shareDescriptors) >0 {
		// TODO query from global config or add field to specify clusterName
		httpFilterEnvoyRateLimitPatch:= generateHttpFilterEnvoyRateLimitPatch("clusterName")
		if httpFilterEnvoyRateLimitPatch != nil {
			ef.ConfigPatches = append(ef.ConfigPatches, httpFilterEnvoyRateLimitPatch)
		}
	}

	// enable and config plugin envoy.filters.http.local_ratelimit
	if len(localDescriptors) >0 {
		httpFilterLocalRateLimitPatch := generateHttpFilterLocalRateLimit()
		ef.ConfigPatches = append(ef.ConfigPatches, httpFilterLocalRateLimitPatch)

		perFilterPatch := generatePerFilterConfig(localDescriptors)
		ef.ConfigPatches = append(ef.ConfigPatches,perFilterPatch...)
	}
	return ef
}
