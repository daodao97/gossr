package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateMinimalAndRepeat(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "my-web")
	result, err := Generate(Options{Dir: dir, Template: "minimal"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Created == 0 || result.Replaced != 0 || result.Unchanged != 0 {
		t.Fatalf("unexpected first result: %+v", result)
	}

	packageJSON, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(packageJSON), `"name": "my-web"`) {
		t.Fatalf("project name was not substituted: %s", packageJSON)
	}
	embedGo, err := os.ReadFile(filepath.Join(dir, "embed.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(embedGo), "package my_web") {
		t.Fatalf("Go package was not normalized: %s", embedGo)
	}

	repeated, err := Generate(Options{Dir: dir, Template: "minimal"})
	if err != nil {
		t.Fatal(err)
	}
	if repeated.Created != 0 || repeated.Replaced != 0 || repeated.Unchanged != result.Created {
		t.Fatalf("unexpected repeated result: %+v", repeated)
	}
}

func TestDefaults(t *testing.T) {
	fullstack := Defaults(Options{})
	if fullstack.Template != "fullstack" || fullstack.Dir != "gossr-app" || fullstack.GoModule != "example.com/gossr-app" || fullstack.GoPackage != "web" {
		t.Fatalf("unexpected fullstack defaults: %+v", fullstack)
	}
	frontend := Defaults(Options{Template: "minimal"})
	if frontend.Dir != "web" || frontend.ProjectName != "web" || frontend.GoPackage != "web" {
		t.Fatalf("unexpected frontend defaults: %+v", frontend)
	}
}

func TestGenerateRefusesConflictBeforeWriting(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte("owned by user\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Generate(Options{Dir: dir, Template: "minimal"})
	if err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("expected conflict, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "src", "main.ts")); !os.IsNotExist(statErr) {
		t.Fatalf("preflight should prevent partial output, stat error: %v", statErr)
	}
	content, readErr := os.ReadFile(filepath.Join(dir, "package.json"))
	if readErr != nil || string(content) != "owned by user\n" {
		t.Fatalf("conflicting file changed: %q, %v", content, readErr)
	}
}

func TestGenerateForceOnlyReplacesOwnedPaths(t *testing.T) {
	dir := t.TempDir()
	if _, err := Generate(Options{Dir: dir, Template: "minimal"}); err != nil {
		t.Fatal(err)
	}
	packagePath := filepath.Join(dir, "package.json")
	if err := os.WriteFile(packagePath, []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	customPath := filepath.Join(dir, "custom.txt")
	if err := os.WriteFile(customPath, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := Generate(Options{Dir: dir, Template: "minimal", Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Replaced != 1 {
		t.Fatalf("expected one replacement, got %+v", result)
	}
	custom, err := os.ReadFile(customPath)
	if err != nil || string(custom) != "keep\n" {
		t.Fatalf("custom file changed: %q, %v", custom, err)
	}
}

func TestGenerateFullAndDryRun(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "web")
	result, err := Generate(Options{Dir: dir, Template: "full", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Created == 0 {
		t.Fatal("dry run reported no files")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("dry run created destination: %v", err)
	}

	if _, err := Generate(Options{Dir: dir, Template: "full"}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"src/layouts/default.vue", "src/pages/about.vue", "src/modules/navigation.ts"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(path))); err != nil {
			t.Errorf("missing full template file %s: %v", path, err)
		}
	}
}

func TestGenerateFullstack(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "my-app")
	result, err := Generate(Options{Dir: dir, Template: "fullstack"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Created == 0 {
		t.Fatal("fullstack template reported no files")
	}

	checks := map[string]string{
		"go.mod":           "module example.com/my-app",
		"main.go":          `frontend "example.com/my-app/web"`,
		"web/embed.go":     "package web",
		"web/package.json": `"name": "my-app"`,
		"web/src/main.ts":  "createAppRouter",
	}
	for path, expected := range checks {
		content, readErr := os.ReadFile(filepath.Join(dir, filepath.FromSlash(path)))
		if readErr != nil {
			t.Errorf("read %s: %v", path, readErr)
			continue
		}
		if !strings.Contains(string(content), expected) {
			t.Errorf("%s does not contain %q: %s", path, expected, content)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "package.json")); !os.IsNotExist(err) {
		t.Errorf("frontend package.json should be nested under web: %v", err)
	}
}

func TestGenerateFullstackCustomModule(t *testing.T) {
	dir := t.TempDir()
	_, err := Generate(Options{
		Dir:       dir,
		Template:  "fullstack",
		GoModule:  "github.com/acme/site",
		GoPackage: "frontend",
	})
	if err != nil {
		t.Fatal(err)
	}
	mainGo, err := os.ReadFile(filepath.Join(dir, "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mainGo), `frontend "github.com/acme/site/web"`) {
		t.Fatalf("custom module was not applied: %s", mainGo)
	}
	embedGo, err := os.ReadFile(filepath.Join(dir, "web", "embed.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(embedGo), "package frontend") {
		t.Fatalf("custom package was not applied: %s", embedGo)
	}
}

func TestGenerateRejectsInvalidModule(t *testing.T) {
	_, err := Generate(Options{Dir: t.TempDir(), Template: "fullstack", GoModule: "bad module"})
	if err == nil || !strings.Contains(err.Error(), "invalid Go module") {
		t.Fatalf("expected module error, got %v", err)
	}
}

func TestGenerateRejectsUnknownTemplate(t *testing.T) {
	_, err := Generate(Options{Dir: t.TempDir(), Template: "everything"})
	if err == nil || !strings.Contains(err.Error(), "unknown template") {
		t.Fatalf("expected template error, got %v", err)
	}
}
