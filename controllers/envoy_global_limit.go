package controllers

import (
	"context"
	envoy_core_v3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	envoy_config_ratelimit_v3 "github.com/envoyproxy/go-control-plane/envoy/config/ratelimit/v3"
	structpb "github.com/gogo/protobuf/types"
	networking "istio.io/api/networking/v1alpha3"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"slime.io/slime/framework/util"
)

// GenerateHttpFilterEnvoyRateLimitPatch  TODO  hard code
func generateHttpFilterEnvoyRateLimitPatch(clusterName string) *networking.EnvoyFilter_EnvoyConfigObjectPatch{

	rateLimitServiceConfig := generateRateLimitService(clusterName)
	t, err := util.MessageToStruct(rateLimitServiceConfig)
	if err != nil {
		log.Errorf("MessageToStruct err: %+v",err.Error())
		return nil
	}

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
					util.Struct_HttpFilter_Name: {  // TODO
						Kind: &structpb.Value_StringValue{StringValue: "envoy.filters.http.ratelimit"},
					},
					util.Struct_HttpFilter_TypedConfig: {
						Kind: &structpb.Value_StructValue{
							StructValue: &structpb.Struct{
								Fields: map[string]*structpb.Value{
									util.Struct_Any_AtType: {
										Kind: &structpb.Value_StringValue{StringValue: util.TypeUrl_UdpaTypedStruct},
									},
									util.Struct_Any_TypedUrl: {
										Kind: &structpb.Value_StringValue{StringValue: "type.googleapis.com/envoy.extensions.filters.http.ratelimit.v3.RateLimit"},
									},
									util.Struct_Any_Value: {
										Kind: &structpb.Value_StructValue{
											StructValue: &structpb.Struct{
												Fields: map[string]*structpb.Value{
													"domain": {
														Kind: &structpb.Value_StringValue{StringValue: "qingzhou"},
													},
													"rate_limit_service" : {
														Kind: &structpb.Value_StructValue{StructValue: t},
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
	return patch
}

func generateRateLimitService(clusterName string) *envoy_config_ratelimit_v3.RateLimitServiceConfig{

	envoyGrpc := &envoy_core_v3.GrpcService_EnvoyGrpc{
		ClusterName: clusterName,
	}
	service := &envoy_core_v3.GrpcService{
		TargetSpecifier: &envoy_core_v3.GrpcService_EnvoyGrpc_{EnvoyGrpc: envoyGrpc},
	}

	rateLimitServiceConfig := &envoy_config_ratelimit_v3.RateLimitServiceConfig{
		GrpcService:   service,
		TransportApiVersion: envoy_core_v3.ApiVersion_V3,
	}
	return rateLimitServiceConfig
}

// TODO get parameters from global config
func generateNamespaceName() types.NamespacedName {
	return types.NamespacedName{
		Namespace: "istio-system",
		Name:      "rate-limit-config",
	}
}

// UpdateConfigMap  how to find acquire client
func UpdateConfigMap(client client.Client) {

	loc := generateNamespaceName()
	cm := &v1.ConfigMap{}
	_ = client.Get(context.TODO(), loc, cm)
	//


}