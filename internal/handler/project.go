package handler

import (
	stderrors "errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// ProjectHandler exposes the 工作空间 → 项目 → 会话 middle layer over HTTP.
//
// Tenant identity always comes from the authenticated context, never from the
// request body: a client that supplies someone else's project id gets a 404
// because every repository query is tenant-scoped.
type ProjectHandler struct {
	projectService *service.ProjectService
}

// NewProjectHandler wires the project service.
func NewProjectHandler(projectService *service.ProjectService) *ProjectHandler {
	return &ProjectHandler{projectService: projectService}
}

func projectTenantID(c *gin.Context) (uint64, bool) {
	raw, exists := c.Get(types.TenantIDContextKey.String())
	if !exists {
		return 0, false
	}
	tenantID, ok := raw.(uint64)
	return tenantID, ok && tenantID != 0
}

// ListProjects godoc
// @Summary      项目列表
// @Description  返回当前工作空间下全部项目，附带会话数量与最近活动时间。
// @Tags         项目
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "项目列表"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /projects [get]
func (h *ProjectHandler) ListProjects(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, ok := projectTenantID(c)
	if !ok {
		_ = c.Error(errors.NewUnauthorizedError("Unauthorized"))
		return
	}
	projects, err := h.projectService.List(ctx, tenantID)
	if err != nil {
		writeProjectError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		// Always an array: the sidebar renders "create your first project" from
		// an empty list rather than from a null, so no client-side null check.
		"data": projects,
	})
}

type createProjectRequest struct {
	// Name is optional; empty means "use the directory's basename".
	Name        string `json:"name"`
	Description string `json:"description"`
	// HostDir must be an absolute path the user picked through the native
	// folder dialog. The picker writes it into the approved list, and the
	// service only verifies membership — it never widens that list.
	HostDir string `json:"host_dir" binding:"required"`
}

// CreateProject godoc
// @Summary      创建项目
// @Description  把一个已授权的本地目录绑定为项目。项目名默认取目录名。
// @Tags         项目
// @Accept       json
// @Produce      json
// @Param        request  body      createProjectRequest  true  "创建项目请求"
// @Success      201      {object}  map[string]interface{}  "创建的项目"
// @Failure      400      {object}  errors.AppError         "参数错误或目录已占用"
// @Failure      403      {object}  errors.AppError         "目录未获授权"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /projects [post]
func (h *ProjectHandler) CreateProject(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, ok := projectTenantID(c)
	if !ok {
		_ = c.Error(errors.NewUnauthorizedError("Unauthorized"))
		return
	}
	var request createProjectRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		_ = c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	project, err := h.projectService.Create(ctx, tenantID, service.CreateProjectInput{
		Name:        request.Name,
		Description: request.Description,
		HostDir:     request.HostDir,
	})
	if err != nil {
		writeProjectError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": project})
}

// GetProject godoc
// @Summary      项目详情
// @Description  按 id 读取单个项目。
// @Tags         项目
// @Produce      json
// @Param        id  path      string  true  "项目 ID"
// @Success      200  {object}  map[string]interface{}  "项目详情"
// @Failure      404  {object}  errors.AppError         "项目不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /projects/{id} [get]
func (h *ProjectHandler) GetProject(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, ok := projectTenantID(c)
	if !ok {
		_ = c.Error(errors.NewUnauthorizedError("Unauthorized"))
		return
	}
	project, err := h.projectService.Get(ctx, tenantID, c.Param("id"))
	if err != nil {
		writeProjectError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": project})
}

// updateProjectRequest uses pointers so "absent" and "empty string" stay
// distinguishable: clearing a description is legal, leaving the name out must
// not blank it.
type updateProjectRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	SortOrder   *int    `json:"sort_order"`
}

// UpdateProject godoc
// @Summary      更新项目
// @Description  修改项目名称或描述。关联目录不可更改。
// @Tags         项目
// @Accept       json
// @Produce      json
// @Param        id       path      string                true  "项目 ID"
// @Param        request  body      updateProjectRequest  true  "更新请求"
// @Success      200      {object}  map[string]interface{}  "更新后的项目"
// @Failure      400      {object}  errors.AppError         "参数错误"
// @Failure      404      {object}  errors.AppError         "项目不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /projects/{id} [put]
func (h *ProjectHandler) UpdateProject(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, ok := projectTenantID(c)
	if !ok {
		_ = c.Error(errors.NewUnauthorizedError("Unauthorized"))
		return
	}
	var request updateProjectRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		_ = c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	project, err := h.projectService.Update(ctx, tenantID, c.Param("id"), service.UpdateProjectInput{
		Name:        request.Name,
		Description: request.Description,
		SortOrder:   request.SortOrder,
	})
	if err != nil {
		writeProjectError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": project})
}

// deleteProjectRequest selects what happens to the conversations inside the
// project being deleted. Omitting it keeps the safe default: refuse while any
// session remains, so a mis-click cannot silently destroy a transcript.
type deleteProjectRequest struct {
	// Sessions is "refuse" (default), "move" or "delete".
	Sessions string `json:"sessions"`
	// MoveTo is the destination project when Sessions == "move". Empty detaches
	// the sessions (they keep existing, just without a project).
	MoveTo string `json:"move_to"`
}

// DeleteProject godoc
// @Summary      删除项目
// @Description  删除项目及其数据库记录。本地目录永远不会被删除。
// @Tags         项目
// @Accept       json
// @Produce      json
// @Param        id       path      string                true  "项目 ID"
// @Param        request  body      deleteProjectRequest  false  "会话处理方式"
// @Success      200      {object}  map[string]interface{}  "删除成功"
// @Failure      400      {object}  errors.AppError         "项目下仍有会话"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /projects/{id} [delete]
func (h *ProjectHandler) DeleteProject(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, ok := projectTenantID(c)
	if !ok {
		_ = c.Error(errors.NewUnauthorizedError("Unauthorized"))
		return
	}
	// A DELETE with no body is the common case; treat an empty body as the
	// safe default instead of failing on EOF.
	var request deleteProjectRequest
	_ = c.ShouldBindJSON(&request)

	mode := service.ProjectSessionsRefuse
	switch strings.ToLower(strings.TrimSpace(request.Sessions)) {
	case "", "refuse":
		mode = service.ProjectSessionsRefuse
	case "move":
		mode = service.ProjectSessionsMove
	case "delete":
		mode = service.ProjectSessionsDelete
	default:
		_ = c.Error(errors.NewBadRequestError("未知的会话处理方式"))
		return
	}

	if err := h.projectService.Delete(ctx, tenantID, c.Param("id"), service.DeleteInput{
		Sessions:        mode,
		MoveToProjectID: request.MoveTo,
	}); err != nil {
		writeProjectError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

type setDefaultProjectRequest struct {
	Default bool `json:"default"`
}

// SetDefaultProject godoc
// @Summary      设为默认项目
// @Description  把项目设为新建会话默认归属的项目。
// @Tags         项目
// @Accept       json
// @Produce      json
// @Param        id       path      string                   true  "项目 ID"
// @Param        request  body      setDefaultProjectRequest true  "是否设为默认"
// @Success      200      {object}  map[string]interface{}  "设置成功"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /projects/{id}/default [put]
func (h *ProjectHandler) SetDefaultProject(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, ok := projectTenantID(c)
	if !ok {
		_ = c.Error(errors.NewUnauthorizedError("Unauthorized"))
		return
	}
	var request setDefaultProjectRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		_ = c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	if request.Default {
		if err := h.projectService.SetDefault(ctx, tenantID, c.Param("id")); err != nil {
			writeProjectError(c, err)
			return
		}
	} else if err := h.projectService.ClearDefault(ctx, tenantID); err != nil {
		writeProjectError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// writeProjectError forwards the service's typed errors to Gin's error
// middleware, which renders them with the status code carried on the AppError.
// Anything untyped (a driver failure, a wrapped context error) becomes a 500 so
// internals never leak through a 400 body.
//
// Keeping the mapping in one place means the UI can rely on 403 for "directory
// not approved" and 400 for "name is empty" regardless of which endpoint it hit.
func writeProjectError(c *gin.Context, err error) {
	var appErr *errors.AppError
	if stderrors.As(err, &appErr) {
		_ = c.Error(appErr)
		return
	}
	logger.ErrorWithFields(c.Request.Context(), err, nil)
	_ = c.Error(errors.NewInternalServerError("项目操作失败"))
}
