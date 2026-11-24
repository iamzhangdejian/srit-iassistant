package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// projectRepository persists projects in the projects table.
//
// Every read and write carries tenant_id in the WHERE clause. That is the whole
// isolation story for this table: a project id guessed from another workspace
// resolves to nil rather than to someone else's directory.
type projectRepository struct {
	db *gorm.DB
}

// NewProjectRepository creates a repository backed by GORM.
func NewProjectRepository(db *gorm.DB) interfaces.ProjectRepository {
	return &projectRepository{db: db}
}

func (r *projectRepository) scoped(ctx context.Context, tenantID uint64) *gorm.DB {
	return r.db.WithContext(ctx).Where("tenant_id = ?", tenantID)
}

func (r *projectRepository) Create(ctx context.Context, project *types.Project) error {
	if project == nil || project.ID == "" || project.TenantID == 0 {
		return errors.New("project identity is required")
	}
	now := time.Now()
	if project.CreatedAt.IsZero() {
		project.CreatedAt = now
	}
	project.UpdatedAt = now
	if err := r.db.WithContext(ctx).Create(project).Error; err != nil {
		// uq_projects_tenant_dir: the same directory already has a live project.
		// Surfacing a typed error lets the handler answer "这个目录已经是一个项目了"
		// instead of leaking a raw driver constraint message.
		if isProjectUniqueViolation(err) {
			return types.ErrProjectDirTaken
		}
		return fmt.Errorf("create project: %w", err)
	}
	return nil
}

func (r *projectRepository) Get(ctx context.Context, tenantID uint64, id string) (*types.Project, error) {
	if id == "" {
		return nil, nil
	}
	var project types.Project
	err := r.scoped(ctx, tenantID).Where("id = ?", id).First(&project).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get project: %w", err)
	}
	return &project, nil
}

func (r *projectRepository) GetByHostDir(
	ctx context.Context, tenantID uint64, hostDir string,
) (*types.Project, error) {
	hostDir = strings.TrimSpace(hostDir)
	if hostDir == "" {
		return nil, nil
	}
	var project types.Project
	err := r.scoped(ctx, tenantID).Where("host_dir = ?", hostDir).First(&project).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get project by host dir: %w", err)
	}
	return &project, nil
}

// List returns every live project with its session count.
//
// The count comes from one grouped subquery rather than N queries: the sidebar
// renders these numbers on every toggle, and a per-project COUNT would make it
// O(projects) round trips against SQLite.
func (r *projectRepository) List(
	ctx context.Context, tenantID uint64,
) ([]*types.ProjectWithStats, error) {
	var projects []*types.Project
	if err := r.scoped(ctx, tenantID).
		Order("is_default DESC, sort_order ASC, created_at ASC").
		Find(&projects).Error; err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	if len(projects) == 0 {
		return []*types.ProjectWithStats{}, nil
	}

	ids := make([]string, 0, len(projects))
	for _, p := range projects {
		ids = append(ids, p.ID)
	}

	// last_at is scanned as a string, not *time.Time: SQLite stores DATETIME as
	// TEXT and MAX() over it yields a string, which the sqlite driver refuses to
	// put into a time.Time ("unsupported Scan, storing driver.Value type string
	// into type *time.Time"). Postgres returns a real timestamp, which also
	// converts cleanly through string. Parsing happens once, below, and a
	// malformed value degrades to "no recent activity" instead of failing the
	// whole list.
	type statRow struct {
		ProjectID string
		Total     int64
		LastAt    string
	}
	var stats []statRow
	if err := r.db.WithContext(ctx).
		Model(&types.Session{}).
		Select("project_id AS project_id, COUNT(*) AS total, MAX(updated_at) AS last_at").
		// Scan() into a custom struct skips GORM's soft-delete scope, so the
		// deleted_at filter has to be explicit here. Without it, sessions that
		// were already deleted still inflate session_count and make a project
		// look undeletable ("项目下还有会话") while the UI shows zero.
		Where("tenant_id = ? AND project_id IN ? AND deleted_at IS NULL", tenantID, ids).
		Group("project_id").
		Scan(&stats).Error; err != nil {
		return nil, fmt.Errorf("count project sessions: %w", err)
	}

	byID := make(map[string]statRow, len(stats))
	for _, s := range stats {
		byID[s.ProjectID] = s
	}

	out := make([]*types.ProjectWithStats, 0, len(projects))
	for _, p := range projects {
		item := &types.ProjectWithStats{Project: *p}
		if s, ok := byID[p.ID]; ok {
			item.SessionCount = s.Total
			item.LastSessionAt = parseStoredTime(s.LastAt)
		}
		out = append(out, item)
	}
	return out, nil
}

func (r *projectRepository) Update(ctx context.Context, project *types.Project) error {
	if project == nil || project.ID == "" || project.TenantID == 0 {
		return errors.New("project identity is required")
	}
	project.UpdatedAt = time.Now()
	// Explicit column list: host_dir is intentionally absent so an Update call
	// can never re-point a project at another directory. Only the service may
	// create that binding, and only at creation time.
	res := r.scoped(ctx, project.TenantID).
		Model(&types.Project{}).
		Where("id = ?", project.ID).
		Updates(map[string]any{
			"name":        project.Name,
			"description": project.Description,
			"sort_order":  project.SortOrder,
			"updated_at":  project.UpdatedAt,
		})
	if res.Error != nil {
		return fmt.Errorf("update project: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return types.ErrProjectNotFound
	}
	return nil
}

func (r *projectRepository) Delete(ctx context.Context, tenantID uint64, id string) (bool, error) {
	res := r.scoped(ctx, tenantID).Where("id = ?", id).Delete(&types.Project{})
	if res.Error != nil {
		return false, fmt.Errorf("delete project: %w", res.Error)
	}
	return res.RowsAffected > 0, nil
}

func (r *projectRepository) GetDefault(
	ctx context.Context, tenantID uint64,
) (*types.Project, error) {
	var project types.Project
	err := r.scoped(ctx, tenantID).Where("is_default = ?", true).First(&project).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get default project: %w", err)
	}
	return &project, nil
}

// SetDefault flips the default flag in one transaction.
//
// Order matters: clearing first keeps uq_projects_default_per_tenant satisfied
// at every intermediate state. Doing it the other way round would transiently
// expose two flagged rows and the second UPDATE would fail.
func (r *projectRepository) SetDefault(ctx context.Context, tenantID uint64, id string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&types.Project{}).
			Where("tenant_id = ? AND is_default = ?", tenantID, true).
			Update("is_default", false).Error; err != nil {
			return fmt.Errorf("clear default project: %w", err)
		}
		res := tx.Model(&types.Project{}).
			Where("tenant_id = ? AND id = ?", tenantID, id).
			Update("is_default", true)
		if res.Error != nil {
			return fmt.Errorf("set default project: %w", res.Error)
		}
		if res.RowsAffected == 0 {
			return types.ErrProjectNotFound
		}
		return nil
	})
}

func (r *projectRepository) ClearDefault(ctx context.Context, tenantID uint64) error {
	err := r.db.WithContext(ctx).Model(&types.Project{}).
		Where("tenant_id = ? AND is_default = ?", tenantID, true).
		Update("is_default", false).Error
	if err != nil {
		return fmt.Errorf("clear default project: %w", err)
	}
	return nil
}

func (r *projectRepository) NextSortOrder(ctx context.Context, tenantID uint64) (int, error) {
	var maxOrder *int
	err := r.db.WithContext(ctx).Model(&types.Project{}).
		Where("tenant_id = ?", tenantID).
		Select("MAX(sort_order)").
		Scan(&maxOrder).Error
	if err != nil {
		return 0, fmt.Errorf("next project sort order: %w", err)
	}
	if maxOrder == nil {
		return 0, nil
	}
	return *maxOrder + 1, nil
}

func (r *projectRepository) CountSessions(
	ctx context.Context, tenantID uint64, projectID string,
) (int64, error) {
	if projectID == "" {
		return 0, nil
	}
	var total int64
	err := r.db.WithContext(ctx).Model(&types.Session{}).
		// Keep the filter explicit: this count gates project deletion, and a
		// count that includes soft-deleted sessions blocks a delete the user
		// cannot resolve from the UI.
		Where("tenant_id = ? AND project_id = ? AND deleted_at IS NULL", tenantID, projectID).
		Count(&total).Error
	if err != nil {
		return 0, fmt.Errorf("count project sessions: %w", err)
	}
	return total, nil
}

func (r *projectRepository) MoveSessionsTo(
	ctx context.Context, tenantID uint64, fromID, toID string,
) (int64, error) {
	if fromID == "" || fromID == toID {
		return 0, nil
	}
	// toID == "" means "detach": the conversations stay alive but lose their
	// grouping. This is the path the UI takes when the user deletes a project
	// that still has sessions — without it the rows would keep pointing at a
	// soft-deleted project and never surface in any project's list.
	// Restrict to live rows: moving soft-deleted sessions would rewrite history
	// for rows no one can see, and (with the counts above now excluding them)
	// would also make the reported moved-count disagree with what the UI shows.
	res := r.db.WithContext(ctx).Model(&types.Session{}).
		Where("tenant_id = ? AND project_id = ? AND deleted_at IS NULL", tenantID, fromID).
		Update("project_id", toID)
	if res.Error != nil {
		return 0, fmt.Errorf("move project sessions: %w", res.Error)
	}
	return res.RowsAffected, nil
}

func (r *projectRepository) DeleteSessionsOf(
	ctx context.Context, tenantID uint64, projectID string,
) (int64, error) {
	if projectID == "" {
		return 0, nil
	}
	// Soft delete, same as SessionRepository.Delete: the transcript stays
	// recoverable in the database and no local files are touched.
	res := r.db.WithContext(ctx).
		Where("tenant_id = ? AND project_id = ?", tenantID, projectID).
		Delete(&types.Session{})
	if res.Error != nil {
		return 0, fmt.Errorf("delete project sessions: %w", res.Error)
	}
	return res.RowsAffected, nil
}

// isProjectUniqueViolation recognises both unique indexes on projects:
// uq_projects_tenant_dir (directory already bound) and
// uq_projects_default_per_tenant (a second default slipping through).
//
// The message sniff matches the helper used by the credential vault so SQLite
// ("UNIQUE constraint failed") and Postgres ("duplicate key value") both land
// here without importing a driver-specific error package.
func isProjectUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate") || strings.Contains(msg, "unique constraint")
}

// parseStoredTime converts a DB-rendered timestamp into a time.Time. SQLite
// hands back "2026-11-14 18:34:45.412095+08:00" (and sometimes without an
// offset or with a "T" separator); Postgres gives RFC3339. Anything unparseable
// returns nil so a single odd row cannot break the project list.
func parseStoredTime(raw string) *time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	layouts := []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02T15:04:05.999999999",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return &parsed
		}
	}
	return nil
}
