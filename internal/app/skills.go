package app

import (
	"context"
	"log"
	"os"
	"path/filepath"

	"github.com/DonScott603/gogoclaw/internal/skill"
	"github.com/DonScott603/gogoclaw/internal/tools"
)

// SkillDeps holds the skill registry, runtime, and dispatcher.
type SkillDeps struct {
	Registry   *skill.Registry
	Runtime    *skill.Runtime
	Dispatcher *skill.SkillDispatcher
	Lister     tools.SkillLister
	closeFn    func()
}

// InitSkills sets up the skill registry, WASM runtime, and skill dispatcher.
func InitSkills(configDir string) SkillDeps {
	skillsDir := filepath.Join(configDir, "skills.d")
	reg, err := skill.NewRegistry(skillsDir)
	if err != nil {
		log.Printf("skills: user skills scan failed: %v", err)
		reg, _ = skill.NewRegistry(os.TempDir())
	}

	builtinDir := resolveBuiltinSkillsDir(configDir)
	if builtinDir != "" {
		log.Printf("skills: loading built-in skills from %s", builtinDir)
		if builtinReg, err := skill.NewRegistry(builtinDir); err == nil {
			for _, s := range builtinReg.ListSkills() {
				reg.AddSkill(s)
			}
		}
	} else {
		log.Printf("skills: no built-in skills directory found")
	}

	allSkills := reg.ListSkills()
	log.Printf("skills: found %d skill(s)", len(allSkills))
	for _, s := range allSkills {
		log.Printf("skills:   %s v%s (%d tools)", s.Manifest.Name, s.Manifest.Version, len(s.Manifest.Tools))
	}

	ctx := context.Background()
	rt, err := skill.NewRuntime(ctx)
	if err != nil {
		log.Printf("skills: runtime init failed: %v (continuing without skills)", err)
		return SkillDeps{
			Registry: reg,
			Lister:   tools.NoOpSkillLister{},
			closeFn:  func() {},
		}
	}

	sd := skill.NewSkillDispatcher(reg, rt)

	return SkillDeps{
		Registry:   reg,
		Runtime:    rt,
		Dispatcher: sd,
		Lister:     sd,
		closeFn:    func() { rt.Close(context.Background()) },
	}
}

// Close shuts down the WASM runtime.
func (d *SkillDeps) Close() {
	d.closeFn()
}

func resolveBuiltinSkillsDir(configDir string) string {
	var candidates []string

	if exePath, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exePath)
		candidates = append(candidates,
			filepath.Join(exeDir, "skills", "builtin"),
			filepath.Join(exeDir, "..", "skills", "builtin"),
		)
	}

	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(cwd, "skills", "builtin"))
	}

	candidates = append(candidates, filepath.Join(configDir, "skills", "builtin"))

	for _, dir := range candidates {
		dir = filepath.Clean(dir)
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	return ""
}
