package types

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

const (
	// ProjectMaxNameLen is the longest project name a user may set.
	ProjectMaxNameLen = 200
	// ProjectMaxDescriptionLen bounds the free-text description.
	ProjectMaxDescriptionLen = 2000
	// ProjectMaxHostDirLen mirrors sessions.host_workspace_dir / the migration column.
	ProjectMaxHostDirLen = 1024
	// ProjectDefaultName is the fallback used when the directory has no usable
	// basename (e.g. the user picked "/"). The UI normally prefills the real
	// basename, so this is a safety net rather than the common path.
	ProjectDefaultName = "未命名项目"
)

var (
	// ErrProjectNotFound means no live project has that id under the tenant.
	ErrProjectNotFound = errors.New("项目不存在")
	// ErrProjectDirTaken means another live project already binds that directory.
	// Enforced by uq_projects_tenant_dir; surfaced here so the UI can say
	// "这个目录已经是一个项目了" instead of showing a raw constraint error.
	ErrProjectDirTaken = errors.New("该目录已被其他项目关联")
	// ErrProjectNameRequired means the name was empty after trimming.
	ErrProjectNameRequired = errors.New("项目名称不能为空")
	// ErrProjectDirRequired means no directory was supplied. Projects exist to
	// bind a directory, so a project without one has no meaning here.
	ErrProjectDirRequired = errors.New("项目必须关联本地目录")
	// ErrProjectDirNotApproved means the path failed the approved-directory
	// check. Once approved, the directory stays usable by the sandbox, so this
	// is the security boundary rather than a UI nicety.
	ErrProjectDirNotApproved = errors.New("目录未获得访问授权，请通过系统目录选择框选择")
	// ErrProjectHostDirImmutable means someone tried to re-point a project at a
	// different directory. Renaming a project is fine; moving it to a new
	// directory is not, because every session under it recorded the old path
	// in sessions.host_workspace_dir and would silently keep pointing there.
	ErrProjectHostDirImmutable = errors.New("项目创建后不能更换关联目录")
	// ErrProjectHasSessions means deletion was refused because sessions still
	// belong to the project. The UI asks the user to move or delete them first.
	ErrProjectHasSessions = errors.New("项目下还有会话，请先移动或删除")
	// ErrProjectRequired means a new session arrived without a project. The
	// composer no longer offers a project switcher, so every create call must
	// name a project (directly or through its directory); silently filing the
	// session under the workspace default would mix unrelated contexts.
	ErrProjectRequired = errors.New("请先选择一个项目，再开始对话")
)

// Project is the middle layer of 工作空间 → 项目 → 会话.
//
// A project binds exactly one local directory. Sessions under it inherit that
// directory at creation time (written into sessions.host_workspace_dir), so the
// sandbox authorization path keeps reading a single column and needs no change.
//
// TenantID is the workspace id (the same column name sessions use). Kept out of
// the JSON payload because the current workspace is implied by the request's
// auth context; exposing it invites cross-tenant forgery on the client side.
type Project struct {
	ID          string         `json:"id"                    gorm:"type:varchar(36);primaryKey"`
	TenantID    uint64         `json:"-"                     gorm:"not null;index"`
	Name        string         `json:"name"                  gorm:"type:varchar(200);not null"`
	Description string         `json:"description"           gorm:"type:text;not null;default:''"`
	HostDir     string         `json:"host_dir"              gorm:"type:varchar(1024);not null"`
	IsDefault   bool           `json:"is_default"            gorm:"not null;default:false"`
	SortOrder   int            `json:"sort_order"            gorm:"not null;default:0"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `json:"-"                     gorm:"index"`
}

// TableName pins the table name so GORM never pluralises into something else.
func (Project) TableName() string { return "projects" }

// ProjectWithStats is a project plus the numbers the sidebar needs to render its
// collapsed row ("竞品分析 · 3") without a second round trip per project.
type ProjectWithStats struct {
	Project
	// SessionCount counts live (not soft-deleted) sessions under this project.
	SessionCount int64 `json:"session_count"`
	// LastSessionAt is the newest session update time; nil when the project is
	// empty. Used to sort the sidebar by recent activity.
	LastSessionAt *time.Time `json:"last_session_at,omitempty"`
}