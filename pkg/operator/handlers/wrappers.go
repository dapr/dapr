package handlers

import (
	appsv1 "k8s.io/api/apps/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ObjectWrapper interface {
	client.Object
	GetMatchLabels() map[string]string
	GetTemplateAnnotations() map[string]string
}

type DeploymentWrapper struct {
	*appsv1.Deployment
}

func (d *DeploymentWrapper) GetMatchLabels() map[string]string {
	return d.Spec.Selector.MatchLabels
}

func (d *DeploymentWrapper) GetTemplateAnnotations() map[string]string {
	return d.Spec.Template.ObjectMeta.Annotations
}

func (d *DeploymentWrapper) GetObject() client.Object {
	return d
}

type StatefulsetWrapper struct {
	*appsv1.StatefulSet
}

func (s *StatefulsetWrapper) GetMatchLabels() map[string]string {
	return s.Spec.Selector.MatchLabels
}

func (s *StatefulsetWrapper) GetTemplateAnnotations() map[string]string {
	return s.Spec.Template.ObjectMeta.Annotations
}

func (s *StatefulsetWrapper) GetObject() client.Object {
	return s
}
