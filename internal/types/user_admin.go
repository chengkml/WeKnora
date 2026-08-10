package types

// UserWithTenants is the response shape for SystemAdmin user management.
// It includes the user's profile plus every tenant they are a member of.
type UserWithTenants struct {
	ID            string            `json:"id"`
	Username      string            `json:"username"`
	Email         string            `json:"email"`
	IsSystemAdmin bool              `json:"is_system_admin"`
	IsActive      bool              `json:"is_active"`
	CreatedAt     string            `json:"created_at"`
	HomeTenantID  *uint64           `json:"home_tenant_id"`
	Tenants       []UserTenantBrief `json:"tenants"`
}

// UserTenantBrief is a tenant the user belongs to, shown on the user
// management page.
type UserTenantBrief struct {
	TenantID   uint64 `json:"tenant_id"`
	TenantName string `json:"tenant_name"`
	Role       string `json:"role"`
}
