package service

import (
	"context"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/hostdir"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ProjectService owns the 工作空间 → 项目 → 会话 middle layer.
//
// A project binds one approved local directory. Sessions under it inherit that
// directory at creation, so the sandbox authorization chain
// (hostdir.MatchApproved → sessions.host_workspace_dir → ProjectDirForSession) keeps
// reading exactly one column and needs no change.
type ProjectService struct {
	repo interfaces.ProjectRepository
	// approvedDirs returns the directories the user has authorised through the
	// native picker. Nil means no directory is authorised, and every create
	// would then be refused — which is correct: a project without an approved
	// directory would hand the sandbox a path it may not read.
	approvedDirs hostdir.Loader
}

// NewProjectService wires the repository and the approved-directory loader.
func NewProjectService(
	repo interfaces.ProjectRepository,
	approvedDirs hostdir.Loader,
) *ProjectService {
	return &ProjectService{repo: repo, approvedDirs: approvedDirs}
}

// CreateProjectInput is what the UI submits when the user picks a folder and
// confirms a name. HostDir must already be an approved directory: the picker
// authorises it, and this service only verifies, never widens the list.
type CreateProjectInput struct {
	Name        string
	Description string
	HostDir     string
}

// UpdateProjectInput carries the editable fields. HostDir is absent on purpose:
// re-pointing a project would strand every session that recorded the old path.
type UpdateProjectInput struct {
	Name        *string
	Description *string
	SortOrder   *int
}

// Create validates, derives the default name from the directory basename, and
// makes the very first project of a workspace its default automatically.
func (s *ProjectService) Create(
	ctx context.Context, tenantID uint64, in CreateProjectInput,
) (*types.Project, error) {
	if tenantID == 0 {
		return nil, apperrors.NewUnauthorizedError("workspace context is required")
	}

	hostDir, err := s.approveDir(in.HostDir)
	if err != nil {
		return nil, err
	}

	name := strings.TrimSpace(in.Name)
	if name == "" {
		// The UI prefills the basename, so this only fires for a raw API caller
		// or a directory whose last segment is empty.
		name = projectDirDisplayName(hostDir)
	}
	if utf8.RuneCountInString(name) > types.ProjectMaxNameLen {
		return nil, apperrors.NewBadRequestError("项目名称过长")
	}
	description := strings.TrimSpace(in.Description)
	if utf8.RuneCountInString(description) > types.ProjectMaxDescriptionLen {
		return nil, apperrors.NewBadRequestError("项目描述过长")
	}

	// A directory with a live project is rejected here rather than relying on
	// the handler to pre-check, so the API and the UI behave identically.
	if existing, err := s.repo.GetByHostDir(ctx, tenantID, hostDir); err != nil {
		return nil, err
	} else if existing != nil {
		return nil, apperrors.NewBadRequestError("该目录已被项目「" + existing.Name + "」关联")
	}

	sortOrder, err := s.repo.NextSortOrder(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	project := &types.Project{
		ID:          uuid.NewString(),
		TenantID:    tenantID,
		Name:        name,
		Description: description,
		HostDir:     hostDir,
		SortOrder:   sortOrder,
	}
	if err := s.repo.Create(ctx, project); err != nil {
		// Map the repository's typed conflict into a message the UI can show.
		if err == types.ErrProjectDirTaken {
			return nil, apperrors.NewBadRequestError("该目录已被其他项目关联")
		}
		return nil, err
	}

	// First project in the workspace becomes the default destination for new
	// sessions. Doing it after the insert keeps the "no default yet" state
	// honest: a brand-new workspace starts with zero projects, not a fake one.
	if current, err := s.repo.GetDefault(ctx, tenantID); err == nil && current == nil {
		if err := s.repo.SetDefault(ctx, tenantID, project.ID); err != nil {
			logger.Warnf(ctx, "failed to mark first project as default: %v", err)
		} else {
			project.IsDefault = true
		}
	}

	logger.Infof(ctx, "project created: %s (%s)", project.ID, project.HostDir)
	return project, nil
}

// List returns every project of the workspace with its session counts.
func (s *ProjectService) List(
	ctx context.Context, tenantID uint64,
) ([]*types.ProjectWithStats, error) {
	if tenantID == 0 {
		return nil, apperrors.NewUnauthorizedError("workspace context is required")
	}
	return s.repo.List(ctx, tenantID)
}

// Get loads one project, translating absence into a 404-style error.
func (s *ProjectService) Get(
	ctx context.Context, tenantID uint64, id string,
) (*types.Project, error) {
	project, err := s.repo.Get(ctx, tenantID, strings.TrimSpace(id))
	if err != nil {
		return nil, err
	}
	if project == nil {
		return nil, apperrors.NewNotFoundError("项目不存在")
	}
	return project, nil
}

// Update renames or re-describes a project. The directory binding is immutable.
func (s *ProjectService) Update(
	ctx context.Context, tenantID uint64, id string, in UpdateProjectInput,
) (*types.Project, error) {
	project, err := s.Get(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" {
			return nil, apperrors.NewBadRequestError("项目名称不能为空")
		}
		if utf8.RuneCountInString(name) > types.ProjectMaxNameLen {
			return nil, apperrors.NewBadRequestError("项目名称过长")
		}
		project.Name = name
	}
	if in.Description != nil {
		description := strings.TrimSpace(*in.Description)
		if utf8.RuneCountInString(description) > types.ProjectMaxDescriptionLen {
			return nil, apperrors.NewBadRequestError("项目描述过长")
		}
		project.Description = description
	}
	if in.SortOrder != nil {
		project.SortOrder = *in.SortOrder
	}
	if err := s.repo.Update(ctx, project); err != nil {
		return nil, err
	}
	return project, nil
}

// DeleteProjectSessions decides what happens to the conversations under a
// project being deleted.
type DeleteProjectSessions int

const (
	// ProjectSessionsRefuse deletes nothing while sessions remain. The default,
	// because losing a transcript by mis-clicking is unrecoverable from the UI.
	ProjectSessionsRefuse DeleteProjectSessions = iota
	// ProjectSessionsMove reassigns the sessions to another project, or detaches
	// them (project_id = "") when no replacement is available.
	ProjectSessionsMove
	// ProjectSessionsDelete soft-deletes the sessions together with the project.
	ProjectSessionsDelete
)

// DeleteInput tells Delete what to do with the conversations inside.
type DeleteInput struct {
	// Sessions selects the behaviour above.
	Sessions DeleteProjectSessions
	// MoveToProjectID is the destination when Sessions == ProjectSessionsMove.
	// Empty detaches the sessions instead (project_id = ""), which is what the
	// UI offers for "keep the conversations, just lose the grouping".
	MoveToProjectID string
}

// Delete removes a project. It never touches the local directory: deleting a
// folder on the user's disk is not something this program does.
func (s *ProjectService) Delete(
	ctx context.Context, tenantID uint64, id string, in DeleteInput,
) error {
	project, err := s.Get(ctx, tenantID, id)
	if err != nil {
		return err
	}

	count, err := s.repo.CountSessions(ctx, tenantID, project.ID)
	if err != nil {
		return err
	}

	switch in.Sessions {
	case ProjectSessionsRefuse:
		if count > 0 {
			return apperrors.NewBadRequestError("项目下还有会话，请先移动或删除")
		}
	case ProjectSessionsMove:
		target := strings.TrimSpace(in.MoveToProjectID)
		if target == project.ID {
			return apperrors.NewBadRequestError("不能把会话移动到正在删除的项目")
		}
		if target != "" {
			dest, err := s.repo.Get(ctx, tenantID, target)
			if err != nil {
				return err
			}
			if dest == nil {
				return apperrors.NewBadRequestError("目标项目不存在")
			}
		}
		if _, err := s.repo.MoveSessionsTo(ctx, tenantID, project.ID, target); err != nil {
			return err
		}
	case ProjectSessionsDelete:
		if _, err := s.repo.DeleteSessionsOf(ctx, tenantID, project.ID); err != nil {
			return err
		}
	default:
		return apperrors.NewBadRequestError("未知的会话处理方式")
	}

	wasDefault := project.IsDefault
	if _, err := s.repo.Delete(ctx, tenantID, project.ID); err != nil {
		return err
	}

	// Deleting the default project must not leave the workspace without a
	// default, or new sessions would have nowhere to land. Hand the flag to the
	// oldest remaining project; a workspace with none simply stays in the
	// first-run "create your first project" state.
	if wasDefault {
		if err := s.promoteFirstProject(ctx, tenantID); err != nil {
			logger.Warnf(ctx, "failed to promote a new default project: %v", err)
		}
	}

	logger.Infof(ctx, "project deleted: %s (sessions=%d)", project.ID, count)
	return nil
}

// SetDefault marks one project as the destination for new sessions.
func (s *ProjectService) SetDefault(
	ctx context.Context, tenantID uint64, id string,
) error {
	if _, err := s.Get(ctx, tenantID, id); err != nil {
		return err
	}
	return s.repo.SetDefault(ctx, tenantID, id)
}

// ClearDefault unsets the workspace default, putting it back into the
// "no automatic destination" state. Exposed so the UI can undo an accidental
// default choice without deleting the project.
func (s *ProjectService) ClearDefault(ctx context.Context, tenantID uint64) error {
	if tenantID == 0 {
		return apperrors.NewUnauthorizedError("workspace context is required")
	}
	return s.repo.ClearDefault(ctx, tenantID)
}

// ResolveForSession returns the project a new session should belong to.
//
// Precedence:
//  1. explicitProjectID, when the caller passed project_id;
//  2. otherwise the project that already owns hostDir, so a session created
//     from the old "open folder" flow still lands under the right project;
//  3. otherwise ErrProjectRequired — a session is never created without an
//     explicit project choice.
//
// Rule 3 used to fall back to the workspace default. That was removed on
// purpose: the composer no longer offers a project switcher, so a silent
// fallback would file conversations the user never assigned into a project,
// mixing contexts across unrelated working directories. The UI is expected to
// send project_id (or project_dir, which maps back to its owning project) for
// every new session; anything else is a client bug worth surfacing.
func (s *ProjectService) ResolveForSession(
	ctx context.Context, tenantID uint64, explicitProjectID, hostDir string,
) (*types.Project, error) {
	if id := strings.TrimSpace(explicitProjectID); id != "" {
		project, err := s.repo.Get(ctx, tenantID, id)
		if err != nil {
			return nil, err
		}
		if project == nil {
			return nil, apperrors.NewBadRequestError("项目不存在")
		}
		return project, nil
	}
	if dir := strings.TrimSpace(hostDir); dir != "" {
		project, err := s.repo.GetByHostDir(ctx, tenantID, dir)
		if err != nil {
			return nil, err
		}
		if project != nil {
			return project, nil
		}
	}
	return nil, types.ErrProjectRequired
}

// ApproveDir checks a raw directory against the picker-authorised list and
// returns its cleaned absolute form. Exposed so the session handler and the
// project handler enforce the identical boundary.
func (s *ProjectService) ApproveDir(raw string) (string, error) {
	return s.approveDir(raw)
}

func (s *ProjectService) approveDir(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", apperrors.NewBadRequestError("项目必须关联本地目录")
	}
	if !filepath.IsAbs(raw) {
		return "", apperrors.NewBadRequestError("目录必须是绝对路径")
	}
	if len(raw) > types.ProjectMaxHostDirLen {
		return "", apperrors.NewBadRequestError("目录路径过长")
	}
	// hostdir.MatchApproved does exact-after-Clean matching. A subdirectory of an
	// approved root is NOT approved: the user granted the root, and honouring
	// children would let a stored path reach deeper than the picker offered.
	if _, ok := hostdir.MatchApproved(raw, s.approvedDirsList()); !ok {
		return "", apperrors.NewForbiddenError("目录未获得访问授权，请通过系统目录选择框选择")
	}
	return filepath.Clean(raw), nil
}

func (s *ProjectService) approvedDirsList() []string {
	if s == nil || s.approvedDirs == nil {
		return nil
	}
	return s.approvedDirs()
}

func (s *ProjectService) promoteFirstProject(ctx context.Context, tenantID uint64) error {
	projects, err := s.repo.List(ctx, tenantID)
	if err != nil {
		return err
	}
	for _, p := range projects {
		if p.IsDefault {
			return nil
		}
	}
	if len(projects) == 0 {
		// No project left: clear the flag so GetDefault keeps returning nil and
		// the sidebar shows the first-run prompt again.
		return s.repo.ClearDefault(ctx, tenantID)
	}
	return s.repo.SetDefault(ctx, tenantID, projects[0].ID)
}

// projectDirDisplayName derives a human name from a path: the last path
// segment, or a fallback when the path is a filesystem root.
func projectDirDisplayName(dir string) string {
	cleaned := filepath.Clean(strings.TrimSpace(dir))
	base := filepath.Base(cleaned)
	if base == "" || base == "." || base == string(filepath.Separator) {
		return types.ProjectDefaultName
	}
	return base
}