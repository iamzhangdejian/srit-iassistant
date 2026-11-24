-- 项目（Project）：工作空间 → 项目 → 会话 三层结构中的中间层。
-- 与 migrations/sqlite/000053_project_entity 等价，类型按 Postgres 习惯换成 TIMESTAMPTZ。
CREATE TABLE IF NOT EXISTS projects (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    name VARCHAR(200) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    host_dir VARCHAR(1024) NOT NULL,
    is_default BOOLEAN NOT NULL DEFAULT FALSE,
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    deleted_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_projects_tenant_id ON projects(tenant_id);
CREATE INDEX IF NOT EXISTS idx_projects_deleted_at ON projects(deleted_at);

CREATE UNIQUE INDEX IF NOT EXISTS uq_projects_tenant_dir
    ON projects(tenant_id, host_dir) WHERE deleted_at IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS uq_projects_default_per_tenant
    ON projects(tenant_id) WHERE is_default = TRUE AND deleted_at IS NULL;

ALTER TABLE sessions ADD COLUMN IF NOT EXISTS project_id VARCHAR(36);
CREATE INDEX IF NOT EXISTS idx_sessions_project_id ON sessions(project_id);