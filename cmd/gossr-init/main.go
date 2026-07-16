package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"charm.land/huh/v2"
	"github.com/daodao97/gossr/cmd/gossr-init/internal/scaffold"
)

func main() {
	var options scaffold.Options
	var yes bool
	flag.StringVar(&options.Dir, "dir", "", "directory to initialize (default: gossr-app, or web for frontend templates)")
	flag.StringVar(&options.Template, "template", "", "template: fullstack, minimal, or full (default: fullstack)")
	flag.StringVar(&options.ProjectName, "name", "", "npm package name (defaults to the directory name)")
	flag.StringVar(&options.GoPackage, "go-package", "", "Go package name for embed.go (defaults to web for fullstack)")
	flag.StringVar(&options.GoModule, "module", "", "Go module path for fullstack (defaults to example.com/<name>)")
	flag.BoolVar(&options.Force, "force", false, "overwrite conflicting template-owned files")
	flag.BoolVar(&options.DryRun, "dry-run", false, "show what would change without writing files")
	flag.BoolVar(&yes, "yes", false, "accept defaults and skip the interactive wizard")
	flag.Parse()

	provided := make(map[string]bool)
	flag.Visit(func(current *flag.Flag) {
		provided[current.Name] = true
	})
	if !yes && isTerminal(os.Stdin) {
		proceed, err := promptOptions(&options, provided, os.Stdin, os.Stdout)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gossr-init: %v\n", err)
			os.Exit(1)
		}
		if !proceed {
			fmt.Println("Cancelled.")
			return
		}
	}
	options = scaffold.Defaults(options)

	result, err := scaffold.Generate(options)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gossr-init: %v\n", err)
		os.Exit(1)
	}

	dir, err := filepath.Abs(options.Dir)
	if err != nil {
		dir = options.Dir
	}
	if options.DryRun {
		fmt.Printf("Would initialize %s (%d create, %d replace, %d unchanged)\n", dir, result.Created, result.Replaced, result.Unchanged)
		return
	}

	fmt.Printf("Initialized %s with the %s template (%d create, %d replace, %d unchanged).\n", dir, options.Template, result.Created, result.Replaced, result.Unchanged)
	if options.Template == "fullstack" {
		fmt.Printf("Next:\n  cd %s/web\n  npm install\n  npm run build\n  cd ..\n  go mod tidy\n  go run .\n", options.Dir)
		return
	}
	fmt.Printf("Next:\n  cd %s\n  npm install\n  npm run build\n", options.Dir)
}

func isTerminal(file *os.File) bool {
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func promptOptions(options *scaffold.Options, provided map[string]bool, input io.Reader, output io.Writer) (bool, error) {
	accessible := !usesStandardTerminal(input, output)
	run := func(form *huh.Form) error {
		return form.
			WithInput(input).
			WithOutput(output).
			WithAccessible(accessible).
			Run()
	}

	if provided["template"] {
		if err := scaffold.ValidateTemplate(options.Template); err != nil {
			return false, err
		}
	} else {
		options.Template = scaffold.Defaults(*options).Template
		form := huh.NewForm(huh.NewGroup(
			huh.NewSelect[string]().
				Title("Choose a project template").
				Description("Fullstack creates Go + Vue; minimal and full add only the Vue frontend.").
				Options(
					huh.NewOption("Full stack (recommended)", "fullstack"),
					huh.NewOption("Minimal Vue frontend", "minimal"),
					huh.NewOption("Full Vue frontend", "full"),
				).
				Value(&options.Template),
		))
		if err := run(form); err != nil {
			if errors.Is(err, huh.ErrUserAborted) {
				return false, nil
			}
			return false, err
		}
	}

	resolvedDefaults := scaffold.Defaults(*options)
	if !provided["dir"] && options.Dir == "" {
		options.Dir = resolvedDefaults.Dir
	}
	if !provided["name"] && options.ProjectName == "" {
		options.ProjectName = resolvedDefaults.ProjectName
	}
	if !provided["module"] && options.GoModule == "" {
		options.GoModule = resolvedDefaults.GoModule
	}
	if !provided["go-package"] && options.GoPackage == "" {
		options.GoPackage = resolvedDefaults.GoPackage
	}

	fields := make([]huh.Field, 0, 4)
	if !provided["dir"] {
		fields = append(fields, inputField("Output directory", &options.Dir, func(string) error { return nil }))
	}
	if !provided["name"] {
		fields = append(fields, inputField("Project/package name", &options.ProjectName, scaffold.ValidateProjectName))
	}
	if options.Template == "fullstack" && !provided["module"] {
		fields = append(fields, inputField("Go module", &options.GoModule, scaffold.ValidateGoModule))
	}
	if !provided["go-package"] {
		fields = append(fields, inputField("Frontend Go package", &options.GoPackage, scaffold.ValidateGoPackage))
	}
	if len(fields) > 0 {
		if err := run(huh.NewForm(huh.NewGroup(fields...))); err != nil {
			if errors.Is(err, huh.ErrUserAborted) {
				return false, nil
			}
			return false, err
		}
	}

	confirmed := true
	confirmation := huh.NewForm(huh.NewGroup(
		huh.NewNote().
			Title("Configuration").
			Description(configurationSummary(scaffold.Defaults(*options))),
		huh.NewConfirm().
			Title("Create project?").
			Affirmative("Create").
			Negative("Cancel").
			Value(&confirmed),
	))
	if err := run(confirmation); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return false, nil
		}
		return false, err
	}
	return confirmed, nil
}

func inputField(title string, value *string, validate func(string) error) *huh.Input {
	return huh.NewInput().
		Title(title).
		Value(value).
		Validate(validate)
}

func configurationSummary(options scaffold.Options) string {
	lines := []string{
		fmt.Sprintf("Template: %s", options.Template),
		fmt.Sprintf("Directory: %s", options.Dir),
		fmt.Sprintf("Project name: %s", options.ProjectName),
	}
	if options.Template == "fullstack" {
		lines = append(lines, fmt.Sprintf("Go module: %s", options.GoModule))
	}
	lines = append(lines, fmt.Sprintf("Go package: %s", options.GoPackage))
	return strings.Join(lines, "\n")
}

func usesStandardTerminal(input io.Reader, output io.Writer) bool {
	in, inputOK := input.(*os.File)
	out, outputOK := output.(*os.File)
	return inputOK && outputOK && in == os.Stdin && out == os.Stdout
}
