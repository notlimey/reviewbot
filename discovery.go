package main

import (
	"bufio"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func runDiscovery(db *sql.DB, projectRoot string, prog Progress) error {
	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return fmt.Errorf("resolve project root: %w", err)
	}

	// If it's a git repo, use git to get non-ignored files
	if isGitRepo(absRoot) {
		prog.Info("Git repo detected — respecting .gitignore")
		return discoverWithGit(db, absRoot, prog)
	}

	return discoverWithWalk(db, absRoot, prog)
}

// discoverWithGit uses `git ls-files` to list only non-ignored files.
func discoverWithGit(db *sql.DB, absRoot string, prog Progress) error {
	var totalFound, newChanged, skipped int

	// -co: cached (tracked) + others (untracked but not ignored)
	// --exclude-standard: respect .gitignore, .git/info/exclude, global gitignore
	cmd := exec.Command("git", "ls-files", "-co", "--exclude-standard")
	cmd.Dir = absRoot

	out, err := cmd.Output()
	if err != nil {
		prog.Warn(fmt.Sprintf("git ls-files failed, falling back to walk: %v", err))
		return discoverWithWalk(db, absRoot, prog)
	}

	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		relPath := scanner.Text()

		ext := filepath.Ext(relPath)
		lang, ok := extensions[ext]
		if !ok {
			continue
		}

		// Still skip files inside skipDirs
		if isInSkipDir(relPath) {
			continue
		}

		totalFound++

		absPath := filepath.Join(absRoot, relPath)
		content, err := os.ReadFile(absPath)
		if err != nil {
			prog.Warn(fmt.Sprintf("skip (read error): %s", relPath))
			continue
		}

		hash := sha256Hash(content)
		tokenEstimate := len(content) / 4

		var existingHash string
		err = db.QueryRow("SELECT hash FROM files WHERE path = ?", relPath).Scan(&existingHash)
		if err == nil && existingHash == hash {
			skipped++
			continue
		}

		newChanged++

		if err == sql.ErrNoRows {
			_, err = db.Exec(
				"INSERT INTO files (path, language, hash, token_estimate, status) VALUES (?, ?, ?, ?, 'pending')",
				relPath, lang, hash, tokenEstimate,
			)
		} else {
			_, err = db.Exec(
				"UPDATE files SET hash = ?, token_estimate = ?, status = 'pending', language = ? WHERE path = ?",
				hash, tokenEstimate, lang, relPath,
			)
		}
		if err != nil {
			prog.Warn(fmt.Sprintf("db error for %s: %v", relPath, err))
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan git output: %w", err)
	}

	prog.DiscoveryComplete(totalFound, newChanged, skipped)
	return nil
}

// discoverWithWalk is the fallback for non-git directories.
func discoverWithWalk(db *sql.DB, absRoot string, prog Progress) error {
	var totalFound, newChanged, skipped int

	err := filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}

		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}

		ext := filepath.Ext(d.Name())
		lang, ok := extensions[ext]
		if !ok {
			return nil
		}

		totalFound++

		content, err := os.ReadFile(path)
		if err != nil {
			prog.Warn(fmt.Sprintf("skip (read error): %s", path))
			return nil
		}

		hash := sha256Hash(content)
		relPath, _ := filepath.Rel(absRoot, path)
		tokenEstimate := len(content) / 4

		var existingHash string
		err = db.QueryRow("SELECT hash FROM files WHERE path = ?", relPath).Scan(&existingHash)
		if err == nil && existingHash == hash {
			skipped++
			return nil
		}

		newChanged++

		if err == sql.ErrNoRows {
			_, err = db.Exec(
				"INSERT INTO files (path, language, hash, token_estimate, status) VALUES (?, ?, ?, ?, 'pending')",
				relPath, lang, hash, tokenEstimate,
			)
		} else {
			_, err = db.Exec(
				"UPDATE files SET hash = ?, token_estimate = ?, status = 'pending', language = ? WHERE path = ?",
				hash, tokenEstimate, lang, relPath,
			)
		}
		if err != nil {
			prog.Warn(fmt.Sprintf("db error for %s: %v", relPath, err))
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("walk directory: %w", err)
	}

	prog.DiscoveryComplete(totalFound, newChanged, skipped)
	return nil
}

func isGitRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

func isInSkipDir(relPath string) bool {
	parts := strings.Split(filepath.ToSlash(relPath), "/")
	for _, p := range parts {
		if skipDirs[p] {
			return true
		}
	}
	return false
}

func sha256Hash(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
