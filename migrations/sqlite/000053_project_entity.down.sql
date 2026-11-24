-- 回滚 000053_project_entity。
-- 注意：down 只删库结构，磁盘上的本地项目目录不受任何影响（程序从不删用户文件）。

DROP INDEX IF EXISTS idx_sessions_project_id;
ALTER TABLE sessions DROP COLUMN project_id;

DROP INDEX IF EXISTS uq_projects_default_per_tenant;
DROP INDEX IF EXISTS uq_projects_tenant_dir;
DROP INDEX IF EXISTS idx_projects_deleted_at;
DROP INDEX IF EXISTS idx_projects_tenant_id;
DROP TABLE IF EXISTS projects;