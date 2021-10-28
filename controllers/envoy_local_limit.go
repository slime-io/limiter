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
	"slime.io/slime/modules/limiter/model"
	"strconv"
)

// GenerateHttpFilterLocalRateLimit enable local rate limit but without parameters
func enableHttpFilterLocalRateLimit() *networking.EnvoyFilter_EnvoyConfigObjectPatch {

	patch := &networking.EnvoyFilter_EnvoyConfigObjectPatch{
		ApplyTo: networking.EnvoyFilter_HTTP_FILTER,
		Match: generateEnvoyHttpFilterMatch(),
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
									"stat_prefix": {
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
func generateLocalRateLimitDescriptorEntries(item *microservicev1alpha1.SmartLimitDescriptor, loc types.NamespacedName) []*envoy_ratelimit_v3.RateLimitDescriptor_Entry {

	entries := make([]*envoy_ratelimit_v3.RateLimitDescriptor_Entry, 0)
	entry := &envoy_ratelimit_v3.RateLimitDescriptor_Entry{}

	if item.CustomKey != "" && item.CustomValue != "" {
		entry.Key = item.CustomKey
		entry.Value = item.CustomValue
	}else if len(item.Match)==0 {
		entry.Key = model.GenericKey
		entry.Value = generateDescriptorValue(item,loc)
	}else {
		entry.Key = model.HeaderValueMatch
		entry.Value = generateDescriptorValue(item, loc)
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
func generateCustomTokenBucket(maxTokens, tokensPerFill, second int) *envoy_type_v3.TokenBucket {
	return &envoy_type_v3.TokenBucket{
		MaxTokens: uint32(maxTokens),
		FillInterval: &duration.Duration{
			Seconds: int64(second),
		},
		TokensPerFill: &wrappers.UInt32Value{Value: uint32(tokensPerFill)},
	}
}

func generateLocalRateLimitDescriptors(descriptors []*microservicev1alpha1.SmartLimitDescriptor, loc types.NamespacedName) []*envoy_ratelimit_v3.LocalRateLimitDescriptor {

	localRateLimitDescriptors := make([]*envoy_ratelimit_v3.LocalRateLimitDescriptor, 0)
	for _, item := range descriptors {
		entries := generateLocalRateLimitDescriptorEntries(item, loc)
		tokenBucket := generateTokenBucket(item)
		localRateLimitDescriptors = append(localRateLimitDescriptors, &envoy_ratelimit_v3.LocalRateLimitDescriptor{
			Entries:     entries,
			TokenBucket: tokenBucket,
		})
	}
	return localRateLimitDescriptors
}

// inbound  direction|protocol|port
// TODO: outbound host:port
func generateVhostName(target *microservicev1alpha1.SmartLimitDescriptor_Target) string {
	direction := target.Direction
	if direction == "" {
		direction = model.Inbound
	}
	return fmt.Sprintf("%s|%s|%d", direction, "http",target.Port)
}

func generateDescriptorValue(item *microservicev1alpha1.SmartLimitDescriptor, loc types.NamespacedName) string {
	id := adler32.Checksum([]byte(item.String() + loc.String()))
	return fmt.Sprintf("Service[%s.%s]-User[none]-Id[%d]", loc.Name, loc.Namespace, id)
}

func generateSafeRegexMatch(match *microservicev1alpha1.SmartLimitDescriptor_HeaderMatcher) *envoy_config_route_v3.HeaderMatcher_SafeRegexMatch {
	return &envoy_config_route_v3.HeaderMatcher_SafeRegexMatch{
		SafeRegexMatch: &envoy_match_v3.RegexMatcher{
			EngineType: &envoy_match_v3.RegexMatcher_GoogleRe2{},
			Regex:      match.RegexMatch,
		},
	}
}


/*
// if key/value is not empty, envoyplugin is needed, we will not generate http route patch
// 有match时，只有当header中的值与match相匹配才会进行对路由进行action限流，需要注意的是RegexMatch(name 的值是否匹配正则)与
// PresentMatch(name是否存在)互斥
// 这里之前打算在pb声明为oneof,但是用kubebuilder生成api的过程中无法识别相关interface{}
// 没有match的时是一般的限流策略
*/
func generateRouteRateLimitAction(descriptor *microservicev1alpha1.SmartLimitDescriptor, loc types.NamespacedName) *envoy_config_route_v3.RateLimit_Action {
	action := &envoy_config_route_v3.RateLimit_Action{}

	if descriptor.CustomKey != "" && descriptor.CustomValue != "" {
		log.Infof("customKey/customValue is not empty, users should apply a envoyplugin with same customKey/customValue")
		return nil
	}else if len(descriptor.Match) ==0 {
		action.ActionSpecifier = &envoy_config_route_v3.RateLimit_Action_GenericKey_{
			GenericKey: &envoy_config_route_v3.RateLimit_Action_GenericKey{
				DescriptorValue: generateDescriptorValue(descriptor, loc),
			},
		}
	} else {
		headers := make([]*envoy_config_route_v3.HeaderMatcher, 0)
		for _, match := range descriptor.Match {
			header := &envoy_config_route_v3.HeaderMatcher{}

			if match.RegexMatch != "" {
				header.Name = match.Name
				header.HeaderMatchSpecifier = generateSafeRegexMatch(match)
			} else {
				present := false
				if match.PresentMatch == "true" {
					present = true
				}
				header.Name = match.Name
				header.HeaderMatchSpecifier = &envoy_config_route_v3.HeaderMatcher_PresentMatch{
					PresentMatch: present,
				}
			}
			headers = append(headers, header)
		}
		action.ActionSpecifier = &envoy_config_route_v3.RateLimit_Action_HeaderValueMatch_{
			HeaderValueMatch: &envoy_config_route_v3.RateLimit_Action_HeaderValueMatch{
				DescriptorValue: generateDescriptorValue(descriptor, loc),
				Headers:         headers,
			},
		}
	}
	return action
}

func generateHttpRouterPatch(descriptors []*microservicev1alpha1.SmartLimitDescriptor, loc types.NamespacedName) ([]*networking.EnvoyFilter_EnvoyConfigObjectPatch, error) {

	patches := make([]*networking.EnvoyFilter_EnvoyConfigObjectPatch, 0)
	route2RateLimitsActions := make(map[string][]*envoy_config_route_v3.RateLimit_Action)


	// action 在 ratelimit 下
	for _, descriptor := range descriptors {
		vhostName := generateVhostName(descriptor.Target)
		action := generateRouteRateLimitAction(descriptor, loc)
		// strange logic
		if action == nil {
			continue
		}
		if actions, ok := route2RateLimitsActions[vhostName]; !ok {
			route2RateLimitsActions[vhostName] = []*envoy_config_route_v3.RateLimit_Action{action}
		} else {
			actions = append(actions, action)
		}
	}
	for vhostName, actions := range route2RateLimitsActions {

		rateLimits := make([]*envoy_config_route_v3.RateLimit,0)
		for _,action := range actions {
			rateLimits = append(rateLimits,&envoy_config_route_v3.RateLimit{Actions: []*envoy_config_route_v3.RateLimit_Action{action}})
		}
		route := &envoy_config_route_v3.Route{
			Action: &envoy_config_route_v3.Route_Route{
				Route: &envoy_config_route_v3.RouteAction{
					RateLimits: rateLimits,
				},
			},
		}
		routeStruct, err := util.MessageToStruct(route)
		if err != nil {
			return nil, err
		}
		patch := &networking.EnvoyFilter_EnvoyConfigObjectPatch{
			ApplyTo: networking.EnvoyFilter_HTTP_ROUTE,
			Match: &networking.EnvoyFilter_EnvoyConfigObjectMatch{
				ObjectTypes: &networking.EnvoyFilter_EnvoyConfigObjectMatch_RouteConfiguration{
					RouteConfiguration: &networking.EnvoyFilter_RouteConfigurationMatch{
						Vhost: &networking.EnvoyFilter_RouteConfigurationMatch_VirtualHostMatch{
							Name: vhostName, // TODO
							Route: &networking.EnvoyFilter_RouteConfigurationMatch_RouteMatch{
								Name: "default", // TODO
							},
						},
					},
				},
			},
			Patch: &networking.EnvoyFilter_Patch{
				Operation: networking.EnvoyFilter_Patch_MERGE,
				Value:     routeStruct,
			},
		}
		patches = append(patches, patch)
	}
	return patches, nil
}

func generatePerFilterConfig(descriptors []*microservicev1alpha1.SmartLimitDescriptor, loc types.NamespacedName) []*networking.EnvoyFilter_EnvoyConfigObjectPatch {

	patches := make([]*networking.EnvoyFilter_EnvoyConfigObjectPatch, 0)
	// multi routes
	route2Descriptors := make(map[string][]*microservicev1alpha1.SmartLimitDescriptor)

	for _, descriptor := range descriptors {
		vhostName := generateVhostName(descriptor.Target)
		if des, ok := route2Descriptors[vhostName]; !ok {
			route2Descriptors[vhostName] = []*microservicev1alpha1.SmartLimitDescriptor{descriptor}
		} else {
			des = append(des, descriptor)
		}
	}

	for vhostName, desc := range route2Descriptors {
		localRateLimitDescriptors := generateLocalRateLimitDescriptors(desc, loc)
		localRateLimit := &envoy_extensions_filters_http_local_ratelimit_v3.LocalRateLimit{
			TokenBucket: generateCustomTokenBucket(100000, 100000, 1),
			Descriptors: localRateLimitDescriptors,
			StatPrefix:  util.Struct_EnvoyLocalRateLimit_Limiter,
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
							Name: vhostName,
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
										util.Envoy_LocalRateLimit: {
											Kind: &structpb.Value_StructValue{
												StructValue: &structpb.Struct{
													Fields: map[string]*structpb.Value{
														util.Struct_Any_AtType: {
															Kind: &structpb.Value_StringValue{StringValue: util.TypeUrl_UdpaTypedStruct},
														},
														"type_url": {
															Kind: &structpb.Value_StringValue{StringValue: util.TypeUrl_EnvoyLocalRatelimit},
														},
														"value" : {
															Kind: &structpb.Value_StructValue{StructValue: local},
														},
													},
												},
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
		patches = append(patches, patch)
	}
	return patches
}
