package scaffold

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

//go:embed all:templates
var templateFiles embed.FS

type Options struct {
	Dir         string
	Template    string
	ProjectName string
	GoPackage   string
	GoModule    string
	Force       bool
	DryRun      bool
}

type Result struct {
	Created   int
	Replaced  int
	Unchanged int
}

type outputFile struct {
	path      string
	content   []byte
	replace   bool
	unchanged bool
}

var npmNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
var goPackagePattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
var goModulePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._~/-]*$`)

func Generate(options Options) (Result, error) {
	options = Defaults(options)
	options.Dir = strings.TrimSpace(options.Dir)
	if options.Dir == "" {
		return Result{}, errors.New("--dir must not be empty")
	}
	if err := ValidateTemplate(options.Template); err != nil {
		return Result{}, err
	}

	baseName := filepath.Base(filepath.Clean(options.Dir))
	if options.ProjectName == "" {
		options.ProjectName = normalizeNPMName(baseName)
	}
	if err := ValidateProjectName(options.ProjectName); err != nil {
		return Result{}, err
	}
	if options.GoPackage == "" {
		if options.Template == "fullstack" {
			options.GoPackage = "web"
		} else {
			options.GoPackage = normalizeGoPackage(baseName)
		}
	}
	if err := ValidateGoPackage(options.GoPackage); err != nil {
		return Result{}, err
	}
	if options.GoModule == "" {
		options.GoModule = "example.com/" + options.ProjectName
	}
	if options.Template == "fullstack" {
		if err := ValidateGoModule(options.GoModule); err != nil {
			return Result{}, err
		}
	}

	files, err := loadTemplate(options.Template, options)
	if err != nil {
		return Result{}, err
	}

	outputs := make([]outputFile, 0, len(files))
	result := Result{}
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	for _, relativePath := range paths {
		destination := filepath.Join(options.Dir, filepath.FromSlash(relativePath))
		existing, readErr := os.ReadFile(destination)
		switch {
		case readErr == nil && string(existing) == string(files[relativePath]):
			result.Unchanged++
			outputs = append(outputs, outputFile{path: destination, content: files[relativePath], unchanged: true})
		case readErr == nil && !options.Force:
			return Result{}, fmt.Errorf("refusing to overwrite %s; rerun with --force to replace template-owned files", destination)
		case readErr == nil:
			result.Replaced++
			outputs = append(outputs, outputFile{path: destination, content: files[relativePath], replace: true})
		case errors.Is(readErr, os.ErrNotExist):
			result.Created++
			outputs = append(outputs, outputFile{path: destination, content: files[relativePath]})
		default:
			return Result{}, fmt.Errorf("inspect %s: %w", destination, readErr)
		}
	}

	if options.DryRun {
		return result, nil
	}
	for _, output := range outputs {
		if output.unchanged {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(output.path), 0o755); err != nil {
			return Result{}, fmt.Errorf("create directory for %s: %w", output.path, err)
		}
		var err error
		if output.replace {
			err = os.WriteFile(output.path, output.content, 0o644)
		} else {
			err = writeNewFile(output.path, output.content)
		}
		if err != nil {
			return Result{}, fmt.Errorf("write %s: %w", output.path, err)
		}
	}

	return result, nil
}

func ValidateTemplate(value string) error {
	if value != "minimal" && value != "full" && value != "fullstack" {
		return fmt.Errorf("unknown template %q (want fullstack, minimal, or full)", value)
	}
	return nil
}

func ValidateProjectName(value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	if !npmNamePattern.MatchString(value) {
		return fmt.Errorf("invalid npm package name %q", value)
	}
	return nil
}

func ValidateGoPackage(value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	if !goPackagePattern.MatchString(value) || goKeywords[value] {
		return fmt.Errorf("invalid Go package name %q", value)
	}
	return nil
}

func ValidateGoModule(value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	if !validGoModule(value) {
		return fmt.Errorf("invalid Go module path %q", value)
	}
	return nil
}

// Defaults resolves the dynamic defaults used by both the CLI wizard and the
// generator. Explicit values are preserved.
func Defaults(options Options) Options {
	if strings.TrimSpace(options.Template) == "" {
		options.Template = "fullstack"
	}
	if strings.TrimSpace(options.Dir) == "" {
		if options.Template == "fullstack" {
			options.Dir = "gossr-app"
		} else {
			options.Dir = "web"
		}
	}

	baseName := filepath.Base(filepath.Clean(options.Dir))
	if strings.TrimSpace(options.ProjectName) == "" {
		options.ProjectName = normalizeNPMName(baseName)
	}
	if strings.TrimSpace(options.GoPackage) == "" {
		if options.Template == "fullstack" {
			options.GoPackage = "web"
		} else {
			options.GoPackage = normalizeGoPackage(baseName)
		}
	}
	if strings.TrimSpace(options.GoModule) == "" {
		options.GoModule = "example.com/" + options.ProjectName
	}
	return options
}

func loadTemplate(name string, options Options) (map[string][]byte, error) {
	files := make(map[string][]byte)
	type layer struct {
		root   string
		prefix string
	}
	layers := []layer{{root: "templates/common"}, {root: "templates/" + name}}
	if name == "fullstack" {
		layers = []layer{
			{root: "templates/common", prefix: "web"},
			{root: "templates/minimal", prefix: "web"},
			{root: "templates/fullstack"},
		}
	}
	for _, currentLayer := range layers {
		err := fs.WalkDir(templateFiles, currentLayer.root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			content, err := templateFiles.ReadFile(path)
			if err != nil {
				return err
			}
			relativePath := strings.TrimPrefix(path, currentLayer.root+"/")
			relativePath = strings.TrimSuffix(relativePath, ".tmpl")
			if currentLayer.prefix != "" {
				relativePath = currentLayer.prefix + "/" + relativePath
			}
			text := strings.NewReplacer(
				"__GOSSR_PROJECT_NAME__", options.ProjectName,
				"__GOSSR_GO_PACKAGE__", options.GoPackage,
				"__GOSSR_GO_MODULE__", options.GoModule,
				"__GOSSR_TEMPLATE__", options.Template,
			).Replace(string(content))
			files[relativePath] = []byte(text)
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("load %s template: %w", name, err)
		}
	}
	return files, nil
}

func writeNewFile(path string, content []byte) (err error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			_ = os.Remove(path)
		}
	}()
	_, err = file.Write(content)
	return err
}

func normalizeNPMName(name string) string {
	name = strings.ToLower(name)
	name = regexp.MustCompile(`[^a-z0-9._-]+`).ReplaceAllString(name, "-")
	name = strings.Trim(name, "-._")
	if name == "" {
		return "gossr-web"
	}
	return name
}

func normalizeGoPackage(name string) string {
	name = strings.ToLower(name)
	name = regexp.MustCompile(`[^a-z0-9_]+`).ReplaceAllString(name, "_")
	name = strings.Trim(name, "_")
	if name == "" {
		name = "web"
	}
	if name[0] >= '0' && name[0] <= '9' {
		name = "web_" + name
	}
	if goKeywords[name] {
		name += "web"
	}
	return name
}

func validGoModule(module string) bool {
	return goModulePattern.MatchString(module) &&
		!strings.Contains(module, "//") &&
		!strings.HasSuffix(module, "/")
}

var goKeywords = map[string]bool{
	"break": true, "default": true, "func": true, "interface": true, "select": true,
	"case": true, "defer": true, "go": true, "map": true, "struct": true,
	"chan": true, "else": true, "goto": true, "package": true, "switch": true,
	"const": true, "fallthrough": true, "if": true, "range": true, "type": true,
	"continue": true, "for": true, "import": true, "return": true, "var": true,
}
