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

func runDiscovery(db *sql.DB, projectRoot string) error {
	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return fmt.Errorf("resolve project root: %w", err)
	}

	// If it's a git repo, use git to get non-ignored files
	if isGitRepo(absRoot) {
		fmt.Println("Git repo detected — respecting .gitignore")
		return discoverWithGit(db, absRoot)
	}

	return discoverWithWalk(db, absRoot)
}

// discoverWithGit uses `git ls-files` to list only non-ignored files.
func discoverWithGit(db *sql.DB, absRoot string) error {
	var totalFound, newChanged, skipped int

	// -co: cached (tracked) + others (untracked but not ignored)
	// --exclude-standard: respect .gitignore, .git/info/exclude, global gitignore
	cmd := exec.Command("git", "ls-files", "-co", "--exclude-standard")
	cmd.Dir = absRoot

	out, err := cmd.Output()
	if err != nil {
		fmt.Printf("  git ls-files failed, falling back to walk: %v\n", err)
		return discoverWithWalk(db, absRoot)
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
			fmt.Printf("  skip (read error): %s\n", relPath)
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
			fmt.Printf("  db error for %s: %v\n", relPath, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan git output: %w", err)
	}

	fmt.Printf("Discovery complete: %d files found, %d new/changed, %d unchanged (skipped)\n",
		totalFound, newChanged, skipped)
	return nil
}

// discoverWithWalk is the fallback for non-git directories.
func discoverWithWalk(db *sql.DB, absRoot string) error {
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
			fmt.Printf("  skip (read error): %s\n", path)
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
			fmt.Printf("  db error for %s: %v\n", relPath, err)
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("walk directory: %w", err)
	}

	fmt.Printf("Discovery complete: %d files found, %d new/changed, %d unchanged (skipped)\n",
		totalFound, newChanged, skipped)
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
