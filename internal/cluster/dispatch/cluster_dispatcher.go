package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/cyoda-platform/cyoda-go/internal/common"
	internalgrpc "github.com/cyoda-platform/cyoda-go/internal/grpc"
	"github.com/cyoda-platform/cyoda-go/internal/spi"
)

const (
	gossipPollInterval = 200 * time.Millisecond
)

// ClusterDispatcher implements spi.ExternalProcessingService with cluster-aware
// dispatch. It tries the local node first, and if no local calculation member
// matches the required tags, it looks up peers via gossip and forwards the
// request to a peer that advertises the tag.
type ClusterDispatcher struct {
	local       spi.ExternalProcessingService
	registry    spi.NodeRegistry
	selfNodeID  string
	selector    PeerSelector
	forwarder   DispatchForwarder
	waitTimeout time.Duration
}

// NewClusterDispatcher constructs a ClusterDispatcher.
func NewClusterDispatcher(
	local spi.ExternalProcessingService,
	registry spi.NodeRegistry,
	selfNodeID string,
	selector PeerSelector,
	forwarder DispatchForwarder,
	waitTimeout time.Duration,
) *ClusterDispatcher {
	return &ClusterDispatcher{
		local:       local,
		registry:    registry,
		selfNodeID:  selfNodeID,
		selector:    selector,
		forwarder:   forwarder,
		waitTimeout: waitTimeout,
	}
}

// DispatchProcessor tries the local node first. If the local node has no matching
// calculation member, it looks up peers via gossip and forwards the request.
func (d *ClusterDispatcher) DispatchProcessor(ctx context.Context, entity *common.Entity, processor common.ProcessorDefinition, workflowName string, transitionName string, txID string) (*common.Entity, error) {
	// Try local first.
	result, err := d.local.DispatchProcessor(ctx, entity, processor, workflowName, transitionName, txID)
	if err == nil {
		return result, nil
	}
	if !isNoMatchingMember(err) {
		return nil, err
	}

	tags := processor.Config.CalculationNodesTags
	uc := common.MustGetUserContext(ctx)
	tenantID := string(uc.Tenant.ID)

	slog.Debug("local dispatch found no member, looking up cluster peers",
		"pkg", "dispatch", "tenantID", tenantID, "tags", tags)

	req := d.buildProcessorRequest(entity, processor, workflowName, transitionName, txID, uc, tags)

	peer, err := d.findPeerWithPolling(ctx, tenantID, tags)
	if err != nil {
		return nil, err
	}

	slog.Debug("forwarding processor to peer",
		"pkg", "dispatch", "peer", peer.NodeID, "addr", peer.Addr, "tags", tags)

	resp, err := d.forwarder.ForwardProcessor(ctx, peer.Addr, req)
	if err != nil {
		return nil, fmt.Errorf("%s: forward to %s: %w", common.ErrCodeDispatchForwardFailed, peer.NodeID, err)
	}
	if !resp.Success {
		return nil, fmt.Errorf("peer %s dispatch failed: %s", peer.NodeID, resp.Error)
	}
	for _, w := range resp.Warnings {
		common.AddWarning(ctx, w)
	}

	updated := &common.Entity{
		Meta: entity.Meta,
		Data: resp.EntityData,
	}
	return updated, nil
}

// DispatchCriteria tries the local node first. If the local node has no matching
// calculation member, it looks up peers via gossip and forwards the request.
func (d *ClusterDispatcher) DispatchCriteria(ctx context.Context, entity *common.Entity, criterion json.RawMessage, target string, workflowName string, transitionName string, processorName string, txID string) (bool, error) {
	// Try local first.
	matches, err := d.local.DispatchCriteria(ctx, entity, criterion, target, workflowName, transitionName, processorName, txID)
	if err == nil {
		return matches, nil
	}
	if !isNoMatchingMember(err) {
		return false, err
	}

	tags := extractCriteriaTags(criterion)
	uc := common.MustGetUserContext(ctx)
	tenantID := string(uc.Tenant.ID)

	slog.Debug("local criteria dispatch found no member, looking up cluster peers",
		"pkg", "dispatch", "tenantID", tenantID, "tags", tags)

	req := d.buildCriteriaRequest(entity, criterion, target, workflowName, transitionName, processorName, txID, uc, tags)

	peer, err := d.findPeerWithPolling(ctx, tenantID, tags)
	if err != nil {
		return false, err
	}

	slog.Debug("forwarding criteria to peer",
		"pkg", "dispatch", "peer", peer.NodeID, "addr", peer.Addr, "tags", tags)

	resp, err := d.forwarder.ForwardCriteria(ctx, peer.Addr, req)
	if err != nil {
		return false, fmt.Errorf("%s: forward to %s: %w", common.ErrCodeDispatchForwardFailed, peer.NodeID, err)
	}
	if !resp.Success {
		return false, fmt.Errorf("peer %s criteria dispatch failed: %s", peer.NodeID, resp.Error)
	}
	for _, w := range resp.Warnings {
		common.AddWarning(ctx, w)
	}

	return resp.Matches, nil
}

// findPeerWithPolling polls the gossip registry for a peer with matching tags,
// retrying every gossipPollInterval up to waitTimeout.
func (d *ClusterDispatcher) findPeerWithPolling(ctx context.Context, tenantID string, tags string) (spi.NodeInfo, error) {
	deadline := time.After(d.waitTimeout)
	ticker := time.NewTicker(gossipPollInterval)
	defer ticker.Stop()

	// Try immediately first, then poll.
	for {
		peer, found := d.findPeer(ctx, tenantID, tags)
		if found {
			return peer, nil
		}

		select {
		case <-deadline:
			return spi.NodeInfo{}, fmt.Errorf("%s: no peer with tags %q for tenant %s after %v",
				common.ErrCodeNoComputeMemberForTag, tags, tenantID, d.waitTimeout)
		case <-ctx.Done():
			return spi.NodeInfo{}, ctx.Err()
		case <-ticker.C:
			// Continue polling.
		}
	}
}

// findPeer queries the registry and returns a peer (not self, alive) whose tags
// for the given tenant overlap with the required tags.
func (d *ClusterDispatcher) findPeer(ctx context.Context, tenantID string, tags string) (spi.NodeInfo, bool) {
	nodes, err := d.registry.List(ctx)
	if err != nil {
		slog.Debug("failed to list cluster nodes", "pkg", "dispatch", "err", err)
		return spi.NodeInfo{}, false
	}

	var candidates []spi.NodeInfo
	for _, n := range nodes {
		if n.NodeID == d.selfNodeID {
			continue
		}
		if !n.Alive {
			continue
		}
		if common.TagsOverlap(n.Tags[tenantID], tags) {
			candidates = append(candidates, n)
		}
	}

	if len(candidates) == 0 {
		return spi.NodeInfo{}, false
	}

	peer, err := d.selector.Select(candidates)
	if err != nil {
		slog.Debug("peer selection failed", "pkg", "dispatch", "err", err)
		return spi.NodeInfo{}, false
	}
	return peer, true
}

// buildProcessorRequest constructs the cross-node dispatch request for a processor.
func (d *ClusterDispatcher) buildProcessorRequest(entity *common.Entity, processor common.ProcessorDefinition, workflowName, transitionName, txID string, uc *common.UserContext, tags string) *DispatchProcessorRequest {
	return &DispatchProcessorRequest{
		Entity:         json.RawMessage(entity.Data),
		EntityMeta:     entity.Meta,
		Processor:      processor,
		WorkflowName:   workflowName,
		TransitionName: transitionName,
		TxID:           txID,
		TenantID:       string(uc.Tenant.ID),
		Tags:           tags,
		UserID:         uc.UserID,
		Roles:          uc.Roles,
	}
}

// buildCriteriaRequest constructs the cross-node dispatch request for criteria.
func (d *ClusterDispatcher) buildCriteriaRequest(entity *common.Entity, criterion json.RawMessage, target, workflowName, transitionName, processorName, txID string, uc *common.UserContext, tags string) *DispatchCriteriaRequest {
	return &DispatchCriteriaRequest{
		Entity:         json.RawMessage(entity.Data),
		EntityMeta:     entity.Meta,
		Criterion:      criterion,
		Target:         target,
		WorkflowName:   workflowName,
		TransitionName: transitionName,
		ProcessorName:  processorName,
		TxID:           txID,
		TenantID:       string(uc.Tenant.ID),
		Tags:           tags,
		UserID:         uc.UserID,
		Roles:          uc.Roles,
	}
}

// isNoMatchingMember returns true if the error indicates no local calculation
// member was found (tests against the sentinel from ProcessorDispatcher).
func isNoMatchingMember(err error) bool {
	return errors.Is(err, internalgrpc.ErrNoMatchingMember)
}

// extractCriteriaTags extracts the calculationNodesTags from a criterion JSON.
// The expected structure is: {"type":"function","function":{"config":{"calculationNodesTags":"..."}}}
func extractCriteriaTags(criterion json.RawMessage) string {
	var parsed struct {
		Function struct {
			Config struct {
				CalculationNodesTags string `json:"calculationNodesTags"`
			} `json:"config"`
		} `json:"function"`
	}
	if err := json.Unmarshal(criterion, &parsed); err != nil {
		return ""
	}
	return parsed.Function.Config.CalculationNodesTags
}
