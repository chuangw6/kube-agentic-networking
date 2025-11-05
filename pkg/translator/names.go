package translator

const (
	// ListenerNameFormat is the format string for Envoy listener names, becoming `listener-<port>`.
	ListenerNameFormat = "listener-%d"
	// RouteNameFormat is the format string for Envoy route configuration names, becoming `route-<port>`.
	RouteNameFormat = "route-%d"
	// EnvoyRouteNameFormat is the format string for individual Envoy route names within a RouteConfiguration,
	// becoming `<namespace>-<httproute-name>-rule<rule-index>-match<match-index>`.
	EnvoyRouteNameFormat = "%s-%s-rule%d-match%d"
	// VHostNameFormat is the format string for Envoy virtual host names, becoming `<gateway-name>-vh-<port>-<domain>`.
	VHostNameFormat = "%s-vh-%d-%s"
	// ClusterNameFormat is the format string for Envoy cluster names, becoming `<namespace>-<backend-name>`.
	ClusterNameFormat = "%s-%s"
	// RBACPolicyNameFormat is the format string for Envoy RBAC policies, becoming `<namespace>-<backend-name>-rule-<rule-index>`.
	RBACPolicyNameFormat = "%s-%s-rule-%d"
)

const (
	// JWKSFilePath is the file path where the JWKS file is mounted in the Envoy container.
	JWKSFilePath = "/etc/envoy/jwks/jwks.json"
	// SAAuthTokenHeader is the header used to carry the Kubernetes service account token.
	SAAuthTokenHeader = "x-k8s-sa-token"
	// UserRoleHeader is the header populated with the subject claim from the JWT.
	UserRoleHeader = "x-user-role"
)
