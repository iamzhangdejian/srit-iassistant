package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// ProjectRepository persists projects (工作空间 → 项目 → 会话 的中间层).
//
// Every method is tenant-scoped: a caller can never read or mutate a project
// that belongs to another workspace, even with a valid project id.
type ProjectRepository interface {
	// Create inserts a project. Returns types.ErrProjectDirTaken when the
	// (tenant_id, host_dir) unique index rejects a duplicate directory.
	Create(ctx context.Context, project *types.Project) error

	// Get returns nil when no live project has that id under the tenant.
	Get(ctx context.Context, tenantID uint64, id string) (*types.Project, error)

	// GetByHostDir returns nil when no live project binds that directory.
	// Used at session creation to attach a session to the project that owns
	// its directory, rather than trusting a client-supplied project id.
	GetByHostDir(ctx context.Context, tenantID uint64, hostDir string) (*types.Project, error)

	// List returns live projects ordered by sort_order then created_at, each
	// carrying its session count and last activity for the sidebar.
	List(ctx context.Context, tenantID uint64) ([]*types.ProjectWithStats, error)

	// Update persists name/description/sort order of an existing project.
	// It deliberately does not touch host_dir; see
	// types.ErrProjectHostDirImmutable.
	Update(ctx context.Context, project *types.Project) error

	// Delete soft-deletes a project and reports whether a row was affected.
	Delete(ctx context.Context, tenantID uint64, id string) (bool, error)

	// GetDefault returns the tenant's default project, or nil when none is
	// flagged. A workspace with no default project simply has no automatic
	// destination for new sessions yet (first-run state).
	GetDefault(ctx context.Context, tenantID uint64) (*types.Project, error)

	// SetDefault makes id the tenant's default project, clearing the flag from
	// any other project first. Both statements run in one transaction so the
	// uq_projects_default_per_tenant index never sees two flagged rows.
	SetDefault(ctx context.Context, tenantID uint64, id string) error

	// ClearDefault unsets the default flag without selecting a replacement.
	ClearDefault(ctx context.Context, tenantID uint64) error

	// NextSortOrder returns the value a newly created project should take so
	// it lands at the bottom of the sidebar.
	NextSortOrder(ctx context.Context, tenantID uint64) (int, error)

	// CountSessions reports how many live sessions reference the project.
	CountSessions(ctx context.Context, tenantID uint64, projectID string) (int64, error)

	// MoveSessionsTo reassigns every live session of fromID to toID. Used when
	// a project is deleted and the user chose to keep the conversations.
	MoveSessionsTo(ctx context.Context, tenantID uint64, fromID, toID string) (int64, error)

	// DeleteSessionsOf soft-deletes every session under the project. Used when
	// a project is deleted and the user chose to discard the conversations.
	DeleteSessionsOf(ctx context.Context, tenantID uint64, projectID string) (int64, error)
}