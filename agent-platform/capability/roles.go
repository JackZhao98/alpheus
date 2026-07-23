package capability

import (
	"fmt"
	"sort"
)

// AgentRoleID identifies a versioned Cortex investment-research responsibility.
// Intent remains a system step and Decision Desk remains the final synthesizer;
// neither is registered as a Specialist.
type AgentRoleID string

const (
	RoleMarketScout      AgentRoleID = "market_scout"
	RoleFundamentalScout AgentRoleID = "fundamental_scout"
	RoleOptionsScout     AgentRoleID = "options_scout"
	RolePositionManager  AgentRoleID = "position_manager"
	RoleCatalystScout    AgentRoleID = "catalyst_scout"
	RoleDiscoveryScout   AgentRoleID = "discovery_scout"
)

type AgentRoleDescriptor struct {
	ID                   AgentRoleID
	Revision             uint16
	Purpose              string
	ToolCategories       []string
	OutputContract       string
	AllowedHandoffTarget []string
	MaxToolCalls         uint16
	Effect               string
}

var agentRoleCatalog = []AgentRoleDescriptor{
	{ID: RoleMarketScout, Revision: 1, Purpose: "Interpret bounded market, index, price, liquidity, and technical evidence.", ToolCategories: []string{"market"}, OutputContract: "specialist_memo_v1", AllowedHandoffTarget: []string{"decision_desk"}, MaxToolCalls: 1, Effect: "none"},
	{ID: RoleFundamentalScout, Revision: 1, Purpose: "Interpret bounded company fundamentals, valuation, and financial-statement evidence.", ToolCategories: []string{"fundamentals"}, OutputContract: "specialist_memo_v1", AllowedHandoffTarget: []string{"decision_desk"}, MaxToolCalls: 1, Effect: "none"},
	{ID: RoleOptionsScout, Revision: 1, Purpose: "Interpret bounded option-chain, contract, quote, volatility, and GEX evidence.", ToolCategories: []string{"options", "market_options"}, OutputContract: "specialist_memo_v1", AllowedHandoffTarget: []string{"decision_desk"}, MaxToolCalls: 1, Effect: "none"},
	{ID: RolePositionManager, Revision: 1, Purpose: "Interpret canonical facts for existing positions, orders, lots, eligibility, and realized P&L.", ToolCategories: []string{"portfolio"}, OutputContract: "specialist_memo_v1", AllowedHandoffTarget: []string{"decision_desk"}, MaxToolCalls: 1, Effect: "none"},
	{ID: RoleCatalystScout, Revision: 1, Purpose: "Interpret bounded earnings and catalyst timing evidence.", ToolCategories: []string{"catalyst"}, OutputContract: "specialist_memo_v1", AllowedHandoffTarget: []string{"decision_desk"}, MaxToolCalls: 1, Effect: "none"},
	{ID: RoleDiscoveryScout, Revision: 1, Purpose: "Resolve bounded discovery, scanner, watchlist, search, and explicit public-web evidence.", ToolCategories: []string{"discovery", "web"}, OutputContract: "specialist_memo_v1", AllowedHandoffTarget: []string{"decision_desk"}, MaxToolCalls: 1, Effect: "none"},
}

func AgentRoles() []AgentRoleDescriptor {
	roles := make([]AgentRoleDescriptor, len(agentRoleCatalog))
	for index, role := range agentRoleCatalog {
		roles[index] = role
		roles[index].ToolCategories = append([]string(nil), role.ToolCategories...)
		roles[index].AllowedHandoffTarget = append([]string(nil), role.AllowedHandoffTarget...)
	}
	return roles
}

func LookupAgentRole(id AgentRoleID) (AgentRoleDescriptor, bool) {
	for _, role := range agentRoleCatalog {
		if role.ID == id {
			role.ToolCategories = append([]string(nil), role.ToolCategories...)
			role.AllowedHandoffTarget = append([]string(nil), role.AllowedHandoffTarget...)
			return role, true
		}
	}
	return AgentRoleDescriptor{}, false
}

func AgentRoleIDs() []string {
	ids := make([]string, 0, len(agentRoleCatalog))
	for _, role := range agentRoleCatalog {
		ids = append(ids, string(role.ID))
	}
	sort.Strings(ids)
	return ids
}

func AgentRolesForTool(toolID ToolID) []AgentRoleID {
	tool, ok := LookupTool(toolID)
	if !ok {
		return nil
	}
	var result []AgentRoleID
	for _, role := range agentRoleCatalog {
		for _, category := range role.ToolCategories {
			if category == tool.Category {
				result = append(result, role.ID)
				break
			}
		}
	}
	return result
}

func ValidateAgentRoleCatalog() error {
	seen := make(map[AgentRoleID]struct{}, len(agentRoleCatalog))
	for _, role := range agentRoleCatalog {
		if role.ID == "" || role.Revision != 1 || role.Purpose == "" || len(role.ToolCategories) == 0 ||
			role.OutputContract != "specialist_memo_v1" || len(role.AllowedHandoffTarget) != 1 ||
			role.AllowedHandoffTarget[0] != "decision_desk" || role.MaxToolCalls != 1 || role.Effect != "none" {
			return fmt.Errorf("invalid Agent role %q", role.ID)
		}
		if _, exists := seen[role.ID]; exists {
			return fmt.Errorf("duplicate Agent role %q", role.ID)
		}
		seen[role.ID] = struct{}{}
	}
	if len(seen) != 6 {
		return fmt.Errorf("unexpected Agent role count")
	}
	return nil
}
