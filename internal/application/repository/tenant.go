package repository

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrTenantNotFound         = errors.New("tenant not found")
	ErrTenantHasKnowledgeBase = errors.New("tenant has associated knowledge bases")
)

// tenantRepository implements tenant repository interface
type tenantRepository struct {
	db *gorm.DB
}

// NewTenantRepository creates a new tenant repository
func NewTenantRepository(db *gorm.DB) interfaces.TenantRepository {
	return &tenantRepository{db: db}
}

// CreateTenant creates tenant
func (r *tenantRepository) CreateTenant(ctx context.Context, tenant *types.Tenant) error {
	return r.db.WithContext(ctx).Create(tenant).Error
}

// GetTenantByID gets tenant by ID
func (r *tenantRepository) GetTenantByID(ctx context.Context, id uint64) (*types.Tenant, error) {
	var tenant types.Tenant
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&tenant).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrTenantNotFound
		}
		return nil, err
	}
	return &tenant, nil
}

// GetTenantsByIDs batches GetTenantByID with a single IN-list query.
// Returns a map keyed by tenant ID; missing rows are simply absent from
// the map (no error). An empty input slice short-circuits to an empty map
// without hitting the database.
func (r *tenantRepository) GetTenantsByIDs(ctx context.Context, ids []uint64) (map[uint64]*types.Tenant, error) {
	if len(ids) == 0 {
		return map[uint64]*types.Tenant{}, nil
	}
	var tenants []*types.Tenant
	if err := r.db.WithContext(ctx).Where("id IN ?", ids).Find(&tenants).Error; err != nil {
		return nil, err
	}
	out := make(map[uint64]*types.Tenant, len(tenants))
	for _, t := range tenants {
		if t != nil {
			out[t.ID] = t
		}
	}
	return out, nil
}

// ListTenants lists all tenants
func (r *tenantRepository) ListTenants(ctx context.Context) ([]*types.Tenant, error) {
	var tenants []*types.Tenant
	if err := r.db.WithContext(ctx).Order("created_at DESC").Find(&tenants).Error; err != nil {
		return nil, err
	}
	return tenants, nil
}

// SearchTenants searches tenants with pagination and filters
func (r *tenantRepository) SearchTenants(ctx context.Context, keyword string, tenantID uint64, page, pageSize int) ([]*types.Tenant, int64, error) {
	var tenants []*types.Tenant
	var total int64

	query := r.db.WithContext(ctx).Model(&types.Tenant{})

	// Build search conditions
	if tenantID > 0 && keyword != "" {
		escaped := escapeLikeKeyword(keyword)
		query = query.Where("id = ? OR name LIKE ? OR description LIKE ?", tenantID, "%"+escaped+"%", "%"+escaped+"%")
	} else if tenantID > 0 {
		query = query.Where("id = ?", tenantID)
	} else if keyword != "" {
		escaped := escapeLikeKeyword(keyword)
		query = query.Where("name LIKE ? OR description LIKE ?", "%"+escaped+"%", "%"+escaped+"%")
	}

	// Count total
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// Apply pagination
	if page > 0 && pageSize > 0 {
		offset := (page - 1) * pageSize
		query = query.Offset(offset).Limit(pageSize)
	}

	// Order by created_at DESC
	query = query.Order("created_at DESC")

	// Execute query
	if err := query.Find(&tenants).Error; err != nil {
		return nil, 0, err
	}

	return tenants, total, nil
}

// UpdateTenant updates tenant.
func (r *tenantRepository) UpdateTenant(ctx context.Context, tenant *types.Tenant) error {
	return r.db.WithContext(ctx).Model(&types.Tenant{}).Where("id = ?", tenant.ID).Updates(tenant).Error
}

// DeleteTenant hard-deletes the tenant and every related record for that
// tenant in one transaction.  Without the membership purge, /auth/me still
// lists the defunct tenant (name lookup fails → UI shows "#<id>").
func (r *tenantRepository) DeleteTenant(ctx context.Context, id uint64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. Tables referenced by knowledge_bases rows (no FK cascade).
		for _, c := range []struct {
			table  string
			column string
		}{
			{"embeddings", "knowledge_base_id"},
			{"chunks", "knowledge_base_id"},
			{"data_sources", "knowledge_base_id"},
		} {
			if err := tx.Unscoped().
				Table(c.table).
				Where(c.column+" IN (SELECT id FROM knowledge_bases WHERE tenant_id = ?)", id).
				Delete(nil).Error; err != nil {
				return err
			}
		}
		// 2. Tables that reference the tenant via a non-standard column.
		for _, c := range []struct {
			table  string
			column string
		}{
			{"agent_shares", "source_tenant_id"},
			{"kb_shares", "source_tenant_id"},
			{"messages", "agent_tenant_id"},
		} {
			if err := tx.Unscoped().
				Table(c.table).
				Where(c.column+" = ?", id).
				Delete(nil).Error; err != nil {
				return err
			}
		}
		// 3. Tables that use the standard "tenant_id" column.
		for _, c := range []struct {
			table  string
			column string
		}{
			{"sync_logs", "tenant_id"},
			{"knowledges", "tenant_id"},
			{"knowledge_bases", "tenant_id"},
			{"knowledge_tags", "tenant_id"},
			{"sessions", "tenant_id"},
			{"audit_logs", "tenant_id"},
			{"custom_agents", "tenant_id"},
			{"im_channels", "tenant_id"},
			{"im_channel_sessions", "tenant_id"},
			{"embed_channels", "tenant_id"},
			{"mcp_oauth_clients", "tenant_id"},
			{"mcp_oauth_tokens", "tenant_id"},
			{"mcp_services", "tenant_id"},
			{"mcp_tool_approvals", "tenant_id"},
			{"message_suggestion_events", "tenant_id"},
			{"message_suggestion_sets", "tenant_id"},
			{"models", "tenant_id"},
			{"organization_join_requests", "tenant_id"},
			{"organization_members_pre_plan3", "tenant_id"},
			{"organization_tenant_members", "tenant_id"},
			{"resource_bindings", "tenant_id"},
			{"resources", "tenant_id"},
			{"storage_backends", "tenant_id"},
			{"task_dead_letters", "tenant_id"},
			{"task_pending_ops", "tenant_id"},
			{"temporary_documents", "tenant_id"},
			{"tenant_api_keys", "tenant_id"},
			{"tenant_disabled_shared_agents", "tenant_id"},
			{"tenant_invitations", "tenant_id"},
			{"tenant_members", "tenant_id"},
			{"user_kb_pins", "tenant_id"},
			{"user_resource_favorites", "tenant_id"},
			{"vector_stores", "tenant_id"},
			{"web_search_providers", "tenant_id"},
			{"wiki_folders", "tenant_id"},
			{"wiki_log_entries", "tenant_id"},
			{"wiki_page_folders", "tenant_id"},
			{"wiki_page_issues", "tenant_id"},
			{"wiki_pages", "tenant_id"},
		} {
			if err := tx.Unscoped().
				Table(c.table).
				Where(c.column+" = ?", id).
				Delete(nil).Error; err != nil {
				return err
			}
		}
		return tx.Unscoped().Where("id = ?", id).Delete(&types.Tenant{}).Error
	})
}

func (r *tenantRepository) AdjustStorageUsed(ctx context.Context, tenantID uint64, delta int64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var tenant types.Tenant
		// 使用悲观锁确保并发安全
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&tenant, tenantID).Error; err != nil {
			return err
		}

		tenant.StorageUsed += delta
		// 保存更新并验证业务规则
		if tenant.StorageUsed < 0 {
			logger.Errorf(ctx, "tenant storage used is negative %d: %d", tenant.ID, tenant.StorageUsed)
			tenant.StorageUsed = 0
		}

		return tx.Save(&tenant).Error
	})
}

// BulkSetStorageQuota writes quotaBytes to storage_quota for every
// tenant in one statement. We don't WHERE-filter (the action is
// "apply globally"), so the affected count equals the row count of
// the tenants table.
//
// No transaction here: the operation is a single statement and we
// don't want to hold a long lock just to update a single column. If
// a concurrent CreateTenant lands in the middle, the new row gets
// the new default via the system-setting resolver in the handler —
// no risk of the new tenant being skipped.
func (r *tenantRepository) BulkSetStorageQuota(ctx context.Context, quotaBytes int64) (int64, error) {
	res := r.db.WithContext(ctx).
		Model(&types.Tenant{}).
		Where("1 = 1"). // GORM refuses unconditional UPDATEs without an explicit WHERE
		Update("storage_quota", quotaBytes)
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
}
