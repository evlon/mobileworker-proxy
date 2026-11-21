// Package dsh 负责把模型写进 DeepSeek Harness 的 profile 配置。
package dsh

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Profile 是一个 DSH profile。
type Profile struct {
	Name     string // 目录名，如 desktop / web
	Dir      string
	Patch    string // cordis.patch.yml 的完整路径
	HasPatch bool   // 该文件是否存在
	Active   bool   // 是否为当前 DSH_PROFILE
}

// HomeDir 返回 DSH 家目录。
func HomeDir() string {
	if v := strings.TrimSpace(os.Getenv("DSH_HOME")); v != "" {
		return v
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return ".dsh"
	}
	return filepath.Join(h, ".dsh")
}

// Locate 动态探测本机所有 DSH profile（不写死 desktop/web/headless）。
// 当前 profile 排在最前，便于作为默认项。
func Locate() ([]Profile, error) {
	root := filepath.Join(HomeDir(), "profiles")
	ents, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	active := strings.TrimSpace(os.Getenv("DSH_PROFILE"))
	var out []Profile
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		// 跳过备份目录（_backup-…）与隐藏目录（.plugin-manager 等）
		if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
			continue
		}
		dir := filepath.Join(root, name)
		patch := filepath.Join(dir, "cordis.patch.yml")
		_, statErr := os.Stat(patch)
		out = append(out, Profile{
			Name:     name,
			Dir:      dir,
			Patch:    patch,
			HasPatch: statErr == nil,
			Active:   active != "" && name == active,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Active != out[j].Active {
			return out[i].Active
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}
