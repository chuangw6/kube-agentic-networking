package translator

import (
	"fmt"

	rbacconfigv3 "github.com/envoyproxy/go-control-plane/envoy/config/rbac/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	rbacv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/rbac/v3"
	matcherv3 "github.com/envoyproxy/go-control-plane/envoy/type/matcher/v3"
	"k8s.io/apimachinery/pkg/labels"
	agenticv1alpha1 "sigs.k8s.io/kube-agentic-networking/api/agentic/v1alpha1"
	agenticlisters "sigs.k8s.io/kube-agentic-networking/k8s/client/listers/agentic/v1alpha1"
)

const (
	// allowMCPSessionClosePolicyName is the name of the RBAC policy that allows agents to close MCP sessions.
	allowMCPSessionClosePolicyName = "allow-mcp-session-close"

	// allowAnyoneToInitializeAndListToolsPolicyName is the name of the RBAC policy that allows anyone to initialize a session and list available tools.
	allowAnyoneToInitializeAndListToolsPolicyName = "allow-anyone-to-initialize-and-list-tools"
	initializeMethod                              = "initialize"
	initializedMethod                             = "notifications/initialized"
	toolsListMethod                               = "tools/list"

	mcpSessionIDHeader = "mcp-session-id"
	toolsCallMethod    = "tools/call"
	mcpProxyFilterName = "mcp_proxy"
)

// rbacConfigFromAuthPolicy generates all RBAC policies for a given backend, including common policies
// and those derived from AuthPolicy resources.
func rbacConfigFromAuthPolicy(authPolicyLister agenticlisters.AuthPolicyLister, backend *agenticv1alpha1.Backend) (*rbacv3.RBAC, error) {
	var rbacPolicies = make(map[string]*rbacconfigv3.Policy)

	// Add AuthPolicy-derived RBAC policies.
	// Currently, we assume only one AuthPolicy targets a given backend.
	authPolicy, err := findAuthPolicyForBackend(backend, authPolicyLister)
	if err != nil {
		return nil, err
	}
	if authPolicy != nil {
		rbacPolicies = translateAuthPolicyToRBAC(authPolicy, backend)
	}

	// Determine the RBAC action based on the AuthPolicy.
	// Currently, only ALLOW action is supported.
	action := rbacActionFromAuthPolicy(authPolicy)

	// If it's deny-by-default (i.e., ALLOW action), we explicitly allow necessary
	// MCP operations for all backends. These policies are essential for MCP
	// session management and tool initialization.
	if action == rbacconfigv3.RBAC_ALLOW {
		rbacPolicies[allowMCPSessionClosePolicyName] = buildAllowMCPSessionClosePolicy()
		rbacPolicies[allowAnyoneToInitializeAndListToolsPolicyName] = buildAllowAnyoneToInitializeAndListToolsPolicy()
	}

	rbacConfig := &rbacv3.RBAC{
		Rules: &rbacconfigv3.RBAC{
			Action:   action,
			Policies: rbacPolicies,
		},
	}

	return rbacConfig, nil
}

// Currently, only ALLOW action is supported.
func rbacActionFromAuthPolicy(authPolicy *agenticv1alpha1.AuthPolicy) rbacconfigv3.RBAC_Action {
	defaultAction := rbacconfigv3.RBAC_ALLOW
	if authPolicy == nil {
		return defaultAction // Default to ALLOW if no AuthPolicy is defined.
	}
	switch authPolicy.Spec.Action {
	case agenticv1alpha1.ActionAllow:
		return rbacconfigv3.RBAC_ALLOW
	default:
		return defaultAction // Default to ALLOW if unspecified.
	}
}

// findAuthPolicyForBackend finds the AuthPolicy that targets the given backend.
// It assumes that there is only one AuthPolicy for each backend.
func findAuthPolicyForBackend(backend *agenticv1alpha1.Backend, authPolicyLister agenticlisters.AuthPolicyLister) (*agenticv1alpha1.AuthPolicy, error) {
	// List all AuthPolicies in the Backend's namespace.
	allAuthPolicies, err := authPolicyLister.AuthPolicies(backend.Namespace).List(labels.Everything())
	if err != nil {
		return nil, fmt.Errorf("failed to list AuthPolicies in namespace %s: %w", backend.Namespace, err)
	}

	// Find the first AuthPolicy that targets this specific backend.
	// We assume only one AuthPolicy will target a given backend.
	// TODO: Enforce this uniqueness constraint at the API level or merge multiple policies if needed.
	for _, authPolicy := range allAuthPolicies {
		if authPolicy.Spec.TargetRef.Kind == "Backend" && string(authPolicy.Spec.TargetRef.Name) == backend.Name {
			return authPolicy, nil
		}
	}
	return nil, nil // No AuthPolicy found for the backend.
}

func translateAuthPolicyToRBAC(authPolicy *agenticv1alpha1.AuthPolicy, backend *agenticv1alpha1.Backend) map[string]*rbacconfigv3.Policy {
	policies := make(map[string]*rbacconfigv3.Policy)

	for i, rule := range authPolicy.Spec.Rules {
		policyName := fmt.Sprintf(RBACPolicyNameFormat, backend.Namespace, backend.Name, i)
		var principalIDs []*rbacconfigv3.Principal

		// Build source principals
		allSources := append(rule.Source.Identities, rule.Source.ServiceAccounts...)

		if len(allSources) > 0 {
			var sourcePrincipals []*rbacconfigv3.Principal
			for _, source := range allSources {
				sourcePrincipal := &rbacconfigv3.Principal{
					Identifier: &rbacconfigv3.Principal_Header{
						Header: &routev3.HeaderMatcher{
							Name: "x-user-role",
							HeaderMatchSpecifier: &routev3.HeaderMatcher_StringMatch{
								StringMatch: &matcherv3.StringMatcher{
									MatchPattern: &matcherv3.StringMatcher_Exact{Exact: source},
								},
							},
						},
					},
				}
				sourcePrincipals = append(sourcePrincipals, sourcePrincipal)
			}
			principalIDs = append(principalIDs, &rbacconfigv3.Principal{
				Identifier: &rbacconfigv3.Principal_OrIds{
					OrIds: &rbacconfigv3.Principal_Set{Ids: sourcePrincipals},
				},
			})
		}

		// Build permissions based on tools if specified
		var permissions []*rbacconfigv3.Permission
		if len(rule.Tools) > 0 {
			var toolValueMatchers []*matcherv3.ValueMatcher
			for _, tool := range rule.Tools {
				toolValueMatchers = append(toolValueMatchers, &matcherv3.ValueMatcher{
					MatchPattern: &matcherv3.ValueMatcher_StringMatch{
						StringMatch: &matcherv3.StringMatcher{
							MatchPattern: &matcherv3.StringMatcher_Exact{Exact: tool},
						},
					},
				})
			}

			var toolsMatcher *matcherv3.ValueMatcher
			if len(toolValueMatchers) == 1 {
				toolsMatcher = toolValueMatchers[0]
			} else {
				toolsMatcher = &matcherv3.ValueMatcher{
					MatchPattern: &matcherv3.ValueMatcher_OrMatch{OrMatch: &matcherv3.OrMatcher{ValueMatchers: toolValueMatchers}},
				}
			}

			permissions = append(permissions, &rbacconfigv3.Permission{
				Rule: &rbacconfigv3.Permission_AndRules{
					AndRules: &rbacconfigv3.Permission_Set{
						Rules: []*rbacconfigv3.Permission{
							{
								Rule: &rbacconfigv3.Permission_SourcedMetadata{
									SourcedMetadata: &rbacconfigv3.SourcedMetadata{
										MetadataMatcher: &matcherv3.MetadataMatcher{
											Filter: mcpProxyFilterName,
											Path:   []*matcherv3.MetadataMatcher_PathSegment{{Segment: &matcherv3.MetadataMatcher_PathSegment_Key{Key: "method"}}},
											Value:  &matcherv3.ValueMatcher{MatchPattern: &matcherv3.ValueMatcher_StringMatch{StringMatch: &matcherv3.StringMatcher{MatchPattern: &matcherv3.StringMatcher_Exact{Exact: toolsCallMethod}}}},
										},
									},
								},
							},
							{
								Rule: &rbacconfigv3.Permission_SourcedMetadata{
									SourcedMetadata: &rbacconfigv3.SourcedMetadata{
										MetadataMatcher: &matcherv3.MetadataMatcher{
											Filter: mcpProxyFilterName,
											Path:   []*matcherv3.MetadataMatcher_PathSegment{{Segment: &matcherv3.MetadataMatcher_PathSegment_Key{Key: "params"}}, {Segment: &matcherv3.MetadataMatcher_PathSegment_Key{Key: "name"}}},
											Value:  toolsMatcher,
										},
									},
								},
							},
						},
					},
				},
			})
		}

		policies[policyName] = &rbacconfigv3.Policy{
			Principals:  principalIDs,
			Permissions: permissions,
		}
	}
	return policies
}

// buildAllowMCPSessionClosePolicy creates the RBAC policy that allows agents to close MCP sessions.
func buildAllowMCPSessionClosePolicy() *rbacconfigv3.Policy {
	return &rbacconfigv3.Policy{
		Principals: []*rbacconfigv3.Principal{
			{
				Identifier: &rbacconfigv3.Principal_AndIds{
					AndIds: &rbacconfigv3.Principal_Set{
						Ids: []*rbacconfigv3.Principal{
							{ // Condition 1: The HTTP method must be DELETE
								Identifier: &rbacconfigv3.Principal_Header{
									Header: &routev3.HeaderMatcher{
										Name: ":method",
										HeaderMatchSpecifier: &routev3.HeaderMatcher_StringMatch{
											StringMatch: &matcherv3.StringMatcher{
												MatchPattern: &matcherv3.StringMatcher_Exact{Exact: "DELETE"},
											},
										},
									},
								},
							},
							{ // Condition 2: The 'mcp-session-id' header must exist
								Identifier: &rbacconfigv3.Principal_Header{
									Header: &routev3.HeaderMatcher{Name: mcpSessionIDHeader, HeaderMatchSpecifier: &routev3.HeaderMatcher_PresentMatch{PresentMatch: true}},
								},
							},
						},
					},
				},
			},
		},
		Permissions: []*rbacconfigv3.Permission{
			{
				// If the principal (the request's identity) matches, allow it.
				Rule: &rbacconfigv3.Permission_Any{
					Any: true,
				},
			},
		},
	}
}

// buildAllowAnyoneToInitializeAndListToolsPolicy creates the RBAC policy that allows anyone to
// initialize a session and list available tools.
func buildAllowAnyoneToInitializeAndListToolsPolicy() *rbacconfigv3.Policy {
	return &rbacconfigv3.Policy{
		Principals: []*rbacconfigv3.Principal{
			{
				Identifier: &rbacconfigv3.Principal_Any{
					Any: true,
				},
			},
		},
		Permissions: []*rbacconfigv3.Permission{
			{
				Rule: &rbacconfigv3.Permission_OrRules{
					OrRules: &rbacconfigv3.Permission_Set{
						Rules: []*rbacconfigv3.Permission{
							{
								Rule: &rbacconfigv3.Permission_SourcedMetadata{
									SourcedMetadata: &rbacconfigv3.SourcedMetadata{
										MetadataMatcher: &matcherv3.MetadataMatcher{
											Filter: mcpProxyFilterName,
											Path:   []*matcherv3.MetadataMatcher_PathSegment{{Segment: &matcherv3.MetadataMatcher_PathSegment_Key{Key: "method"}}},
											Value: &matcherv3.ValueMatcher{
												MatchPattern: &matcherv3.ValueMatcher_OrMatch{
													OrMatch: &matcherv3.OrMatcher{
														ValueMatchers: []*matcherv3.ValueMatcher{
															{MatchPattern: &matcherv3.ValueMatcher_StringMatch{StringMatch: &matcherv3.StringMatcher{MatchPattern: &matcherv3.StringMatcher_Exact{Exact: initializeMethod}}}},
															{MatchPattern: &matcherv3.ValueMatcher_StringMatch{StringMatch: &matcherv3.StringMatcher{MatchPattern: &matcherv3.StringMatcher_Exact{Exact: initializedMethod}}}},
															{MatchPattern: &matcherv3.ValueMatcher_StringMatch{StringMatch: &matcherv3.StringMatcher{MatchPattern: &matcherv3.StringMatcher_Exact{Exact: toolsListMethod}}}},
														},
													},
												},
											},
										},
									},
								},
							},
							{
								// This rule explicitly allows GET requests. In the MCP protocol, after an initial POST handshake,
								// a long-lived GET request is established to receive server-sent events. Without this rule,
								// the RBAC filter (which primarily inspects POST request bodies via SourcedMetadata)
								// would implicitly deny these GET requests, leading to a 403 Forbidden error.
								Rule: &rbacconfigv3.Permission_Header{
									Header: &routev3.HeaderMatcher{
										Name: ":method",
										HeaderMatchSpecifier: &routev3.HeaderMatcher_StringMatch{
											StringMatch: &matcherv3.StringMatcher{
												MatchPattern: &matcherv3.StringMatcher_Exact{Exact: "GET"},
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
}
