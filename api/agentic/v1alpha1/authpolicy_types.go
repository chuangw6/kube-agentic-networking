/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// IMPORTANT: Run "make generate-all" to regenerate code after modifying this file

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// AuthPolicySpec defines the desired state of AuthPolicy.
type AuthPolicySpec struct {
	// TargetRef specifies the target of the AuthPolicy.
	// Currently, only Backend can be used as a target.
	// +required
	TargetRef gwapiv1.LocalPolicyTargetReference `json:"targetRef"`

	// Rules defines a list of rules to be applied to the target.
	// +required
	Rules []AuthRule `json:"rules"`

	// Action specifies the action to take when a request matches the rules.
	// +kubebuilder:validation:Required
	// +required
	Action AuthPolicyAction `json:"action"`
}

// AuthPolicyAction specifies the action to take.
// Currently, the only supported action is ALLOW.
// +kubebuilder:validation:Enum=ALLOW
type AuthPolicyAction string

const (
	// ActionAllow allows requests that match the policy rules.
	ActionAllow AuthPolicyAction = "ALLOW"
)

// AuthRule specifies an authorization rule for the targeted backend.
// When the action is ALLOW,
//   - requests from Source are permitted to access the listed Tools.
//   - If the tool list is empty, the rule denies access to all tools from Source.
type AuthRule struct {
	// Source specifies the source of the request.
	// +required
	Source Source `json:"source"`

	// Tools specifies a list of tools.
	// +optional
	Tools []string `json:"tools,omitempty"`
}

// Source specifies the source of a request.
// This struct is same as the Source struct defined in https://github.com/kubernetes-sigs/gateway-api/blob/950c6639afd099b7bba4236f8b894ae4b891d26a/geps/gep-3779/index.md#api-design.
//
// At least one field may be set. If multiple fields are set,
// a request matches this Source if it matches
// **any** of the specified criteria (logical OR across fields).
//
// For example, if both `Identities` and `ServiceAccounts` are provided,
// the rule matches a request if either:
// - the request's identity is in `Identities`
// - OR the request's Serviceaccount matches an entry in `ServiceAccounts`.
//
// Each list within the fields (e.g. `Identities`) is itself an OR list.
//
// If this struct is omitted in a rule, it matches any source.
//
// <gateway:util:excludeFromCRD> NOTE: In the future, if there’s a need to express more complex
// logical conditions (e.g. requiring a request to match multiple
// criteria simultaneously—logical AND), we may evolve this API
// to support richer match expressions or logical operators. </gateway:util:excludeFromCRD>
type Source struct {

	// Identities specifies a list of identities that are matched by this rule.
	// A request's identity must be present in this list to match the rule.
	//
	// Identities must be specified as SPIFFE-formatted URIs following the pattern:
	//   spiffe://<trust_domain>/<workload-identifier>
	//
	// While the exact workload identifier structure is implementation-specific,
	// implementations are encouraged to follow the convention of
	// `spiffe://<trust_domain>/ns/<namespace>/sa/<serviceaccount>`
	// when representing Kubernetes workload identities.
	//
	// +optional
	Identities []string `json:"identities,omitempty"`

	// ServiceAccounts specifies a list of Kubernetes Service Accounts that are
	// matched by this rule. A request originating from a pod associated with
	// one of these Serviceaccounts will match the rule.
	//
	// Values must be in one of the following formats:
	//   - "<namespace>/<serviceaccount-name>": A specific Serviceaccount in a namespace.
	//   - "<namespace>/*": All Serviceaccounts in the given namespace.
	//   - "<serviceaccount-name>": a Serviceaccount in the same namespace as the policy.
	//
	// Use of "*" alone (i.e., all Serviceaccounts in all namespaces) is not allowed.
	// To select all Serviceaccounts in the current namespace, use "<namespace>/*" explicitly.
	//
	// Example:
	//   - "default/bookstore" → Matches Serviceaccount "bookstore" in namespace "default"
	//   - "payments/*" → Matches any Serviceaccount in namespace "payments"
	//   - "frontend" → Matches "frontend" Serviceaccount in the same namespace as the policy
	//
	// The ServiceAccounts listed here are expected to exist within the same
	// trust domain as the targeted workload, which in many environments means
	// the same Kubernetes cluster. Cross-cluster or cross-trust-domain access
	// should instead be expressed using the `Identities` field.
	//
	// +optional
	ServiceAccounts []string `json:"serviceAccounts,omitempty"`
}

// AuthPolicyStatus defines the observed state of AuthPolicy.
type AuthPolicyStatus struct {
	// For Kubernetes API conventions, see:
	// https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties

	// conditions represent the current state of the AuthPolicy resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// Standard condition types include:
	// - "Available": the resource is fully functional
	// - "Progressing": the resource is being created or updated
	// - "Degraded": the resource failed to reach or maintain its desired state
	//
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +genclient
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// AuthPolicy is the Schema for the authpolicies API.
type AuthPolicy struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec defines the desired state of AuthPolicy.
	// +required
	Spec AuthPolicySpec `json:"spec"`

	// status defines the observed state of AuthPolicy.
	// +optional
	Status AuthPolicyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AuthPolicyList contains a list of AuthPolicy.
type AuthPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	// metadata is a standard list metadata.
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AuthPolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AuthPolicy{}, &AuthPolicyList{})
}
