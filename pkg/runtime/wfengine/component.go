package wfengine

import (
	"context"
	"fmt"

	"github.com/dapr/components-contrib/workflows"
	componentsV1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/kit/logger"
	"github.com/microsoft/durabletask-go/api"
	"github.com/microsoft/durabletask-go/backend"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var ComponentDefinition = componentsV1alpha1.Component{
	TypeMeta: metav1.TypeMeta{
		Kind: "Component",
	},
	ObjectMeta: metav1.ObjectMeta{
		Name: "dapr",
	},
	Spec: componentsV1alpha1.ComponentSpec{
		Type:     "workflow.dapr",
		Version:  "v1",
		Metadata: []componentsV1alpha1.MetadataItem{},
	},
}

func BuiltinWorkflowFactory(engine *WorkflowEngine) func(logger.Logger) workflows.Workflow {
	return func(logger logger.Logger) workflows.Workflow {
		return &WorkflowEngineComponent{
			logger: logger,
			engine: engine,
		}
	}
}

type WorkflowEngineComponent struct {
	workflows.Workflow
	logger logger.Logger
	engine *WorkflowEngine
}

func (c *WorkflowEngineComponent) Init(metadata workflows.Metadata) error {
	c.logger.Infof("Initializing Dapr workflow engine")
	return nil
}

func (c *WorkflowEngineComponent) Start(ctx context.Context, req *workflows.StartRequest) (*workflows.WorkflowReference, error) {
	if req.WorkflowReference.InstanceID == "" {
		return nil, fmt.Errorf("no workflow instance ID supplied")
	}

	wi := &backend.OrchestrationWorkItem{
		InstanceID: api.InstanceID(req.WorkflowReference.InstanceID),
	}

	if err := c.engine.backend.ScheduleWorkflow(ctx, wi); err != nil {
		c.logger.Warnf("Unable to schedule workflow: %v", err)
		return nil, fmt.Errorf("unable to start workflow: %v", err)
	}

	return &workflows.WorkflowReference{
		InstanceID: req.WorkflowReference.InstanceID,
	}, nil
}

func (c *WorkflowEngineComponent) Terminate(ctx context.Context, req *workflows.WorkflowReference) error {
	if req.InstanceID == "" {
		return fmt.Errorf("no workflow instance ID supplied")
	}

	wi := &backend.OrchestrationWorkItem{
		InstanceID: api.InstanceID(req.InstanceID),
	}

	if err := c.engine.backend.AbandonOrchestrationWorkItem(ctx, wi); err != nil {
		return fmt.Errorf("failed to terminate workflow %s: %v", req.InstanceID, err)
	}

	return nil
}

func (c *WorkflowEngineComponent) Get(ctx context.Context, req *workflows.WorkflowReference) (*workflows.StateResponse, error) {
	if req.InstanceID == "" {
		return nil, fmt.Errorf("no workflow instance ID supplied")
	}

	workId := api.InstanceID(req.InstanceID)

	if metadata, err := c.engine.backend.GetOrchestrationMetadata(ctx, workId); err != nil {
		return nil, fmt.Errorf("failed to get workflow metadata for %s: %v", req.InstanceID, err)
	} else {
		return &workflows.StateResponse{
			WFInfo: workflows.WorkflowReference{
				InstanceID: req.InstanceID,
			},
			StartTime: metadata.CreatedAt.Local().String(),
		}, nil
	}
}
