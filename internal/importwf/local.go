package importwf

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

func normalizeModelPath(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if value == "" {
		return ""
	}
	clean := path.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return ""
	}
	return strings.TrimPrefix(clean, "/")
}

func findLocalWeight(roots []string, want, preferFolder string) string {
	want = normalizeModelPath(want)
	if want == "" || strings.Contains(want, "..") {
		return ""
	}
	base := path.Base(want)
	generic := genericWeights[strings.ToLower(base)]
	var exact, named []string
	for _, root := range roots {
		if root == "" {
			continue
		}
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				n := d.Name()
				if n == ".git" || n == ".cache" || n == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(d.Name(), ".part") {
				return nil
			}
			if info, err := d.Info(); err != nil || info.Size() == 0 {
				return nil
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return nil
			}
			rel = filepath.ToSlash(rel)
			if rel == want || strings.Contains(want, "/") && strings.HasSuffix(rel, "/"+want) {
				exact = append(exact, p)
				return nil
			}
			if !generic && d.Name() == base {
				named = append(named, p)
			}
			return nil
		})
	}
	if len(exact) > 0 {
		return exact[0]
	}
	named = preferFolderHits(named, preferFolder)
	if len(named) == 1 {
		return named[0]
	}
	return ""
}

func preferFolderHits(paths []string, folder string) []string {
	if folder == "" || len(paths) < 2 {
		return paths
	}
	var hit []string
	for _, p := range paths {
		if pathHasDir(p, folder) {
			hit = append(hit, p)
		}
	}
	if len(hit) > 0 {
		return hit
	}
	return paths
}

func pathHasDir(p, folder string) bool {
	for _, part := range strings.Split(filepath.ToSlash(p), "/") {
		if part == folder {
			return true
		}
	}
	return false
}

func expectedDest(modelsDir, class, field, value string) string {
	value = normalizeModelPath(value)
	if value == "" {
		return filepath.Join(modelsDir, modelFolder(class, field))
	}
	return filepath.Join(modelsDir, modelFolder(class, field), filepath.FromSlash(value))
}

func destInRoot(dest, root string) bool {
	cleanDest, cleanRoot := filepath.Clean(dest), filepath.Clean(root)
	sep := string(os.PathSeparator)
	return cleanDest == cleanRoot || strings.HasPrefix(cleanDest, cleanRoot+sep)
}

func linkOrReuse(found, dest string) error {
	found, dest = filepath.Clean(found), filepath.Clean(dest)
	if found == dest {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if st, err := os.Lstat(dest); err == nil {
		if st.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		if st.Size() > 0 {
			return nil
		}
		if err := os.Remove(dest); err != nil {
			return err
		}
	}
	if err := os.Symlink(found, dest); err == nil {
		return nil
	}
	return os.Link(found, dest)
}

func reuseLocalModel(modelsDir, class, field, value string) (found, dest string, ok bool) {
	if modelsDir == "" {
		return "", "", false
	}
	dest = expectedDest(modelsDir, class, field, value)
	if !destInRoot(dest, modelsDir) {
		return "", "", false
	}
	if st, err := os.Stat(dest); err == nil && st.Size() > 0 {
		return dest, dest, true
	}
	found = findLocalWeight([]string{modelsDir}, value, modelFolder(class, field))
	if found == "" {
		return "", dest, false
	}
	if err := linkOrReuse(found, dest); err != nil {
		return found, dest, false
	}
	return found, dest, true
}
