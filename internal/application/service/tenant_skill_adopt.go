package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/uuid"

	"github.com/Tencent/WeKnora/internal/agent/skills"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

// hostSkillAdopter is the repository slice adoption needs. The real repository
// implements all of it; the read-only listers used by some callers do not, and
// adoption is then skipped rather than failing the turn.
type hostSkillAdopter interface {
	installedSkillLister
	CreateSkill(ctx context.Context, e *types.TenantSkillEntity) error
	GetCatalogByName(ctx context.Context, tenantID uint64, name string) (*types.TenantSkillCatalogEntity, error)
}

// hostSkillSelfInstallLimit bounds how many skills one turn may adopt. A tree
// far larger than this is not the drift this heals; it is a root pointed at
// something else, and registering hundreds of rows would be worse than
// leaving them alone.
const hostSkillSelfInstallLimit = 32

// adoptOrphanHostSkills re-registers skills whose files are still in the local
// tree but whose install row is gone.
//
// Lite reads two things to decide what a chat may use: the local tree, for the
// files a skill runs, and the install row, for what the picker offers and what
// the model is told exists. The two drift apart whenever the data directory
// changes - portable <-> regular mode, a restored backup, a deleted database -
// because the skill tree lives under the user's home and survives all of it.
// The skill then keeps its files but vanishes from the picker, which reads as
// "the plugin I installed is gone".
//
// Only names with no row at all are adoped: a row that exists but is disabled
// or failed is a decision by the administrator or the installer, and adoption
// must not undo it.
func adoptOrphanHostSkills(
	ctx context.Context,
	lister installedSkillLister,
	tree HostSkillTree,
	tenantID uint64,
	known []*types.TenantSkillEntity,
) []*types.TenantSkillEntity {
	adopter, ok := lister.(hostSkillAdopter)
	if !ok || tree == nil || tenantID == 0 {
		return nil
	}
	names, err := tree.Names()
	if err != nil {
		logger.Warnf(ctx, "[skill] list the local skill tree failed: %v", err)
		return nil
	}
	if len(names) > hostSkillSelfInstallLimit {
		logger.Warnf(ctx, "[skill] local skill tree has %d entries; skipping install-record repair",
			len(names))
		return nil
	}
	knownNames := make(map[string]bool, len(known))
	for _, row := range known {
		if row != nil {
			knownNames[row.Name] = true
		}
	}
	var adopted []*types.TenantSkillEntity
	for _, name := range names {
		if knownNames[name] || !sandbox.IsValidSkillName(name) {
			continue
		}
		parsed, err := readLocalSkill(tree, name)
		if err != nil {
			logger.Warnf(ctx, "[skill] local skill %s cannot be adopted: %v", name, err)
			continue
		}
		row := &types.TenantSkillEntity{
			ID:              uuid.NewString(),
			TenantID:        tenantID,
			SandboxConfigID: sandbox.HostSkillTargetID,
			Name:            name,
			Description:     parsed.Description,
			Instructions:    parsed.Instructions,
			Enabled:         true,
			Status:          types.SkillStatusReady,
		}
		if catalog, err := adopter.GetCatalogByName(ctx, tenantID, name); err == nil && catalog != nil {
			row.CatalogID = catalog.ID
		}
		if err := adopter.CreateSkill(ctx, row); err != nil {
			logger.Warnf(ctx, "[skill] record local skill %s failed: %v", name, err)
			continue
		}
		logger.Infof(ctx,
			"[skill] adopted local skill %s: its files were on disk with no install record", name)
		adopted = append(adopted, row)
	}
	return adopted
}

// readLocalSkill parses <root>/<name>/SKILL.md.
//
// The directory name is the identity chats resolve a skill by, so a bundle
// whose frontmatter names something else is refused instead of registered
// under a name the tree cannot serve.
func readLocalSkill(tree HostSkillTree, name string) (*skills.Skill, error) {
	path := filepath.Join(tree.Root(), name, skills.SkillFileName)
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	parsed, err := skills.ParseSkillFile(string(content))
	if err != nil {
		return nil, err
	}
	if parsed.Name != name {
		return nil, fmt.Errorf("%s declares name %q, expected %q", path, parsed.Name, name)
	}
	return parsed, nil
}