package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// agentBuildTaskRepository is the GORM-backed ledger/queue used by the agent
// task monitor page. All queue transitions are expressed as conditional UPDATEs
// so two processes (or two dispatcher ticks) can never claim the same row.
type agentBuildTaskRepository struct {
	db *gorm.DB
}

// NewAgentBuildTaskRepository builds the ledger repository.
func NewAgentBuildTaskRepository(db *gorm.DB) interfaces.AgentBuildTaskRepository {
	return &agentBuildTaskRepository{db: db}
}

// Create inserts a new ledger row.
func (r *agentBuildTaskRepository) Create(ctx context.Context, task *types.AgentBuildTask) error {
	return r.db.WithContext(ctx).Create(task).Error
}

// GetByID loads one row, translating the driver's not-found error into the
// package sentinel so upper layers avoid importing gorm.
func (r *agentBuildTaskRepository) GetByID(ctx context.Context, id string) (*types.AgentBuildTask, error) {
	var row types.AgentBuildTask
	if err := r.db.WithContext(ctx).Where("id = ?", id).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, types.ErrAgentBuildTaskNotFound
		}
		return nil, err
	}
	return &row, nil
}

// List returns a filtered page plus the unpaged total. The filter is rebuilt for
// each query so Count and Find cannot leak state into each other.
func (r *agentBuildTaskRepository) List(
	ctx context.Context, filter types.AgentBuildTaskFilter,
) ([]*types.AgentBuildTask, int64, error) {
	build := func() *gorm.DB {
		q := r.db.WithContext(ctx).Model(&types.AgentBuildTask{})
		if filter.TenantID != 0 {
			q = q.Where("tenant_id = ?", filter.TenantID)
		}
		if statuses := filter.StatusList(); len(statuses) > 0 {
			q = q.Where("status IN ?", statuses)
		}
		if filter.KnowledgeBaseID != "" {
			q = q.Where("knowledge_base_id = ?", filter.KnowledgeBaseID)
		}
		if filter.KnowledgeID != "" {
			q = q.Where("knowledge_id = ?", filter.KnowledgeID)
		}
		if filter.Keyword != "" {
			// LOWER(...) LIKE keeps this portable (SQLite has no ILIKE).
			q = q.Where("LOWER(doc_name) LIKE LOWER(?)", "%"+filter.Keyword+"%")
		}
		return q
	}

	var total int64
	if err := build().Count(&total).Error; err != nil {
		return nil, 0, err
	}

	rows := make([]*types.AgentBuildTask, 0, filter.PageSize)
	offset := (filter.Page - 1) * filter.PageSize
	if err := build().Order("created_at DESC, id DESC").
		Limit(filter.PageSize).Offset(offset).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// CountByStatus groups rows by status. tenantID limits the count to one tenant
// (0 = every tenant, which is what a system administrator and the dispatcher
// backlog guard want).
func (r *agentBuildTaskRepository) CountByStatus(
	ctx context.Context, tenantID uint64,
) (map[string]int64, error) {
	var rows []struct {
		Status string `gorm:"column:status"`
		Total  int64  `gorm:"column:total"`
	}
	q := r.db.WithContext(ctx).Model(&types.AgentBuildTask{}).
		Select("status, COUNT(*) AS total").
		Group("status")
	if tenantID != 0 {
		q = q.Where("tenant_id = ?", tenantID)
	}
	if err := q.Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(rows))
	for _, row := range rows {
		out[row.Status] = row.Total
	}
	return out, nil
}

// CountFinishedSince counts rows that reached `status` at or after `since`.
func (r *agentBuildTaskRepository) CountFinishedSince(
	ctx context.Context, status string, since time.Time, tenantID uint64,
) (int64, error) {
	var total int64
	q := r.db.WithContext(ctx).Model(&types.AgentBuildTask{}).
		Where("status = ? AND finished_at IS NOT NULL AND finished_at >= ?", status, since)
	if tenantID != 0 {
		q = q.Where("tenant_id = ?", tenantID)
	}
	err := q.Count(&total).Error
	return total, err
}

// OldestQueuedAt returns the queued_at of the oldest waiting row.
func (r *agentBuildTaskRepository) OldestQueuedAt(ctx context.Context, tenantID uint64) (*time.Time, error) {
	var rows []struct {
		QueuedAt *time.Time `gorm:"column:queued_at"`
	}
	q := r.db.WithContext(ctx).Model(&types.AgentBuildTask{}).
		Select("queued_at").
		Where("status = ?", types.AgentBuildStatusQueued)
	if tenantID != 0 {
		q = q.Where("tenant_id = ?", tenantID)
	}
	err := q.Order("queued_at ASC").Limit(1).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0].QueuedAt, nil
}

// ClaimQueued promotes up to `limit` eligible rows to running and returns them.
//
// The UPDATE itself is the lock: under READ COMMITTED a concurrent claimer that
// picked the same candidate ids re-evaluates the `status = queued` qualifier on
// the locked row and skips it, so a row is handed to exactly one dispatcher.
// attempts is bumped here (not at submit time) so a crash between claim and POST
// still counts against MaxAttempts.
func (r *agentBuildTaskRepository) ClaimQueued(ctx context.Context, limit int) ([]*types.AgentBuildTask, error) {
	rows := make([]*types.AgentBuildTask, 0, limit)
	if limit <= 0 {
		return rows, nil
	}
	now := time.Now()
	err := r.db.WithContext(ctx).Raw(`
		UPDATE agent_build_tasks
		   SET status = ?, started_at = ?, updated_at = ?, attempts = attempts + 1
		 WHERE id IN (
		       SELECT id FROM agent_build_tasks
		        WHERE status = ? AND next_attempt_at <= ?
		        ORDER BY queued_at ASC
		        LIMIT ?
		 )
		RETURNING *`,
		types.AgentBuildStatusRunning, now, now,
		types.AgentBuildStatusQueued, now, limit,
	).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// CountInFlight counts rows the gateway is currently working on.
func (r *agentBuildTaskRepository) CountInFlight(ctx context.Context) (int64, error) {
	var total int64
	err := r.db.WithContext(ctx).Model(&types.AgentBuildTask{}).
		Where("status = ?", types.AgentBuildStatusRunning).
		Count(&total).Error
	return total, err
}

// CountActiveForKnowledge counts non-terminal rows for one document, used to
// avoid queueing the same document twice.
func (r *agentBuildTaskRepository) CountActiveForKnowledge(ctx context.Context, knowledgeID string) (int64, error) {
	var total int64
	err := r.db.WithContext(ctx).Model(&types.AgentBuildTask{}).
		Where("knowledge_id = ? AND status IN ?", knowledgeID,
			[]string{types.AgentBuildStatusQueued, types.AgentBuildStatusRunning}).
		Count(&total).Error
	return total, err
}

// MarkSubmitted records that the gateway accepted the task.
func (r *agentBuildTaskRepository) MarkSubmitted(
	ctx context.Context, id string, gatewayTaskID string, submittedAt time.Time,
) error {
	return r.db.WithContext(ctx).Model(&types.AgentBuildTask{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":          types.AgentBuildStatusRunning,
			"gateway_task_id": gatewayTaskID,
			"submitted_at":    submittedAt,
			"last_error":      "",
			"updated_at":      submittedAt,
		}).Error
}

// MarkRetry either returns a row to the queue with a backoff, or ends it as
// failed once attempts are exhausted.
func (r *agentBuildTaskRepository) MarkRetry(
	ctx context.Context, id string, lastError string, nextAttemptAt time.Time, terminal bool,
) error {
	now := time.Now()
	updates := map[string]interface{}{
		"last_error": lastError,
		"updated_at": now,
	}
	if terminal {
		updates["status"] = types.AgentBuildStatusFailed
		updates["finished_at"] = now
	} else {
		updates["status"] = types.AgentBuildStatusQueued
		updates["next_attempt_at"] = nextAttemptAt
		updates["started_at"] = nil
	}
	return r.db.WithContext(ctx).Model(&types.AgentBuildTask{}).
		Where("id = ?", id).Updates(updates).Error
}

// MarkTerminal records a terminal outcome.
func (r *agentBuildTaskRepository) MarkTerminal(
	ctx context.Context, id string, status string, output string, lastError string, durationMs int64,
) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&types.AgentBuildTask{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":      status,
			"output_text": output,
			"last_error":  lastError,
			"finished_at": now,
			"duration_ms": durationMs,
			"updated_at":  now,
		}).Error
}

// Requeue resets a terminal row into a fresh queue entry.
func (r *agentBuildTaskRepository) Requeue(ctx context.Context, id string) (int64, error) {
	now := time.Now()
	res := r.db.WithContext(ctx).Exec(`
		UPDATE agent_build_tasks
		   SET status = ?, attempts = 0, next_attempt_at = ?, last_error = '', output_text = '',
		       gateway_task_id = '', submitted_at = NULL, started_at = NULL, finished_at = NULL,
		       duration_ms = NULL, updated_at = ?
		 WHERE id = ? AND status IN (?, ?, ?)`,
		types.AgentBuildStatusQueued, now, now, id,
		types.AgentBuildStatusSucceeded, types.AgentBuildStatusFailed, types.AgentBuildStatusCancelled,
	)
	return res.RowsAffected, res.Error
}

// Cancel marks a queued/running row cancelled.
func (r *agentBuildTaskRepository) Cancel(ctx context.Context, id string) (int64, error) {
	now := time.Now()
	res := r.db.WithContext(ctx).Exec(`
		UPDATE agent_build_tasks
		   SET status = ?, finished_at = ?, updated_at = ?
		 WHERE id = ? AND status IN (?, ?)`,
		types.AgentBuildStatusCancelled, now, now, id,
		types.AgentBuildStatusQueued, types.AgentBuildStatusRunning,
	)
	return res.RowsAffected, res.Error
}

// ListInFlight returns running rows that carry a gateway task id.
func (r *agentBuildTaskRepository) ListInFlight(ctx context.Context, limit int) ([]*types.AgentBuildTask, error) {
	rows := make([]*types.AgentBuildTask, 0, limit)
	err := r.db.WithContext(ctx).Model(&types.AgentBuildTask{}).
		Where("status = ?", types.AgentBuildStatusRunning).
		Where("gateway_task_id IS NOT NULL AND gateway_task_id <> ''").
		Order("submitted_at ASC").
		Limit(limit).
		Find(&rows).Error
	return rows, err
}

// RecoverInterrupted handles rows left claimed by a process that died before it
// recorded a gateway task id: they are requeued while attempts remain, else
// failed. Rows that already carry a gateway task id stay running so the status
// poller can keep tracking them across a restart.
func (r *agentBuildTaskRepository) RecoverInterrupted(ctx context.Context) (int64, error) {
	now := time.Now()
	const predicate = `status = ? AND (gateway_task_id IS NULL OR gateway_task_id = '')`

	res := r.db.WithContext(ctx).Exec(`
		UPDATE agent_build_tasks
		   SET status = ?, next_attempt_at = ?, started_at = NULL, updated_at = ?
		 WHERE `+predicate+` AND attempts < max_attempts`,
		types.AgentBuildStatusQueued, now, now, types.AgentBuildStatusRunning,
	)
	if res.Error != nil {
		return 0, res.Error
	}
	requeued := res.RowsAffected

	failed := r.db.WithContext(ctx).Exec(`
		UPDATE agent_build_tasks
		   SET status = ?, finished_at = ?, last_error = ?, updated_at = ?
		 WHERE `+predicate+` AND attempts >= max_attempts`,
		types.AgentBuildStatusFailed, now, "interrupted before the hand-off completed", now,
		types.AgentBuildStatusRunning,
	)
	if failed.Error != nil {
		return requeued, failed.Error
	}
	return requeued + failed.RowsAffected, nil
}

// KnowledgeBaseNames resolves display names for the list view.
func (r *agentBuildTaskRepository) KnowledgeBaseNames(ctx context.Context, ids []string) (map[string]string, error) {
	out := make(map[string]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var rows []struct {
		ID   string `gorm:"column:id"`
		Name string `gorm:"column:name"`
	}
	if err := r.db.WithContext(ctx).Table("knowledge_bases").
		Select("id, name").Where("id IN ?", ids).Scan(&rows).Error; err != nil {
		return out, err
	}
	for _, row := range rows {
		out[row.ID] = row.Name
	}
	return out, nil
}

// TableExists reports whether the ledger table has been created yet.
func (r *agentBuildTaskRepository) TableExists(ctx context.Context) bool {
	return r.db.WithContext(ctx).Migrator().HasTable("agent_build_tasks")
}
