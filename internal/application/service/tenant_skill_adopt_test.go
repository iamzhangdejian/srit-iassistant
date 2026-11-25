package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/localsandbox/skilltree"
	"github.com/Tencent/WeKnora/internal/types"
)

// writeLocalSkill puts a servable skill in the tree, the way an install leaves
// one behind.
func writeLocalSkill(t *testing.T, tree *skilltree.Tree, dir, name, description string) {
	t.Helper()
	version := filepath.Join(tree.VersionsRoot(), dir+"-1")
	require.NoError(t, os.MkdirAll(version, 0o755))
	body := "---\nname: " + name + "\ndescription: " + description +
		"\n---\n\n# 说明\n\n正文。\n"
	require.NoError(t, os.WriteFile(filepath.Join(version, "SKILL.md"), []byte(body), 0o644))
	_, err := tree.Activate(dir, version)
	require.NoError(t, err)
}

// A local tree that still serves a skill is the truth about what a chat can
// read; a lost install row must not make it disappear from the picker.
func TestAdoptOrphanHostSkillsRegistersFilesWithoutRow(t *testing.T) {
	tree, err := skilltree.New(filepath.Join(t.TempDir(), ".weknora", "skills"))
	require.NoError(t, err)
	writeLocalSkill(t, tree, "office-docs", "office-docs", "生成办公文档")

	repo := newInstallSkillRepo()
	repo.catalogs["cat-1"] = &types.TenantSkillCatalogEntity{
		ID: "cat-1", TenantID: 7, Name: "office-docs",
	}

	adopted := adoptOrphanHostSkills(context.Background(), repo, tree, 7, nil)

	require.Len(t, adopted, 1)
	row := adopted[0]
	require.Equal(t, "office-docs", row.Name)
	require.Equal(t, "生成办公文档", row.Description)
	require.Contains(t, row.Instructions, "正文。")
	require.Equal(t, "cat-1", row.CatalogID)
	require.Equal(t, "host", row.SandboxConfigID)
	require.Equal(t, uint64(7), row.TenantID)
	require.Equal(t, types.SkillStatusReady, row.Status)
	require.True(t, row.Enabled)
	require.NotEmpty(t, row.ID)

	stored, err := repo.ListSkillsByConfig(context.Background(), 7, "host")
	require.NoError(t, err)
	require.Len(t, stored, 1)
	require.Equal(t, "office-docs", stored[0].Name)
}

// A row that exists is a decision: disabled stays disabled, failed stays
// failed, and adoption adds nothing.
func TestAdoptOrphanHostSkillsLeavesExistingRowsAlone(t *testing.T) {
	tree, err := skilltree.New(filepath.Join(t.TempDir(), ".weknora", "skills"))
	require.NoError(t, err)
	writeLocalSkill(t, tree, "pdf", "pdf", "处理 PDF")
	writeLocalSkill(t, tree, "office-docs", "office-docs", "生成办公文档")

	repo := newInstallSkillRepo()
	disabled := &types.TenantSkillEntity{
		ID: "row-pdf", TenantID: 7, SandboxConfigID: "host", Name: "pdf",
		Enabled: false, Status: types.SkillStatusReady,
	}
	repo.skills[skillKey(7, "host", "row-pdf")] = disabled

	adopted := adoptOrphanHostSkills(context.Background(), repo, tree, 7,
		[]*types.TenantSkillEntity{disabled})

	require.Len(t, adopted, 1)
	require.Equal(t, "office-docs", adopted[0].Name)

	rows, err := repo.ListSkillsByConfig(context.Background(), 7, "host")
	require.NoError(t, err)
	require.Len(t, rows, 2)
	for _, row := range rows {
		if row.Name == "pdf" {
			require.False(t, row.Enabled, "adoption must not re-enable a disabled skill")
		}
	}
}

// A tree entry that cannot be read as a servable skill is left out rather
// than registered under a name chats cannot resolve.
func TestAdoptOrphanHostSkillsSkipsUnreadableEntries(t *testing.T) {
	tree, err := skilltree.New(filepath.Join(t.TempDir(), ".weknora", "skills"))
	require.NoError(t, err)
	// Directory name and frontmatter name disagree.
	writeLocalSkill(t, tree, "office", "office-docs", "生成办公文档")

	repo := newInstallSkillRepo()
	adopted := adoptOrphanHostSkills(context.Background(), repo, tree, 7, nil)
	require.Empty(t, adopted)

	rows, err := repo.ListSkillsByConfig(context.Background(), 7, "host")
	require.NoError(t, err)
	require.Empty(t, rows)
}

// A lister that cannot write (the narrow interfaces some callers hold) must
// leave adoption off instead of failing the turn.
func TestAdoptOrphanHostSkillsNeedsWritableRepository(t *testing.T) {
	tree, err := skilltree.New(filepath.Join(t.TempDir(), ".weknora", "skills"))
	require.NoError(t, err)
	writeLocalSkill(t, tree, "office-docs", "office-docs", "生成办公文档")

	require.Empty(t, adoptOrphanHostSkills(context.Background(),
		listOnlySkills{}, tree, 7, nil))
}

// listOnlySkills implements the read side alone.
type listOnlySkills struct{}

func (listOnlySkills) ListSkillsByConfig(
	context.Context, uint64, string,
) ([]*types.TenantSkillEntity, error) {
	return nil, nil
}

// The picker and the chat turn both ask hostSkillsForRun what this machine
// offers, so the repair has to land there: a skill whose files are on disk
// must come back even though its row was lost.
func TestHostSkillsForRunAdoptsFilesWithoutRow(t *testing.T) {
	tree, err := skilltree.New(filepath.Join(t.TempDir(), ".weknora", "skills"))
	require.NoError(t, err)
	writeLocalSkill(t, tree, "office-docs", "office-docs", "生成办公文档")

	repo := newInstallSkillRepo()
	configID, rows := hostSkillsForRun(context.Background(), repo, tree, 7)

	require.Equal(t, "host", configID)
	require.Len(t, rows, 1)
	require.Equal(t, "office-docs", rows[0].Name)
	require.Equal(t, "生成办公文档", rows[0].Description)
	require.Equal(t, types.SkillStatusReady, rows[0].Status)

	// Second call is a plain read: the row is there now, nothing is written
	// twice.
	stored, err := repo.ListSkillsByConfig(context.Background(), 7, "host")
	require.NoError(t, err)
	require.Len(t, stored, 1)
	_, rows = hostSkillsForRun(context.Background(), repo, tree, 7)
	require.Len(t, rows, 1)
	stored, err = repo.ListSkillsByConfig(context.Background(), 7, "host")
	require.NoError(t, err)
	require.Len(t, stored, 1)
}
