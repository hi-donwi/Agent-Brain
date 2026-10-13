package codeintel

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// ProjectCommands holds detected execution commands categorized by lifecycle phase.
type ProjectCommands struct {
	Build []string `json:"build,omitempty"`
	Test  []string `json:"test,omitempty"`
	Lint  []string `json:"lint,omitempty"`
	Run   []string `json:"run,omitempty"`
}

// InstructionFile describes an identified guidance document for coding agents.
type InstructionFile struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Content string `json:"content"`
}

// ProjectMeta aggregates detected project guidance and build/test commands.
type ProjectMeta struct {
	Instructions []InstructionFile `json:"instructions"`
	Commands     ProjectCommands   `json:"commands"`
}

// DetectProject inspects rootDir to detect project instruction files and lifecycle commands.
func DetectProject(rootDir string) (*ProjectMeta, error) {
	cleanRoot := filepath.Clean(rootDir)

	instructions := detectInstructions(cleanRoot)
	commands := detectCommands(cleanRoot)

	return &ProjectMeta{
		Instructions: instructions,
		Commands:     commands,
	}, nil
}

func detectInstructions(rootDir string) []InstructionFile {
	candidateNames := []string{
		"AGENTS.md",
		"CLAUDE.md",
		"GEMINI.md",
		".cursorrules",
		".workspace-instructions.md",
	}

	var results []InstructionFile

	for _, name := range candidateNames {
		targetPath := filepath.Join(rootDir, name)
		if fi, err := os.Stat(targetPath); err == nil && !fi.IsDir() {
			content, err := os.ReadFile(targetPath)
			if err == nil {
				results = append(results, InstructionFile{
					Name:    name,
					Path:    targetPath,
					Content: string(content),
				})
			}
		}
	}

	return results
}

func detectCommands(rootDir string) ProjectCommands {
	var cmds ProjectCommands

	// 1. Check Makefile
	makefilePath := filepath.Join(rootDir, "Makefile")
	if data, err := os.ReadFile(makefilePath); err == nil {
		targets := parseMakefileTargets(data)
		if targets["build"] {
			cmds.Build = append(cmds.Build, "make build")
		}
		if targets["test"] {
			cmds.Test = append(cmds.Test, "make test")
		}
		if targets["race"] {
			cmds.Test = append(cmds.Test, "make race")
		}
		if targets["vet"] {
			cmds.Lint = append(cmds.Lint, "make vet")
		}
		if targets["lint"] {
			cmds.Lint = append(cmds.Lint, "make lint")
		}
		if targets["check"] {
			cmds.Lint = append(cmds.Lint, "make check")
		}
	}

	// 2. Check go.mod
	goModPath := filepath.Join(rootDir, "go.mod")
	if _, err := os.Stat(goModPath); err == nil {
		if len(cmds.Build) == 0 {
			cmds.Build = append(cmds.Build, "go build ./...")
		}
		if len(cmds.Test) == 0 {
			cmds.Test = append(cmds.Test, "go test ./...")
		}
		if len(cmds.Lint) == 0 {
			cmds.Lint = append(cmds.Lint, "go vet ./...")
		}
	}

	// 3. Check package.json
	packageJSONPath := filepath.Join(rootDir, "package.json")
	if data, err := os.ReadFile(packageJSONPath); err == nil {
		var pkg struct {
			Scripts map[string]string `json:"scripts"`
		}
		if err := json.Unmarshal(data, &pkg); err == nil {
			if _, ok := pkg.Scripts["build"]; ok {
				cmds.Build = append(cmds.Build, "npm run build")
			}
			if _, ok := pkg.Scripts["test"]; ok {
				cmds.Test = append(cmds.Test, "npm test")
			}
			if _, ok := pkg.Scripts["lint"]; ok {
				cmds.Lint = append(cmds.Lint, "npm run lint")
			}
			if _, ok := pkg.Scripts["start"]; ok {
				cmds.Run = append(cmds.Run, "npm start")
			}
		}
	}

	// 4. Check Cargo.toml
	cargoPath := filepath.Join(rootDir, "Cargo.toml")
	if _, err := os.Stat(cargoPath); err == nil {
		cmds.Build = append(cmds.Build, "cargo build")
		cmds.Test = append(cmds.Test, "cargo test")
		cmds.Lint = append(cmds.Lint, "cargo clippy")
	}

	// 5. Check pyproject.toml
	pyprojectPath := filepath.Join(rootDir, "pyproject.toml")
	if _, err := os.Stat(pyprojectPath); err == nil {
		cmds.Test = append(cmds.Test, "pytest")
		cmds.Lint = append(cmds.Lint, "ruff check")
	}

	return cmds
}

func parseMakefileTargets(data []byte) map[string]bool {
	targets := make(map[string]bool)
	scanner := bufio.NewScanner(bytes.NewReader(data))

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") || trimmed == "" {
			continue
		}

		// Check .PHONY: target1 target2
		if strings.HasPrefix(trimmed, ".PHONY:") {
			parts := strings.Fields(strings.TrimPrefix(trimmed, ".PHONY:"))
			for _, p := range parts {
				targets[p] = true
			}
			continue
		}

		// Check target: dependencies
		if idx := strings.Index(line, ":"); idx > 0 && !strings.HasPrefix(line, "\t") {
			targetName := strings.TrimSpace(line[:idx])
			if !strings.Contains(targetName, " ") && !strings.Contains(targetName, "%") && !strings.Contains(targetName, "$") {
				targets[targetName] = true
			}
		}
	}

	return targets
}
