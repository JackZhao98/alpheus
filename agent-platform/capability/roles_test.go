package capability

import "testing"

func TestAgentRoleCatalogIsBoundedAndCoversReadTools(t *testing.T) {
	if err := ValidateAgentRoleCatalog(); err != nil {
		t.Fatal(err)
	}
	if got := AgentRoleIDs(); len(got) != 6 {
		t.Fatalf("role count=%d", len(got))
	}
	for _, tool := range Catalog() {
		if tool.State != CatalogStateActive || tool.Effect == "read_only_preflight" {
			continue
		}
		roles := AgentRolesForTool(tool.ID)
		if len(roles) != 1 {
			t.Fatalf("active read Tool %s has %d Specialist roles", tool.ID, len(roles))
		}
		role, found := SpecialistRoleForTool(tool.ID)
		if !found || role != roles[0] {
			t.Fatalf("active read Tool %s has ambiguous Specialist owner", tool.ID)
		}
	}
	if roles := AgentRolesForTool("kernel_review_equity_order"); len(roles) != 0 {
		t.Fatalf("preflight Tool leaked to Specialist roles: %v", roles)
	}
	if _, found := SpecialistRoleForTool("kernel_review_option_order"); found {
		t.Fatal("preflight Tool acquired a Specialist owner")
	}
}

func TestAgentRolesReturnsDefensiveCopies(t *testing.T) {
	roles := AgentRoles()
	roles[0].ToolCategories[0] = "mutated"
	roles[0].AllowedHandoffTarget[0] = "user"
	fresh, ok := LookupAgentRole(roles[0].ID)
	if !ok || fresh.ToolCategories[0] == "mutated" || fresh.AllowedHandoffTarget[0] != "decision_desk" {
		t.Fatal("role catalog was mutated by caller")
	}
}
