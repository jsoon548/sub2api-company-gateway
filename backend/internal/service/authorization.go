package service

// Capability is a stable authorization decision key. Handlers ask for a
// capability instead of duplicating role comparisons.
type Capability string

const (
	CapabilityAdminAccess            Capability = "admin.access"
	CapabilityManageEmployeeAccounts Capability = "employee_account_lifecycle"
	CapabilityManageAdminIdentities  Capability = "admin_identity_management"
	CapabilityTransferSuperAdmin     Capability = "super_admin_seat_transfer"
	CapabilityRawContentDisclosure   Capability = "raw_content_disclosure"
	CapabilityManageModelCatalog     Capability = "model_catalog_management"
	CapabilityManageAutoConfig       Capability = "auto_configuration_management"
	CapabilityBreakGlassSeatRecovery Capability = "break_glass_seat_recovery"
)

type Authorizer struct{ Role string }

func (a Authorizer) Has(capability Capability) bool {
	switch capability {
	case CapabilityAdminAccess, CapabilityManageEmployeeAccounts, CapabilityManageModelCatalog, CapabilityManageAutoConfig:
		return a.Role == RoleAdmin || a.Role == RoleSuperAdmin
	case CapabilityManageAdminIdentities, CapabilityTransferSuperAdmin, CapabilityRawContentDisclosure:
		return a.Role == RoleSuperAdmin
	case CapabilityBreakGlassSeatRecovery:
		// Break-glass is deployment-controlled and is never an online role capability.
		return false
	default:
		return false
	}
}
