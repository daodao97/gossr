package gossr

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"testing"
)

type navigationURLCorpus struct {
	Valid []struct {
		Name      string `json:"name"`
		Input     string `json:"input"`
		Canonical string `json:"canonical"`
	} `json:"valid"`
	Invalid []struct {
		Name  string `json:"name"`
		Input string `json:"input"`
	} `json:"invalid"`
}

func loadNavigationURLCorpus(t *testing.T) navigationURLCorpus {
	t.Helper()
	data, err := os.ReadFile("testdata/navigation_urls.json")
	if err != nil {
		t.Fatalf("read navigation URL corpus: %v", err)
	}
	var corpus navigationURLCorpus
	if err := json.Unmarshal(data, &corpus); err != nil {
		t.Fatalf("decode navigation URL corpus: %v", err)
	}
	return corpus
}

func TestSharedNavigationURLCorpus(t *testing.T) {
	corpus := loadNavigationURLCorpus(t)
	for _, testCase := range corpus.Valid {
		t.Run("valid/"+testCase.Name, func(t *testing.T) {
			target, err := ParsePageTarget(testCase.Input)
			if err != nil {
				t.Fatalf("ParsePageTarget(%q): %v", testCase.Input, err)
			}
			if got := target.RequestURI(); got != testCase.Canonical {
				t.Fatalf("canonical target=%q, want %q", got, testCase.Canonical)
			}
		})
	}
	for _, testCase := range corpus.Invalid {
		t.Run("invalid/"+testCase.Name, func(t *testing.T) {
			if _, err := ParsePageTarget(testCase.Input); err == nil {
				t.Fatalf("unsafe target %q succeeded", testCase.Input)
			}
		})
	}
}

func rawTargetRequest(raw string) *http.Request {
	return &http.Request{
		Method:     http.MethodGet,
		URL:        &url.URL{Path: "/placeholder"},
		RequestURI: raw,
		Header:     make(http.Header),
	}
}

func TestNavigationTargetPreservesEscapingAndForceQuery(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "encoded slash",
			raw:  DefaultSSRDataRoute + "/a%2Fb?tab=usage",
			want: "/a%2Fb?tab=usage",
		},
		{
			name: "force query",
			raw:  DefaultSSRDataRoute + "/foo?",
			want: "/foo?",
		},
		{
			name: "root exact route",
			raw:  DefaultSSRDataRoute,
			want: "/",
		},
		{
			name: "raw query order and encoding",
			raw:  DefaultSSRDataRoute + "/search?b=2&a=%2F&a=1",
			want: "/search?b=2&a=%2F&a=1",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			target, err := navigationTargetFromRequest(rawTargetRequest(testCase.raw))
			if err != nil {
				t.Fatalf("navigationTargetFromRequest failed: %v", err)
			}
			if got := target.RequestURI(); got != testCase.want {
				t.Fatalf("target=%q, want %q; value=%#v", got, testCase.want, target)
			}
		})
	}
}

func TestNavigationTargetRejectsUnsafeRequestTargets(t *testing.T) {
	tests := []string{
		"http://example.test" + DefaultSSRDataRoute + "/page",
		"//example.test" + DefaultSSRDataRoute + "/page",
		DefaultSSRDataRoute + "/%2e%2e/secret",
		DefaultSSRDataRoute + "/a/../secret",
		DefaultSSRDataRoute + "/a%5Cb",
		DefaultSSRDataRoute + "/a\\b",
		DefaultSSRDataRoute + "/a%00b",
		DefaultSSRDataRoute + "/a%1fb",
		DefaultSSRDataRoute + "/bad%",
		DefaultSSRDataRoute + "/page?raw#fragment",
		DefaultSSRDataRoute + "/page?next=%00",
		DefaultSSRDataRoute + "x/page",
	}

	for _, raw := range tests {
		t.Run(raw, func(t *testing.T) {
			if _, err := navigationTargetFromRequest(rawTargetRequest(raw)); err == nil {
				t.Fatalf("navigation target %q succeeded, want error", raw)
			}
		})
	}
}

func TestDocumentTargetUsesOriginalEscapedRequestURI(t *testing.T) {
	request := rawTargetRequest("/a%2Fb?next=%2Fdashboard")
	request.URL = &url.URL{
		Path:     "/a/b",
		RawPath:  "/a%2Fb",
		RawQuery: "next=%2Fdashboard",
	}

	pageRequest, err := newDocumentPageRequest(request)
	if err != nil {
		t.Fatalf("newDocumentPageRequest failed: %v", err)
	}
	if got := targetRequestURI(pageRequest); got != "/a%2Fb?next=%2Fdashboard" {
		t.Fatalf("document target=%q", got)
	}
}

func TestRedirectValidationUsesStrictRelativeURLRules(t *testing.T) {
	valid := []string{
		"/login",
		"/a%2Fb?next=%2Fdashboard",
		"/search?",
	}
	for _, location := range valid {
		if _, err := normalizeRedirect(Redirect{Location: location}); err != nil {
			t.Fatalf("valid redirect %q failed: %v", location, err)
		}
	}

	invalid := []string{
		"https://attacker.test/path",
		"//attacker.test/path",
		"/%2e%2e/secret",
		"/a/../secret",
		"/a%5Cb",
		"/a%00b",
		"/bad%",
		"/page#fragment",
		"/page?next=%0d%0aHeader:value",
	}
	for _, location := range invalid {
		if _, err := normalizeRedirect(Redirect{Location: location}); err == nil {
			t.Fatalf("unsafe redirect %q succeeded", location)
		}
	}
}
