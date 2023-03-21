package handlers

import (
	argov1alpha1 "github.com/argoproj/argo-rollouts/pkg/apis/rollouts/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ObjectWrapper interface {
	GetMatchLabels() map[string]string
	GetTemplateAnnotations() map[string]string
	GetObject() client.Object
}

type DeploymentWrapper struct {
	appsv1.Deployment
}

func (d *DeploymentWrapper) GetMatchLabels() map[string]string {
	return d.Spec.Selector.MatchLabels
}

func (d *DeploymentWrapper) GetTemplateAnnotations() map[string]string {
	return d.Spec.Template.ObjectMeta.Annotations
}

func (d *DeploymentWrapper) GetObject() client.Object {
	return &d.Deployment
}

type StatefulSetWrapper struct {
	appsv1.StatefulSet
}

func (s *StatefulSetWrapper) GetMatchLabels() map[string]string {
	return s.Spec.Selector.MatchLabels
}

func (s *StatefulSetWrapper) GetTemplateAnnotations() map[string]string {
	return s.Spec.Template.ObjectMeta.Annotations
}

func (s *StatefulSetWrapper) GetObject() client.Object {
	return &s.StatefulSet
}

type RolloutWrapper struct {
	argov1alpha1.Rollout
}

func (r *RolloutWrapper) GetMatchLabels() map[string]string {
	return r.Spec.Selector.MatchLabels
}

func (r *RolloutWrapper) GetTemplateAnnotations() map[string]string {
	return r.Spec.Template.ObjectMeta.Annotations
}

func (r *RolloutWrapper) GetObject() client.Object {
	return &r.Rollout
}
