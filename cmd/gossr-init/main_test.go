package main

import (
	"strings"
	"testing"

	"github.com/daodao97/gossr/cmd/gossr-init/internal/scaffold"
)

func TestPromptOptionsAcceptsFullstackDefaults(t *testing.T) {
	var options scaffold.Options
	var output strings.Builder
	proceed, err := promptOptions(&options, map[string]bool{}, promptInput("\n\n\n\n\n\n"), &output)
	if err != nil {
		t.Fatal(err)
	}
	if !proceed {
		t.Fatal("default confirmation should proceed")
	}
	resolved := scaffold.Defaults(options)
	if resolved.Template != "fullstack" || resolved.Dir != "gossr-app" || resolved.ProjectName != "gossr-app" {
		t.Fatalf("unexpected defaults: %+v", resolved)
	}
	if resolved.GoModule != "example.com/gossr-app" || resolved.GoPackage != "web" {
		t.Fatalf("unexpected Go defaults: %+v", resolved)
	}
	if !strings.Contains(output.String(), "Configuration") {
		t.Fatalf("summary was not printed: %s", output.String())
	}
}

func TestPromptOptionsFrontendValues(t *testing.T) {
	var options scaffold.Options
	var output strings.Builder
	input := promptInput("2\nfrontend\nsite-ui\nui\ny\n")
	proceed, err := promptOptions(&options, map[string]bool{}, input, &output)
	if err != nil {
		t.Fatal(err)
	}
	if !proceed {
		t.Fatal("explicit confirmation should proceed")
	}
	if options.Template != "minimal" || options.Dir != "frontend" || options.ProjectName != "site-ui" || options.GoPackage != "ui" {
		t.Fatalf("unexpected options: %+v", options)
	}
	if strings.Contains(output.String(), "Go module") {
		t.Fatalf("frontend-only wizard should not ask for a module: %s", output.String())
	}
}

func TestPromptOptionsRetriesTemplateAndCanCancel(t *testing.T) {
	var options scaffold.Options
	var output strings.Builder
	input := promptInput("9\n1\n\n\n\n\nno\n")
	proceed, err := promptOptions(&options, map[string]bool{}, input, &output)
	if err != nil {
		t.Fatal(err)
	}
	if proceed {
		t.Fatal("no confirmation should cancel")
	}
	if !strings.Contains(output.String(), "between 1 and 3") {
		t.Fatalf("validation message was not printed: %s", output.String())
	}
}

func TestPromptOptionsPreservesProvidedFlags(t *testing.T) {
	options := scaffold.Options{Template: "fullstack", Dir: "custom"}
	provided := map[string]bool{"template": true, "dir": true}
	var output strings.Builder
	proceed, err := promptOptions(&options, provided, promptInput("\n\n\n\n"), &output)
	if err != nil {
		t.Fatal(err)
	}
	if !proceed {
		t.Fatal("default confirmation should proceed")
	}
	if options.Template != "fullstack" || options.Dir != "custom" {
		t.Fatalf("provided flags changed: %+v", options)
	}
}

type byteReader struct {
	reader *strings.Reader
}

func promptInput(value string) *byteReader {
	return &byteReader{reader: strings.NewReader(value)}
}

func (reader *byteReader) Read(buffer []byte) (int, error) {
	if len(buffer) > 1 {
		buffer = buffer[:1]
	}
	return reader.reader.Read(buffer)
}
