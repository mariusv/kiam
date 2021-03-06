package server

import (
	"context"
	"fmt"
	"github.com/uswitch/kiam/pkg/k8s"
	pb "github.com/uswitch/kiam/proto"
	"regexp"
)

type adaptedDecision struct {
	d *pb.Decision
}

func (a *adaptedDecision) IsAllowed() bool {
	return a.d.IsAllowed
}

func (a *adaptedDecision) Explanation() string {
	return a.d.Explanation
}

// Decision reports (with message) as to whether the assume role is permitted.
type Decision interface {
	IsAllowed() bool
	Explanation() string
}

type allowed struct {
}

func (a *allowed) IsAllowed() bool {
	return true
}
func (a *allowed) Explanation() string {
	return ""
}

// AssumeRolePolicy allows for policy to check whether pods can assume the role being
// requested
type AssumeRolePolicy interface {
	IsAllowedAssumeRole(ctx context.Context, roleName, podIP string) (Decision, error)
}

// CompositeAssumeRolePolicy allows multiple policies to be checked
type CompositeAssumeRolePolicy struct {
	policies []AssumeRolePolicy
}

func (p *CompositeAssumeRolePolicy) IsAllowedAssumeRole(ctx context.Context, role, podIP string) (Decision, error) {
	for _, policy := range p.policies {
		decision, err := policy.IsAllowedAssumeRole(ctx, role, podIP)
		if err != nil {
			return nil, err
		}
		if !decision.IsAllowed() {
			return decision, nil
		}
	}

	return &allowed{}, nil
}

// Creates a AssumeRolePolicy that tests all policies pass.
func Policies(p ...AssumeRolePolicy) *CompositeAssumeRolePolicy {
	return &CompositeAssumeRolePolicy{
		policies: p,
	}
}

// RequestingAnnotatedRolePolicy ensures the pod is requesting the role that it's
// currently annotated with.
type RequestingAnnotatedRolePolicy struct {
	pods k8s.PodGetter
}

func NewRequestingAnnotatedRolePolicy(p k8s.PodGetter) *RequestingAnnotatedRolePolicy {
	return &RequestingAnnotatedRolePolicy{pods: p}
}

type forbidden struct {
	requested string
	annotated string
}

func (f *forbidden) IsAllowed() bool {
	return false
}
func (f *forbidden) Explanation() string {
	return fmt.Sprintf("requested '%s' but annotated with '%s', forbidden", f.requested, f.annotated)
}

func (p *RequestingAnnotatedRolePolicy) IsAllowedAssumeRole(ctx context.Context, role, podIP string) (Decision, error) {
	pod, err := p.pods.GetPodByIP(ctx, podIP)
	if err != nil {
		return nil, err
	}

	annotatedRole := k8s.PodRole(pod)
	if annotatedRole != role {
		return &forbidden{requested: role, annotated: annotatedRole}, nil
	}

	return &allowed{}, nil
}

type NamespacePermittedRoleNamePolicy struct {
	namespaces k8s.NamespaceFinder
	pods       k8s.PodGetter
}

func NewNamespacePermittedRoleNamePolicy(n k8s.NamespaceFinder, p k8s.PodGetter) *NamespacePermittedRoleNamePolicy {
	return &NamespacePermittedRoleNamePolicy{namespaces: n, pods: p}
}

type namespacePolicyForbidden struct {
	expression string
	role       string
}

func (f *namespacePolicyForbidden) IsAllowed() bool {
	return false
}

func (f *namespacePolicyForbidden) Explanation() string {
	return fmt.Sprintf("namespace policy expression '%s' forbids role '%s'", f.expression, f.role)
}

func (p *NamespacePermittedRoleNamePolicy) IsAllowedAssumeRole(ctx context.Context, role, podIP string) (Decision, error) {
	pod, err := p.pods.GetPodByIP(ctx, podIP)
	if err != nil {
		return nil, err
	}

	ns, err := p.namespaces.FindNamespace(ctx, pod.GetObjectMeta().GetNamespace())
	if err != nil {
		return nil, err
	}

	expression := ns.GetAnnotations()[k8s.AnnotationName]
	if expression == "" {
		return &namespacePolicyForbidden{expression: "(empty)", role: role}, nil
	}

	re, err := regexp.Compile(expression)
	if err != nil {
		return nil, err
	}

	if !re.MatchString(role) {
		return &namespacePolicyForbidden{expression: expression, role: role}, nil
	}

	return &allowed{}, nil
}
