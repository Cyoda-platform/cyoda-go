package grpc

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// ClusterServiceImpl implements contract.ClusterService by wrapping a MemberRegistry.
type ClusterServiceImpl struct {
	registry *MemberRegistry
}

// NewClusterService creates a new ClusterServiceImpl backed by the given registry.
func NewClusterService(registry *MemberRegistry) *ClusterServiceImpl {
	return &ClusterServiceImpl{registry: registry}
}

type memberInfo struct {
	MemberID    string   `json:"memberId"`
	TenantID    string   `json:"tenantId"`
	Tags        []string `json:"tags"`
	ConnectedAt string   `json:"connectedAt"`
	LastSeen    string   `json:"lastSeen"`
	Status      string   `json:"status"`
}

// ListCalculationMembers returns a JSON array of all connected members.
func (s *ClusterServiceImpl) ListCalculationMembers(_ context.Context) ([]byte, error) {
	members := s.registry.List()
	infos := make([]memberInfo, len(members))
	for i, m := range members {
		infos[i] = toMemberInfo(m)
	}
	return json.Marshal(infos)
}

// GetCalculationMember returns the JSON representation of a single member by ID.
func (s *ClusterServiceImpl) GetCalculationMember(_ context.Context, memberID string) ([]byte, error) {
	m := s.registry.Get(memberID)
	if m == nil {
		return nil, fmt.Errorf("member not found: %s", memberID)
	}
	return json.Marshal(toMemberInfo(m))
}

// GetSummary returns a JSON summary of the cluster state.
func (s *ClusterServiceImpl) GetSummary(_ context.Context) ([]byte, error) {
	members := s.registry.List()

	byTenant := make(map[string]int)
	for _, m := range members {
		byTenant[string(m.TenantID)]++
	}

	summary := map[string]any{
		"totalMembers":  len(members),
		"onlineMembers": len(members),
		"byTenant":      byTenant,
	}
	return json.Marshal(summary)
}

func toMemberInfo(m *Member) memberInfo {
	return memberInfo{
		MemberID:    m.ID,
		TenantID:    string(m.TenantID),
		Tags:        m.Tags,
		ConnectedAt: m.ConnectedAt.Format(time.RFC3339),
		LastSeen:    m.LastSeen().Format(time.RFC3339),
		Status:      "online",
	}
}
