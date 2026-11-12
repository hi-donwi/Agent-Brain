package gateway

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/hi-donwi/Agent-Brain/internal/forge"
	"github.com/hi-donwi/Agent-Brain/internal/home"
	localmcp "github.com/hi-donwi/Agent-Brain/internal/mcp"
	"github.com/hi-donwi/Agent-Brain/internal/skills"
)

type managementSkill struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Source      string   `json:"source"`
	Pack        string   `json:"pack,omitempty"`
	Keywords    []string `json:"keywords,omitempty"`
	Valid       bool     `json:"valid"`
}

func (s *Server) userSkillsManager() (*skills.Manager, error) {
	layout, err := home.Default()
	if err != nil {
		return nil, err
	}
	skillsRoot, err := canonicalBrainHomeChild(layout.Root, layout.Skills)
	if err != nil {
		return nil, err
	}
	return skills.NewManager("", skillsRoot, nil), nil
}

func canonicalBrainHomeChild(homeRoot, childPath string) (string, error) {
	homeAbs, err := filepath.Abs(homeRoot)
	if err != nil {
		return "", err
	}
	homeReal, err := filepath.EvalSymlinks(homeAbs)
	if err != nil {
		homeReal = filepath.Clean(homeAbs)
	}
	childAbs, err := filepath.Abs(childPath)
	if err != nil {
		return "", skills.ErrInvalidPath
	}
	if childReal, err := filepath.EvalSymlinks(childAbs); err == nil {
		childReal = filepath.Clean(childReal)
		if !knowledgePathWithin(filepath.Clean(homeReal), childReal) || childReal == filepath.Clean(homeReal) {
			return "", skills.ErrInvalidPath
		}
		return childReal, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	rel, err := filepath.Rel(filepath.Clean(homeAbs), filepath.Clean(childAbs))
	if err != nil || filepath.IsAbs(rel) || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", skills.ErrInvalidPath
	}
	child := filepath.Clean(filepath.Join(homeReal, rel))
	resolved, err := filepath.EvalSymlinks(child)
	if err == nil {
		resolved = filepath.Clean(resolved)
		if !knowledgePathWithin(filepath.Clean(homeReal), resolved) || resolved == filepath.Clean(homeReal) {
			return "", skills.ErrInvalidPath
		}
		return resolved, nil
	}
	if os.IsNotExist(err) {
		return child, nil
	}
	return "", err
}

func validManagementSkillID(name string) bool {
	if len(name) == 0 || len(name) > 64 || strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-") || strings.Contains(name, "--") {
		return false
	}
	for _, char := range name {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-' {
			continue
		}
		return false
	}
	return true
}

func (s *Server) handleManagementSkills(w http.ResponseWriter, _ *http.Request) {
	manager, err := s.userSkillsManager()
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "Brain user skills are unavailable", "api_error", "skills_unavailable")
		return
	}
	list, err := manager.List()
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "Brain user skills are unavailable", "api_error", "skills_unavailable")
		return
	}
	items := make([]managementSkill, 0, len(list))
	for _, skill := range list {
		if skill.Source != skills.SourceUser {
			continue
		}
		id := filepath.Base(filepath.Clean(skill.Path))
		if !validManagementSkillID(id) {
			continue
		}
		validation := skills.ValidateDir(skill.Path)
		items = append(items, managementSkill{
			ID: id, Name: skill.Name, Description: skill.Description, Source: string(skill.Source),
			Pack: skill.Pack, Keywords: skill.Keywords, Valid: validation.Valid,
		})
	}
	writeJSON(w, map[string]any{"skills": items, "scope": "brain-user"})
}

func (s *Server) handleManagementSkill(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validManagementSkillID(name) {
		writeAPIError(w, http.StatusBadRequest, "invalid skill ID", "invalid_request_error", "bad_request")
		return
	}
	manager, err := s.userSkillsManager()
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "Brain user skills are unavailable", "api_error", "skills_unavailable")
		return
	}
	skill, err := manager.GetSkill(name)
	if err != nil || skill.Summary.Source != skills.SourceUser || filepath.Base(filepath.Clean(skill.Summary.Path)) != name {
		writeAPIError(w, http.StatusNotFound, "Brain user skill not found", "invalid_request_error", "skill_not_found")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, map[string]any{
		"id": name, "name": skill.Summary.Name, "description": skill.Summary.Description,
		"source": string(skill.Summary.Source), "pack": skill.Summary.Pack,
		"keywords": skill.Summary.Keywords, "license": skill.Frontmatter.License,
		"compatibility": skill.Frontmatter.Compatibility, "body": skill.Body,
		"references": skill.References,
	})
}

func (s *Server) handleManagementSkillValidation(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validManagementSkillID(name) {
		writeAPIError(w, http.StatusBadRequest, "invalid skill ID", "invalid_request_error", "bad_request")
		return
	}
	manager, err := s.userSkillsManager()
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "Brain user skills are unavailable", "api_error", "skills_unavailable")
		return
	}
	skill, err := manager.GetSkill(name)
	if err != nil || skill.Summary.Source != skills.SourceUser || filepath.Base(filepath.Clean(skill.Summary.Path)) != name {
		writeAPIError(w, http.StatusNotFound, "Brain user skill not found", "invalid_request_error", "skill_not_found")
		return
	}
	result := skills.ValidateDir(skill.Summary.Path)
	fields := make([]string, 0, len(result.Errors))
	for _, validationError := range result.Errors {
		fields = append(fields, validationError.Field)
	}
	writeJSON(w, map[string]any{"id": name, "valid": result.Valid, "failed_fields": fields})
}

type removeSkillRequest struct {
	Confirmation string `json:"confirmation"`
}

func (s *Server) handleManagementRemoveSkill(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validManagementSkillID(name) {
		writeAPIError(w, http.StatusBadRequest, "invalid skill ID", "invalid_request_error", "bad_request")
		return
	}
	var input removeSkillRequest
	if err := decodeManagementJSON(w, r, &input); err != nil || input.Confirmation != "remove "+name {
		writeAPIError(w, http.StatusBadRequest, "skill removal requires the exact skill ID confirmation", "invalid_request_error", "bad_request")
		return
	}
	manager, err := s.userSkillsManager()
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "Brain user skills are unavailable", "api_error", "skills_unavailable")
		return
	}
	if err := manager.RemoveUserSkill(name); err != nil {
		if errors.Is(err, skills.ErrSkillNotFound) {
			writeAPIError(w, http.StatusNotFound, "Brain user skill not found", "invalid_request_error", "skill_not_found")
		} else {
			writeAPIError(w, http.StatusConflict, "Brain user skill could not be removed safely", "invalid_request_error", "skill_remove_failed")
		}
		return
	}
	writeJSON(w, map[string]any{"id": name, "removed": true})
}

const maxSkillImportBytes = 64 << 10

type installSkillRequest struct {
	Name         string `json:"name"`
	SkillMD      string `json:"skill_md"`
	Confirmation string `json:"confirmation"`
}

func (s *Server) handleManagementInstallSkill(w http.ResponseWriter, r *http.Request) {
	var input installSkillRequest
	if err := decodeManagementJSONLimit(w, r, &input, 512<<10); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeAPIError(w, http.StatusRequestEntityTooLarge, "skill import request exceeds the size limit", "invalid_request_error", "payload_too_large")
		} else {
			writeAPIError(w, http.StatusBadRequest, "invalid skill import request", "invalid_request_error", "bad_request")
		}
		return
	}
	if len(input.SkillMD) > maxSkillImportBytes {
		writeAPIError(w, http.StatusRequestEntityTooLarge, "SKILL.md exceeds the 64 KiB import limit", "invalid_request_error", "payload_too_large")
		return
	}
	if !validManagementSkillID(input.Name) || input.Confirmation != "install "+input.Name || len(input.SkillMD) == 0 {
		writeAPIError(w, http.StatusBadRequest, "skill import requires a valid slug and exact confirmation", "invalid_request_error", "bad_request")
		return
	}
	frontmatter, body, err := skills.ParseSkillMD([]byte(input.SkillMD))
	if err != nil || frontmatter.Name != input.Name {
		writeAPIError(w, http.StatusBadRequest, "SKILL.md must be valid and its name must match the requested skill ID", "invalid_request_error", "invalid_skill")
		return
	}
	if violations := forge.ScanSafety([]byte(input.SkillMD)); len(violations) > 0 {
		writeAPIError(w, http.StatusBadRequest, "SKILL.md was rejected by Brain's secret/prompt-safety scan", "invalid_request_error", "skill_safety_rejected")
		return
	}
	validation := skills.Validate(&skills.Skill{Frontmatter: *frontmatter, Body: body}, input.Name)
	if !validation.Valid {
		writeAPIError(w, http.StatusBadRequest, "SKILL.md did not pass Brain's Agent Skills validator", "invalid_request_error", "invalid_skill")
		return
	}
	layout, err := home.Default()
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "Brain user-skill storage is unavailable", "api_error", "skills_unavailable")
		return
	}
	skillsRoot, err := canonicalBrainHomeChild(layout.Root, layout.Skills)
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "Brain user-skill storage is not a safe private-home directory", "api_error", "skills_unavailable")
		return
	}
	if err := os.MkdirAll(skillsRoot, 0o700); err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "Brain user-skill storage is unavailable", "api_error", "skills_unavailable")
		return
	}
	if err := os.Chmod(skillsRoot, 0o700); err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "Brain user-skill storage permissions could not be secured", "api_error", "skills_unavailable")
		return
	}
	skillsRoot, err = canonicalBrainHomeChild(layout.Root, skillsRoot)
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "Brain user-skill storage is not safe", "api_error", "skills_unavailable")
		return
	}
	destination := filepath.Join(skillsRoot, input.Name)
	if info, err := os.Lstat(destination); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			writeAPIError(w, http.StatusConflict, "Brain-user skill destination is not a safe directory", "invalid_request_error", "skill_destination_unsafe")
		} else {
			writeAPIError(w, http.StatusConflict, "Brain-user skill already exists; HTTP import never overwrites", "invalid_request_error", "skill_already_exists")
		}
		return
	} else if !os.IsNotExist(err) {
		writeAPIError(w, http.StatusInternalServerError, "Brain-user skill destination could not be checked", "api_error", "skills_unavailable")
		return
	}
	tempDir, err := os.MkdirTemp(skillsRoot, ".skill-import-")
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "Brain skill import could not be staged", "api_error", "skill_import_failed")
		return
	}
	defer os.RemoveAll(tempDir)
	if err := os.Chmod(tempDir, 0o700); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "Brain skill import could not be staged", "api_error", "skill_import_failed")
		return
	}
	tempFile := filepath.Join(tempDir, "SKILL.md")
	file, err := os.OpenFile(tempFile, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "Brain skill import could not be staged", "api_error", "skill_import_failed")
		return
	}
	if _, err := io.WriteString(file, input.SkillMD); err != nil {
		_ = file.Close()
		writeAPIError(w, http.StatusInternalServerError, "Brain skill import could not be staged", "api_error", "skill_import_failed")
		return
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		writeAPIError(w, http.StatusInternalServerError, "Brain skill import could not be staged", "api_error", "skill_import_failed")
		return
	}
	if err := file.Close(); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "Brain skill import could not be staged", "api_error", "skill_import_failed")
		return
	}
	if info, err := os.Lstat(destination); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			writeAPIError(w, http.StatusConflict, "Brain-user skill destination is not a safe directory", "invalid_request_error", "skill_destination_unsafe")
		} else {
			writeAPIError(w, http.StatusConflict, "Brain-user skill already exists; HTTP import never overwrites", "invalid_request_error", "skill_already_exists")
		}
		return
	} else if !os.IsNotExist(err) {
		writeAPIError(w, http.StatusInternalServerError, "Brain-user skill destination could not be checked", "api_error", "skills_unavailable")
		return
	}
	if err := os.Mkdir(destination, 0o700); err != nil {
		if os.IsExist(err) {
			writeAPIError(w, http.StatusConflict, "Brain-user skill already exists; HTTP import never overwrites", "invalid_request_error", "skill_already_exists")
		} else {
			writeAPIError(w, http.StatusInternalServerError, "Brain skill import could not be committed", "api_error", "skill_import_failed")
		}
		return
	}
	if err := os.Link(tempFile, filepath.Join(destination, "SKILL.md")); err != nil {
		_ = os.Remove(destination)
		writeAPIError(w, http.StatusInternalServerError, "Brain skill import could not be committed", "api_error", "skill_import_failed")
		return
	}
	writeJSONStatus(w, http.StatusCreated, map[string]any{
		"id": input.Name, "name": frontmatter.Name, "description": frontmatter.Description,
		"installed": true, "validation_passed": true,
	})
}

func (s *Server) handleManagementMCPInventory(w http.ResponseWriter, _ *http.Request) {
	layout, err := home.Default()
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "Brain MCP inventory is unavailable", "api_error", "mcp_unavailable")
		return
	}
	server := localmcp.NewServer(localmcp.Config{Name: "brain", RootDir: layout.Root, SkipResources: true})
	writeJSON(w, map[string]any{
		"transport": "stdio", "lifecycle": "brain mcp CLI", "started_by_console": false,
		"read_only": true, "tools": server.ToolCatalog(),
	})
}
