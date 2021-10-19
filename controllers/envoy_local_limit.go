/*
* @Author: yangdihang
* @Date: 2020/11/19
 */

package controllers

import (
	"fmt"
	envoy_core_v3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	envoy_config_route_v3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	envoy_ratelimit_v3 "github.com/envoyproxy/go-control-plane/envoy/extensions/common/ratelimit/v3"
	envoy_extensions_filters_http_local_ratelimit_v3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/local_ratelimit/v3"
	envoy_match_v3 "github.com/envoyproxy/go-control-plane/envoy/type/matcher/v3"
	envoy_type_v3 "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	structpb "github.com/gogo/protobuf/types"
	"github.com/golang/protobuf/ptypes/duration"
	"github.com/golang/protobuf/ptypes/wrappers"
	"hash/adler32"
	networking "istio.io/api/networking/v1alpha3"
	"k8s.io/apimachinery/pkg/types"
	"slime.io/slime/framework/util"
	microservicev1alpha1 "slime.io/slime/modules/limiter/api/v1alpha1"
	"strconv"
)

// GenerateHttpFilterLocalRateLimit enable local rate limit but without parameters
func generateHttpFilterLocalRateLimit() *networking.EnvoyFilter_EnvoyConfigObjectPatch {

	patch := &networking.EnvoyFilter_EnvoyConfigObjectPatch{
		ApplyTo: networking.EnvoyFilter_HTTP_FILTER,
		Match: &networking.EnvoyFilter_EnvoyConfigObjectMatch{
			Context: networking.EnvoyFilter_SIDECAR_INBOUND,
			ObjectTypes: &networking.EnvoyFilter_EnvoyConfigObjectMatch_Listener{
				Listener: &networking.EnvoyFilter_ListenerMatch{
					FilterChain: &networking.EnvoyFilter_ListenerMatch_FilterChainMatch{
						Filter: &networking.EnvoyFilter_ListenerMatch_FilterMatch{
							Name: util.Envoy_HttpConnectionManager,
							SubFilter: &networking.EnvoyFilter_ListenerMatch_SubFilterMatch{
								Name: util.Envoy_Route,
							},
						},
					},
				},
			},
		},
		Patch: &networking.EnvoyFilter_Patch{
			Operation: networking.EnvoyFilter_Patch_INSERT_BEFORE,
			Value: &structpb.Struct{
				Fields: map[string]*structpb.Value{
					util.Struct_HttpFilter_Name: {
						Kind: &structpb.Value_StringValue{StringValue: util.Envoy_LocalRateLimit},
					},
					util.Struct_HttpFilter_TypedConfig: {
						Kind: &structpb.Value_StructValue{
							StructValue: &structpb.Struct{
								Fields: map[string]*structpb.Value{
									util.Struct_Any_AtType: {
										Kind: &structpb.Value_StringValue{StringValue: util.TypeUrl_EnvoyLocalRatelimit},
									},
									"stat-prefix": {
										Kind: &structpb.Value_StringValue{StringValue: "http_local_rate_limiter"},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	return patch
}

// TODO
func generateEntries(item *microservicev1alpha1.SmartLimitDescriptor,loc types.NamespacedName) []*envoy_ratelimit_v3.RateLimitDescriptor_Entry {
	entries := make([]*envoy_ratelimit_v3.RateLimitDescriptor_Entry, 0)
	var entry *envoy_ratelimit_v3.RateLimitDescriptor_Entry
	if len(item.Match) < 1 {
		entry = &envoy_ratelimit_v3.RateLimitDescriptor_Entry{}
		if item.Key != "" && item.Value != "" {
			entry.Key = item.Key
			entry.Value = item.Value
		} else {
			entry.Key = "generic_key"
			entry.Value = generateDescriptorValue(item,loc)
		}
	} else {
		entry = &envoy_ratelimit_v3.RateLimitDescriptor_Entry{}
		if item.Key != "" && item.Value != "" {
			entry.Key = item.Key
			entry.Value = item.Value
		} else {
			entry.Key = "header_value_match"
			entry.Value = generateDescriptorValue(item,loc)
		}
	}
	entries = append(entries, entry)
	return entries
}

func generateTokenBucket(item *microservicev1alpha1.SmartLimitDescriptor) *envoy_type_v3.TokenBucket {
	i, _ := strconv.Atoi(item.Action.Quota)
	return &envoy_type_v3.TokenBucket{
		MaxTokens: uint32(i),
		FillInterval: &duration.Duration{
			Seconds: item.Action.FillInterval.Seconds,
			Nanos:   item.Action.FillInterval.Nanos,
		},
		TokensPerFill: &wrappers.UInt32Value{Value: uint32(i)},
	}
}

// TODO  default bucket is needed, set 100000/s ?
func generateDefaultTokenBucket(maxTokens,tokensPerFill,second int) *envoy_type_v3.TokenBucket{
	return &envoy_type_v3.TokenBucket{
		MaxTokens: uint32(maxTokens),
		FillInterval: &duration.Duration{
			Seconds: int64(second),
		},
		TokensPerFill: &wrappers.UInt32Value{Value: uint32(tokensPerFill)},
	}
}


func generateLocalRateLimitDescriptors(descriptors []*microservicev1alpha1.SmartLimitDescriptor,loc types.NamespacedName) []*envoy_ratelimit_v3.LocalRateLimitDescriptor {

	localRateLimitDescriptors := make([]*envoy_ratelimit_v3.LocalRateLimitDescriptor, 0)
	for _, item := range descriptors {
		entries := generateEntries(item,loc)
		tokenBucket := generateTokenBucket(item)
		localRateLimitDescriptors = append(localRateLimitDescriptors, &envoy_ratelimit_v3.LocalRateLimitDescriptor{
			Entries:     entries,
			TokenBucket: tokenBucket,
		})
	}
	return localRateLimitDescriptors
}

// todo query from api-server , how to validate api
func generateVhostRouteName(descriptor *microservicev1alpha1.SmartLimitDescriptor) string {
	direction := descriptor.Target.Diretcion
	//port := descriptor.Target.Port
	if direction == "" {
		direction = "inbound"
	}
	return fmt.Sprintf("%s|%s|%d",direction,"http",descriptor.Target.Port)
}

func generateDescriptorValue(item *microservicev1alpha1.SmartLimitDescriptor,loc types.NamespacedName) string {

	id := adler32.Checksum([]byte(item.String()+loc.String()))
	return fmt.Sprintf("Service[%s.%s]-User[none]-Id[%d]",loc.Name,loc.Namespace,id)
}

// actions in route.rate_limits, tag
func generateRouteRateLimitAction(descriptor *microservicev1alpha1.SmartLimitDescriptor,loc types.NamespacedName) *envoy_config_route_v3.RateLimit_Action {

	var action *envoy_config_route_v3.RateLimit_Action

	if len(descriptor.Match) < 1 {
		action.ActionSpecifier = &envoy_config_route_v3.RateLimit_Action_GenericKey_{
			GenericKey: &envoy_config_route_v3.RateLimit_Action_GenericKey{
				DescriptorValue: generateDescriptorValue(descriptor,loc),
			},
		}
	} else {
		headers := make([]*envoy_config_route_v3.HeaderMatcher,0)
		var header *envoy_config_route_v3.HeaderMatcher
		for _,match := range descriptor.Match {
			if match.RegexMatch != "" {
				header.Name = match.Name
				header.InvertMatch = false
				header.HeaderMatchSpecifier = &envoy_config_route_v3.HeaderMatcher_SafeRegexMatch {
					SafeRegexMatch: &envoy_match_v3.RegexMatcher{
						EngineType: &envoy_match_v3.RegexMatcher_GoogleRe2{},
						Regex:      match.RegexMatch,
					},
				}
			} else {
				present := false
				if match.PresentMatch == "true" {
					present = true
				}
				header.Name = match.Name
				header.InvertMatch = false
				header.HeaderMatchSpecifier = &envoy_config_route_v3.HeaderMatcher_PresentMatch{
					PresentMatch: present,
				}
			}
		}
		action.ActionSpecifier = &envoy_config_route_v3.RateLimit_Action_HeaderValueMatch_{
			HeaderValueMatch: &envoy_config_route_v3.RateLimit_Action_HeaderValueMatch{
				DescriptorValue: generateDescriptorValue(descriptor,loc),
				Headers: headers,
			},
		}
	}
	return action
}

func generateHttpRouterPatch(descriptors []*microservicev1alpha1.SmartLimitDescriptor,loc types.NamespacedName) ([]*networking.EnvoyFilter_EnvoyConfigObjectPatch,error) {

	patches := make([]*networking.EnvoyFilter_EnvoyConfigObjectPatch,0)
	// actions in per router
	route2RateLimitsActions := make(map[string][]*envoy_config_route_v3.RateLimit_Action)

	for _,descriptor := range descriptors {
		vhostRoute := generateVhostRouteName(descriptor)
		action := generateRouteRateLimitAction(descriptor,loc)
		if _,ok := route2RateLimitsActions[vhostRoute]; !ok {
			route2RateLimitsActions[vhostRoute] = []*envoy_config_route_v3.RateLimit_Action{action}
		} else {
			route2RateLimitsActions[vhostRoute] = append(route2RateLimitsActions[vhostRoute],action)
		}
	}

	rateLimit := &envoy_config_route_v3.RateLimit{}
	for item,action := range route2RateLimitsActions {
		rateLimit.Actions = action
		rateLimit.Stage =  &wrappers.UInt32Value{Value: uint32(0)}
		rateLimitStruct, err := util.MessageToStruct(rateLimit)
		if err != nil {
			return nil,err
		}
		patch := &networking.EnvoyFilter_EnvoyConfigObjectPatch{
			ApplyTo: networking.EnvoyFilter_HTTP_ROUTE,
			Match: &networking.EnvoyFilter_EnvoyConfigObjectMatch{
				ObjectTypes: &networking.EnvoyFilter_EnvoyConfigObjectMatch_RouteConfiguration{
					RouteConfiguration: &networking.EnvoyFilter_RouteConfigurationMatch{
						Vhost: &networking.EnvoyFilter_RouteConfigurationMatch_VirtualHostMatch{
							Name: item,	// TODO
							Route: &networking.EnvoyFilter_RouteConfigurationMatch_RouteMatch{
								Name: "default", // TODO
							},
						},
					},
				},
			},
			Patch: &networking.EnvoyFilter_Patch{
				Operation: networking.EnvoyFilter_Patch_MERGE,
				Value: &structpb.Struct{
					Fields: map[string]*structpb.Value{
						"route": {
							Kind: &structpb.Value_StructValue{
								StructValue: &structpb.Struct{
									Fields: map[string]*structpb.Value{
										"rate_limits": {
											Kind: &structpb.Value_StructValue{
												StructValue: rateLimitStruct,
											},
										},
									},
								},
							},
						},
					},
				},
			},
		}
		patches = append(patches,patch)
	}

	return patches,nil
}


func generatePerFilterConfig(descriptors []*microservicev1alpha1.SmartLimitDescriptor,loc types.NamespacedName) []*networking.EnvoyFilter_EnvoyConfigObjectPatch {

	patches := make([]*networking.EnvoyFilter_EnvoyConfigObjectPatch,0)
	// multi routes
	route2Descriptors := make(map[string][]*microservicev1alpha1.SmartLimitDescriptor)

	for _,descriptor := range descriptors {
		vhostRoute := generateVhostRouteName(descriptor)
		if _,ok := route2Descriptors[vhostRoute]; !ok {
			route2Descriptors[vhostRoute] = []*microservicev1alpha1.SmartLimitDescriptor{descriptor}
		} else {
			route2Descriptors[vhostRoute] = append(route2Descriptors[vhostRoute],descriptor)
		}
	}

	for vhostRoute, desc := range route2Descriptors {
		localRateLimitDescriptors := generateLocalRateLimitDescriptors(desc,loc)
		localRateLimit := &envoy_extensions_filters_http_local_ratelimit_v3.LocalRateLimit{
			TokenBucket: generateDefaultTokenBucket(100000,100000,1),
			Descriptors: localRateLimitDescriptors,
			StatPrefix: util.Struct_EnvoyLocalRateLimit_Limiter,
			FilterEnabled: &envoy_core_v3.RuntimeFractionalPercent{
				RuntimeKey: util.Struct_EnvoyLocalRateLimit_Enabled,
				DefaultValue: &envoy_type_v3.FractionalPercent{
					Numerator:   100,
					Denominator: envoy_type_v3.FractionalPercent_HUNDRED,
				},
			},
			FilterEnforced: &envoy_core_v3.RuntimeFractionalPercent{
				RuntimeKey: util.Struct_EnvoyLocalRateLimit_Enforced,
				DefaultValue: &envoy_type_v3.FractionalPercent{
					Numerator:   100,
					Denominator: envoy_type_v3.FractionalPercent_HUNDRED,
				},
			},
		}
		local, err := util.MessageToStruct(localRateLimit)
		if err != nil {
			return nil
		}

		patch := &networking.EnvoyFilter_EnvoyConfigObjectPatch{
			ApplyTo: networking.EnvoyFilter_HTTP_ROUTE,
			Match: &networking.EnvoyFilter_EnvoyConfigObjectMatch{
				ObjectTypes: &networking.EnvoyFilter_EnvoyConfigObjectMatch_RouteConfiguration{
					RouteConfiguration: &networking.EnvoyFilter_RouteConfigurationMatch{
						Vhost: &networking.EnvoyFilter_RouteConfigurationMatch_VirtualHostMatch{
							Name: vhostRoute,
							Route: &networking.EnvoyFilter_RouteConfigurationMatch_RouteMatch{
								Name: "default", // todo
							},
						},
					},
				},
			},
			Patch: &networking.EnvoyFilter_Patch{
				Operation: networking.EnvoyFilter_Patch_MERGE,
				Value: &structpb.Struct{
					Fields: map[string]*structpb.Value{
						"typed_per_filter_config": {
							Kind: &structpb.Value_StructValue{
								StructValue: &structpb.Struct{
									Fields: map[string]*structpb.Value{
										util.Envoy_LocalRateLimit:{
											Kind: &structpb.Value_StructValue{
												StructValue: local,
											},
										},
									},
								},
							},
						},
					},
				},
			},
		}
		patches = append(patches,patch)
	}
	return patches
}

//func descriptorsToEnvoyFilter(descriptor []*microservicev1alpha1.SmartLimitDescriptor, labels map[string]string) *networking.EnvoyFilter {
//	ef := &networking.EnvoyFilter{
//		WorkloadSelector: &networking.WorkloadSelector{
//			Labels: labels,
//		},
//	}
//	ef.ConfigPatches = make([]*networking.EnvoyFilter_EnvoyConfigObjectPatch, 0)
//	// envoy local ratelimit 不支持header match，因此仅应存在一条
//	des := descriptor[0]
//	i, _ := strconv.Atoi(des.Action.Quota)
//	envoyLocDes := &envoy_extensions_filters_http_local_ratelimit_v3.LocalRateLimit{
//		StatPrefix: util.Struct_EnvoyLocalRateLimit_Limiter,
//		TokenBucket: &envoy_type_v3.TokenBucket{
//			MaxTokens: uint32(i),
//			FillInterval: &duration.Duration{
//				Seconds: des.Action.FillInterval.Seconds,
//				Nanos:   des.Action.FillInterval.Nanos,
//			},
//		},
//		FilterEnabled: &envoy_config_core_v3.RuntimeFractionalPercent{
//			RuntimeKey: util.Struct_EnvoyLocalRateLimit_Enabled,
//			DefaultValue: &envoy_type_v3.FractionalPercent{
//				Numerator:   100,
//				Denominator: envoy_type_v3.FractionalPercent_HUNDRED,
//			},
//		},
//		FilterEnforced: &envoy_config_core_v3.RuntimeFractionalPercent{
//			RuntimeKey: util.Struct_EnvoyLocalRateLimit_Enforced,
//			DefaultValue: &envoy_type_v3.FractionalPercent{
//				Numerator:   100,
//				Denominator: envoy_type_v3.FractionalPercent_HUNDRED,
//			},
//		},
//	}
//	t, err := util.MessageToStruct(envoyLocDes)
//	if err == nil {
//		patch := &networking.EnvoyFilter_EnvoyConfigObjectPatch{
//			ApplyTo: networking.EnvoyFilter_HTTP_FILTER,
//			Match: &networking.EnvoyFilter_EnvoyConfigObjectMatch{
//				Context: networking.EnvoyFilter_SIDECAR_INBOUND,
//				ObjectTypes: &networking.EnvoyFilter_EnvoyConfigObjectMatch_Listener{
//					Listener: &networking.EnvoyFilter_ListenerMatch{
//						FilterChain: &networking.EnvoyFilter_ListenerMatch_FilterChainMatch{
//							Filter: &networking.EnvoyFilter_ListenerMatch_FilterMatch{
//								Name: util.Envoy_HttpConnectionManager,
//								SubFilter: &networking.EnvoyFilter_ListenerMatch_SubFilterMatch{
//									Name: util.Envoy_Route,
//								},
//							},
//						},
//					},
//				},
//			},
//			Patch: &networking.EnvoyFilter_Patch{
//				Operation: networking.EnvoyFilter_Patch_INSERT_BEFORE,
//				Value: &structpb.Struct{
//					Fields: map[string]*structpb.Value{
//						util.Struct_HttpFilter_Name: {
//							Kind: &structpb.Value_StringValue{StringValue: util.Envoy_LocalRateLimit},
//						},
//						util.Struct_HttpFilter_TypedConfig: {
//							Kind: &structpb.Value_StructValue{
//								StructValue: &structpb.Struct{
//									Fields: map[string]*structpb.Value{
//										util.Struct_Any_AtType: {
//											Kind: &structpb.Value_StringValue{StringValue: util.TypeUrl_UdpaTypedStruct},
//										},
//										util.Struct_Any_TypedUrl: {
//											Kind: &structpb.Value_StringValue{StringValue: util.TypeUrl_EnvoyLocalRatelimit},
//										},
//										util.Struct_Any_Value: {
//											Kind: &structpb.Value_StructValue{StructValue: t},
//										},
//									},
//								},
//							},
//						},
//					},
//				},
//			},
//		}
//		ef.ConfigPatches = append(ef.ConfigPatches, patch)
//	}
//	return ef
//}
