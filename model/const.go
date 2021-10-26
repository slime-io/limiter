package model

const (
	ConfigMapName = "rate-limit-config"

	ConfigMapNamespace = "istio-system"

	ConfigMapConfig = "config.yaml"

	GenericKey = "generic_key"

	HeaderValueMatch = "header_value_match"

	QingZhouDomain = "qingzhou"

	Inbound = "inbound"

	GlobalSmartLimiter = "global"

	RateLimitService = "outbound|18081||rate-limit.istio-system.svc.cluster.local"
)
