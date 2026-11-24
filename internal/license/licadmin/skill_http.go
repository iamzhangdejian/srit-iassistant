package licadmin

// 技能管理接口。鉴权口径与授权接口一致（会话或自动化令牌均可），
// 但发布/删除这类会改变交付内容的动作要求会话 —— 阶段 2 定下的规矩是
// 「令牌只能发码查台账」，不能让它悄悄改客户端里装什么。

import (
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// handleSkillList 返回全部技能。
func (s *Server) handleSkillList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "只支持 GET")
		return
	}
	items, err := s.skills.List()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"skills": items})
}

// handleSkillValidate 只校验、不入库：上传前先看看包有没有问题。
func (s *Server) handleSkillValidate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "只支持 POST")
		return
	}
	name, data, err := s.readSkillUpload(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	val, files, err := ValidateSkillZip(name, data)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.auditAs(s.actor(r), r, "校验技能包", name, summarizeFindings(val.Findings))
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          val.OK(),
		"name":        val.Name,
		"title":       val.Title,
		"description": val.Description,
		"version":     val.Version,
		"author":      val.Author,
		"file_count":  val.FileCount,
		"size_bytes":  val.SizeBytes,
		"files":       files,
		"findings":    val.Findings,
		"error_count": countFindings(val.Findings, skillFindingError),
		"warn_count":  countFindings(val.Findings, skillFindingWarn),
	})
}

// handleSkillUpload 校验并入库。
func (s *Server) handleSkillUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "只支持 POST")
		return
	}
	name, data, err := s.readSkillUpload(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	pkg, err := s.skills.Import(name, data, time.Now())
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.auditAs(s.actor(r), r, "导入技能包", pkg.Name,
		describeSkill(pkg))
	writeJSON(w, http.StatusOK, map[string]any{"skill": pkg})
}

// handleSkillAction 处理 /api/skills/{id}[/action]。
func (s *Server) handleSkillAction(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/skills/")
	rest = strings.Trim(rest, "/")
	if rest == "" {
		s.handleSkillList(w, r)
		return
	}
	parts := strings.Split(rest, "/")
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "技能 id 不合法")
		return
	}
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}
	switch action {
	case "":
		if r.Method != http.MethodGet {
			writeJSONError(w, http.StatusMethodNotAllowed, "只支持 GET")
			return
		}
		pkg, err := s.skills.Get(id)
		if err != nil {
			writeJSONError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"skill": pkg})
	case "publish":
		s.requireSessionHTTP(w, r, func() {
			pkg, err := s.skills.Publish(id, time.Now())
			if err != nil {
				writeJSONError(w, http.StatusBadRequest, err.Error())
				return
			}
			s.auditAs(s.actor(r), r, "发布技能", pkg.Name, describeSkill(pkg))
			writeJSON(w, http.StatusOK, map[string]any{"skill": pkg})
		})
	case "unpublish":
		s.requireSessionHTTP(w, r, func() {
			pkg, err := s.skills.Unpublish(id)
			if err != nil {
				writeJSONError(w, http.StatusBadRequest, err.Error())
				return
			}
			s.auditAs(s.actor(r), r, "撤回技能", pkg.Name, describeSkill(pkg))
			writeJSON(w, http.StatusOK, map[string]any{"skill": pkg})
		})
	case "delete":
		s.requireSessionHTTP(w, r, func() {
			pkg, err := s.skills.Get(id)
			if err != nil {
				writeJSONError(w, http.StatusNotFound, err.Error())
				return
			}
			if err := s.skills.Delete(id); err != nil {
				writeJSONError(w, http.StatusBadRequest, err.Error())
				return
			}
			s.auditAs(s.actor(r), r, "删除技能", pkg.Name, describeSkill(pkg))
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		})
	default:
		writeJSONError(w, http.StatusNotFound, "未知操作")
	}
}

// handleSkillBundle 导出已发布技能包。
func (s *Server) handleSkillBundle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "只支持 GET/POST")
		return
	}
	data, name, err := s.skills.Bundle(time.Now())
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.auditAs(s.actor(r), r, "导出技能包", name, strconv.Itoa(len(data))+" 字节")
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// requireSessionHTTP 在内层 handler 前再校一次「必须是账号会话」。
//
// 路由表上这些动作已经挂在 requireSession 下，这里多一层是因为
// handleSkillAction 是一个方法处理多种动作，将来若有人把路由挪到
// requireAuth 上，越权会静默发生。
func (s *Server) requireSessionHTTP(w http.ResponseWriter, r *http.Request, fn func()) {
	if s.auth != nil {
		if _, _, ok := s.sessionUser(r, time.Now()); !ok {
			writeJSONError(w, http.StatusUnauthorized, "该操作需要账号登录")
			return
		}
	}
	fn()
}

// readSkillUpload 从 multipart 或裸 body 读一个 zip。
func (s *Server) readSkillUpload(r *http.Request) (string, []byte, error) {
	r.Body = http.MaxBytesReader(nil, r.Body, maxSkillZipBytes+1024)
	ctype := r.Header.Get("Content-Type")
	if strings.HasPrefix(ctype, "multipart/form-data") {
		if err := r.ParseMultipartForm(maxSkillZipBytes); err != nil {
			return "", nil, errors.New("解析上传内容失败（单个技能包上限 32 MB）")
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			return "", nil, errors.New("缺少 file 字段")
		}
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, maxSkillZipBytes+1))
		if err != nil {
			return "", nil, errors.New("读取上传文件失败")
		}
		if int64(len(data)) > maxSkillZipBytes {
			return "", nil, errors.New("技能包超过 32 MB 上限")
		}
		return header.Filename, data, nil
	}
	// 裸 body 便于 curl：name 从 query 取，缺省用 skill。
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		name = "skill.zip"
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, maxSkillZipBytes+1))
	if err != nil {
		return "", nil, errors.New("读取请求体失败")
	}
	if int64(len(data)) > maxSkillZipBytes {
		return "", nil, errors.New("技能包超过 32 MB 上限")
	}
	return name, data, nil
}

func countFindings(fs []SkillFinding, level string) int {
	n := 0
	for _, f := range fs {
		if f.Level == level {
			n++
		}
	}
	return n
}

func summarizeFindings(fs []SkillFinding) string {
	if len(fs) == 0 {
		return "无问题"
	}
	return strconv.Itoa(countFindings(fs, skillFindingError)) + " 个 error / " +
		strconv.Itoa(countFindings(fs, skillFindingWarn)) + " 个 warn"
}

func describeSkill(p SkillPackage) string {
	var b strings.Builder
	b.WriteString(p.Version)
	if p.Status == skillStatusPublished {
		b.WriteString(" · 已发布")
	} else {
		b.WriteString(" · 草稿")
	}
	b.WriteString(" · ")
	b.WriteString(strconv.Itoa(p.FileCount))
	b.WriteString(" 个文件")
	if !p.CheckedOK {
		b.WriteString(" · 校验未通过")
	}
	return b.String()
}

// skillUploadDirFor 仅用于测试与外部脚本定位解包目录。
func skillUploadDirFor(dataDir string) string {
	return dataDir + string(os.PathSeparator) + "skills"
}
