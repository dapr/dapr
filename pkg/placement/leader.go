package placement

import (
	"time"

	"github.com/dapr/dapr/pkg/placement/raft"
)

func (p *Service) faultyHostDetectionLoop() {
	detectionTick := time.Tick(500 * time.Millisecond)
	reqCount := 0

	for {
		if !p.raftNode.IsLeader() {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		select {
		case op := <-p.desseminateCh:
			updateRequired := false
			switch op {
			case bufferredOperation:
				reqCount++
				if reqCount > 10 {
					updateRequired = true
					reqCount = 0
				}
			case flushOperation:
				updateRequired = true
			}

			if updateRequired {
				p.PerformTablesUpdate(p.streamConns, placementOptions{incrementGeneration: true})
			}

		case t := <-detectionTick:
			m := p.raftNode.FSM().State().Members
			tableUpdateRequired := false
			for _, v := range m {
				if t.Sub(v.UpdatedAt) < 10*time.Second {
					continue
				}
				_, err := p.raftNode.ApplyCommand(raft.MemberRemove, raft.DaprHostMember{
					Name: v.Name,
				})
				if err != nil {
					log.Debugf("fail to apply command: %v", err)
				}
				tableUpdateRequired = true
			}

			if tableUpdateRequired {
				p.desseminateCh <- flushOperation
			}
		}
	}
}
