package handlers

import (
	"errors"

	"github.com/Silo-Server/silo-server/internal/noderouting"
	"github.com/Silo-Server/silo-server/internal/playback"
)

func applyInitialRoutingV3(session *playback.Session, decision noderouting.Decision) error {
	if session == nil || !decision.Selected() {
		return errors.New("initial playback route unavailable")
	}
	shape, plan := decision.Shape, decision.Plan
	direct := shape.Workload == noderouting.WorkloadDirectPlay && shape.Execution == noderouting.ExecutionNone
	hlsWorkload := shape.Workload == noderouting.WorkloadVideoTranscode || shape.Workload == noderouting.WorkloadRemux
	local := hlsWorkload && shape.Execution == noderouting.ExecutionAPI && shape.Egress == noderouting.EgressAPI
	remote := hlsWorkload && shape.Execution == noderouting.ExecutionTranscode
	if (!direct && !local && !remote) || (shape.Egress != noderouting.EgressAPI && shape.Egress != noderouting.EgressProxy) {
		return errors.New("unsupported initial playback route")
	}
	if remote {
		if plan.TranscodeNode == nil || plan.TranscodeNode.ID <= 0 || plan.TranscodeNode.URL == "" {
			return errors.New("selected worker identity unavailable")
		}
		session.RoutingExecutionNodeID = plan.TranscodeNode.ID
		session.RoutingExecutionNodeURL = plan.TranscodeNode.URL
		session.TranscodeNodeURL = plan.TranscodeNode.URL
	} else if plan.TranscodeNode != nil {
		return errors.New("unexpected initial worker")
	}
	if shape.Egress == noderouting.EgressProxy {
		if plan.ProxyNode == nil || plan.ProxyNode.ID <= 0 || plan.ProxyNode.URL == "" || plan.ProxyNode.ClientURL() == "" {
			return errors.New("selected proxy identity unavailable")
		}
		session.RoutingEgressNodeID = plan.ProxyNode.ID
		session.RoutingEgressNodeURL = plan.ProxyNode.URL
	} else if plan.ProxyNode != nil {
		return errors.New("unexpected initial proxy")
	}
	session.RoutingWorkload = string(shape.Workload)
	session.RoutingExecution = string(shape.Execution)
	session.RoutingEgress = string(shape.Egress)
	return nil
}
