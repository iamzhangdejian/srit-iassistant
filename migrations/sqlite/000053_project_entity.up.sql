-- 项目（Project）：工作空间 → 项目 → 会话 三层结构中的中间层。
--
-- 设计要点：
--   · 一个项目 = 一个本地目录（host_dir），1:1 关联，不做多目录。
--   · name 默认取 host_dir 的 basename，允许用户改名；改名不会反向改目录名，
--     因为目录名变了会让所有历史会话的 host_workspace_dir 失效。
--   · is_default 标记「新建会话默认落到哪个项目」。同一租户下只允许一个，
--     由下面 uq_projects_default_per_tenant 唯一索引保证（DB 层强制，不靠应用逻辑）。
--   · host_dir 存绝对路径，与 sessions.host_workspace_dir 同款。写入前必须已通过
--     MatchApprovedDir 白名单校验（用户经系统目录选择框授权过的根目录）。
CREATE TABLE IF NOT EXISTS projects (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    name VARCHAR(200) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    -- 关联的本地绝对目录；一个项目只关联一个目录
    host_dir VARCHAR(1024) NOT NULL,
    -- 1 = 新建会话默认归属该项目；每租户至多一个
    is_default BOOLEAN NOT NULL DEFAULT 0,
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    deleted_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_projects_tenant_id ON projects(tenant_id);
CREATE INDEX IF NOT EXISTS idx_projects_deleted_at ON projects(deleted_at);

-- 同一租户下同一个目录只能有一个未删除的项目：
-- 防止用户在侧栏连点两次「＋」建出两个同名项目。
CREATE UNIQUE INDEX IF NOT EXISTS uq_projects_tenant_dir
    ON projects(tenant_id, host_dir) WHERE deleted_at IS NULL;

-- 同一租户下只允许一个默认项目。
-- 注意：SQLite 部分索引的条件表达式不能直接约束「最多一行 is_default=1」，
-- 这里用 (tenant_id) 上的唯一索引配合应用层先清后设来保证；
-- 若同一租户出现第二个 is_default=1，写入会直接失败而不是静默产生两份默认。
CREATE UNIQUE INDEX IF NOT EXISTS uq_projects_default_per_tenant
    ON projects(tenant_id) WHERE is_default = 1 AND deleted_at IS NULL;

-- sessions 增加 project_id，把会话挂到项目下。
-- 可空：历史会话与 API 直接创建的会话可能没有项目，应用层在 CreateSession 时
-- 兜底到默认项目；这里不用 NOT NULL，避免 ALTER TABLE 需要回填、也避免外部
-- 写入路径因缺少项目而硬失败。
ALTER TABLE sessions ADD COLUMN project_id VARCHAR(36);
CREATE INDEX IF NOT EXISTS idx_sessions_project_id ON sessions(project_id);