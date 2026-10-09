package sim

import (
	"context"
	"fmt"

	"github.com/mmqz/netops-lab/internal/repo"
)

// TopologyNode is one node of the workspace topology payload: a virtual
// device with its interfaces (sorted by name).
type TopologyNode struct {
	ID         string              `json:"id"`
	Code       string              `json:"code"`
	Name       string              `json:"name"`
	Status     string              `json:"status"`
	Interfaces []TopologyInterface `json:"interfaces"`
}

// TopologyInterface is one interface of a topology node. IPv4Addr "" and
// IPv4Prefix -1 mean "unconfigured" (the payload representation differs
// deliberately from the state hash, which renders nulls — §7 vs §3).
type TopologyInterface struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	IPv4Addr   string `json:"ipv4Addr"`
	IPv4Prefix int    `json:"ipv4Prefix"`
}

// TopologyEdge is one link of the workspace topology payload, resolved to
// both endpoint devices (ids and codes) and interfaces.
type TopologyEdge struct {
	ID           string `json:"id"`
	AInterfaceID string `json:"aInterfaceId"`
	ADeviceID    string `json:"aDeviceId"`
	ADeviceCode  string `json:"aDeviceCode"`
	ZInterfaceID string `json:"zInterfaceId"`
	ZDeviceID    string `json:"zDeviceId"`
	ZDeviceCode  string `json:"zDeviceCode"`
	Status       string `json:"status"`
	Label        string `json:"label"`
}

// Topology is the full workspace topology response: nodes sorted by code,
// interfaces by name, edges by numeric id (creation order).
type Topology struct {
	WorkspaceID   string         `json:"workspaceId"`
	WorkspaceHash string         `json:"workspaceHash"`
	Nodes         []TopologyNode `json:"nodes"`
	Edges         []TopologyEdge `json:"edges"`
}

// Topology returns the workspace topology payload and the canonical state
// hash, read in one consistent snapshot (a read-only transaction).
func (s *Service) Topology(ctx context.Context, workspaceID string) (Topology, error) {
	var topo Topology
	err := s.db.Do(ctx, func(tx *repo.Tx) error {
		if _, err := tx.Workspaces.GetByID(ctx, workspaceID); err != nil {
			return fmt.Errorf("sim: workspace %s: %w", workspaceID, err)
		}
		state, err := loadState(ctx, storeOfTx(tx), workspaceID)
		if err != nil {
			return err
		}
		hash, err := state.hash()
		if err != nil {
			return err
		}
		topo = state.topology(workspaceID, hash)
		return nil
	})
	if err != nil {
		return Topology{}, err
	}
	return topo, nil
}

// topology renders the API payload from the canonical state.
func (s *simState) topology(workspaceID, hash string) Topology {
	topo := Topology{
		WorkspaceID:   workspaceID,
		WorkspaceHash: hash,
		Nodes:         make([]TopologyNode, 0, len(s.devices)),
		Edges:         make([]TopologyEdge, 0, len(s.links)),
	}
	for _, d := range s.devices {
		node := TopologyNode{
			ID:         d.Dev.ID,
			Code:       d.Dev.Code,
			Name:       d.Dev.Name,
			Status:     d.Dev.Status.String(),
			Interfaces: make([]TopologyInterface, 0, len(d.Interfaces)),
		}
		for _, i := range d.Interfaces {
			node.Interfaces = append(node.Interfaces, TopologyInterface{
				ID:         i.ID,
				Name:       i.Name,
				Status:     i.Status.String(),
				IPv4Addr:   i.IPv4Addr,
				IPv4Prefix: i.IPv4Prefix,
			})
		}
		topo.Nodes = append(topo.Nodes, node)
	}
	for _, l := range s.links {
		a, z := s.linkEnds(l)
		topo.Edges = append(topo.Edges, TopologyEdge{
			ID:           l.ID,
			AInterfaceID: l.AInterfaceID,
			ADeviceID:    a.deviceID,
			ADeviceCode:  a.deviceCode,
			ZInterfaceID: l.ZInterfaceID,
			ZDeviceID:    z.deviceID,
			ZDeviceCode:  z.deviceCode,
			Status:       l.Status.String(),
			Label:        l.Label,
		})
	}
	return topo
}

// WorkspaceHash returns the canonical workspace state hash of
// PROJECT_BRIEF_P2 §3 (sha256 hex over the canonical state JSON).
func (s *Service) WorkspaceHash(ctx context.Context, workspaceID string) (string, error) {
	if _, err := s.mustWorkspace(ctx, workspaceID); err != nil {
		return "", err
	}
	var hash string
	err := s.db.Do(ctx, func(tx *repo.Tx) error {
		state, err := loadState(ctx, storeOfTx(tx), workspaceID)
		if err != nil {
			return err
		}
		hash, err = state.hash()
		return err
	})
	if err != nil {
		return "", err
	}
	return hash, nil
}
