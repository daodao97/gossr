package gossr

import (
	"context"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

// PageRequestKind identifies why a page is being resolved.
//
// Document requests produce HTML. Navigation requests produce the JSON
// snapshot consumed by the browser router. Both kinds are intentionally
// resolved by the same PageResolver.
type PageRequestKind uint8

const (
	PageRequestDocument PageRequestKind = iota + 1
	PageRequestNavigation
)

// PageRequest keeps the real transport request separate from the target page
// URL. They are identical for a document request. For a navigation request,
// Source is the real /_ssr/data request while URL is the page being prepared.
//
// Source must be treated as read-only. Authentication middleware can attach a
// verified principal to Source.Context before gossr invokes the resolver.
type PageRequest struct {
	Source     *http.Request
	URL        url.URL
	Kind       PageRequestKind
	SiteOrigin string
}

// PageResolver resolves the serializable snapshot and HTTP intent for one
// target URL. It is called directly; gossr never turns it into an internal HTTP
// request.
type PageResolver func(context.Context, PageRequest) (PageResult, error)

// Redirect is deliberately narrower than http.Header. The SSR orchestrator
// owns Content-Type, cache headers and all other protocol details.
type Redirect struct {
	Status   int    `json:"status"`
	Location string `json:"location"`
}

// CachePolicy is a small, safe set of document/navigation cache policies.
// The zero value is intentionally the safest behavior for personalized SSR.
type CachePolicy uint8

const (
	CacheNoStore CachePolicy = iota
	CachePrivateRevalidate
)

// PageResult is the resolver's complete page-level outcome.
//
// Status defaults to 200. Redirect skips rendering. Payload must be safe to
// serialize into both the document boot state and the navigation response.
// Cookies are the only supported response mutation and are validated before
// being written to the real document or navigation response.
type PageResult struct {
	Payload  SSRPayload
	Status   int
	Redirect *Redirect
	Cache    CachePolicy
	Cookies  []*http.Cookie
}

type navigationRenderOutcome struct {
	Kind     string         `json:"kind"`
	Status   int            `json:"status"`
	Snapshot map[string]any `json:"snapshot"`
}

type navigationRedirectOutcome struct {
	Kind     string `json:"kind"`
	Status   int    `json:"status"`
	Location string `json:"location"`
}

type navigationErrorOutcome struct {
	Kind    string `json:"kind"`
	Status  int    `json:"status"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ParsePageTarget parses and validates an origin-relative page target using
// the same strict rules applied to document requests, navigation requests, and
// redirects. The returned URL preserves the original escaped path, raw query,
// and trailing question mark.
func ParsePageTarget(raw string) (url.URL, error) {
	return parseRelativePageTarget(raw)
}

func normalizePageResult(result PageResult) (PageResult, error) {
	if result.Cache != CacheNoStore && result.Cache != CachePrivateRevalidate {
		return PageResult{}, fmt.Errorf("invalid cache policy %d", result.Cache)
	}
	for index, cookie := range result.Cookies {
		if cookie == nil {
			return PageResult{}, fmt.Errorf("invalid page cookie %d: nil cookie", index)
		}
		if err := cookie.Valid(); err != nil {
			return PageResult{}, fmt.Errorf("invalid page cookie %d: %w", index, err)
		}
	}

	if result.Redirect != nil {
		redirect, err := normalizeRedirect(*result.Redirect)
		if err != nil {
			return PageResult{}, err
		}
		result.Redirect = &redirect
		result.Status = redirect.Status
		return result, nil
	}

	if result.Status == 0 {
		result.Status = http.StatusOK
	}
	if result.Status < 200 || result.Status > 599 {
		return PageResult{}, fmt.Errorf("invalid page status %d", result.Status)
	}
	if result.Status >= 300 && result.Status < 400 {
		return PageResult{}, fmt.Errorf("page status %d requires a redirect", result.Status)
	}
	switch result.Status {
	case http.StatusNoContent, http.StatusResetContent:
		return PageResult{}, fmt.Errorf("page status %d cannot carry a document", result.Status)
	}
	return result, nil
}

func normalizeRedirect(redirect Redirect) (Redirect, error) {
	if redirect.Status == 0 {
		redirect.Status = http.StatusFound
	}
	switch redirect.Status {
	case http.StatusMovedPermanently,
		http.StatusFound,
		http.StatusSeeOther,
		http.StatusTemporaryRedirect,
		http.StatusPermanentRedirect:
	default:
		return Redirect{}, fmt.Errorf("invalid redirect status %d", redirect.Status)
	}

	if redirect.Location != strings.TrimSpace(redirect.Location) {
		return Redirect{}, fmt.Errorf("invalid redirect location %q", redirect.Location)
	}
	if _, err := parseRelativePageTarget(redirect.Location); err != nil {
		return Redirect{}, fmt.Errorf("invalid redirect location %q", redirect.Location)
	}
	return redirect, nil
}

func newDocumentPageRequest(req *http.Request) (PageRequest, error) {
	if req == nil || req.URL == nil {
		return PageRequest{}, fmt.Errorf("nil document request")
	}
	target, err := parseRelativePageTarget(requestTarget(req))
	if err != nil {
		return PageRequest{}, fmt.Errorf("invalid document target: %w", err)
	}
	return PageRequest{
		Source: req,
		URL:    target,
		Kind:   PageRequestDocument,
	}, nil
}

func newNavigationPageRequest(req *http.Request, _ string) (PageRequest, error) {
	if req == nil || req.URL == nil {
		return PageRequest{}, fmt.Errorf("nil navigation request")
	}
	target, err := navigationTargetFromRequest(req)
	if err != nil {
		return PageRequest{}, err
	}
	return PageRequest{
		Source: req,
		URL:    target,
		Kind:   PageRequestNavigation,
	}, nil
}

func requestTarget(req *http.Request) string {
	if req == nil {
		return ""
	}
	if req.RequestURI != "" {
		return req.RequestURI
	}
	if req.URL == nil {
		return ""
	}
	return req.URL.RequestURI()
}

func navigationTargetFromRequest(req *http.Request) (url.URL, error) {
	rawRequestTarget := requestTarget(req)
	if rawRequestTarget == "" {
		return url.URL{}, fmt.Errorf("empty navigation request target")
	}
	if strings.Contains(rawRequestTarget, "#") {
		return url.URL{}, fmt.Errorf("navigation request target contains a fragment")
	}

	rawPath := rawRequestTarget
	rawSuffix := ""
	if queryAt := strings.IndexByte(rawRequestTarget, '?'); queryAt >= 0 {
		rawPath = rawRequestTarget[:queryAt]
		rawSuffix = rawRequestTarget[queryAt:]
	}

	var targetPath string
	switch {
	case rawPath == DefaultSSRDataRoute:
		targetPath = "/"
	case strings.HasPrefix(rawPath, DefaultSSRDataRoute+"/"):
		targetPath = rawPath[len(DefaultSSRDataRoute):]
	default:
		return url.URL{}, fmt.Errorf("navigation target is outside %s", DefaultSSRDataRoute)
	}

	target, err := parseRelativePageTarget(targetPath + rawSuffix)
	if err != nil {
		return url.URL{}, fmt.Errorf("invalid navigation target: %w", err)
	}
	return target, nil
}

func parseRelativePageTarget(raw string) (url.URL, error) {
	if raw == "" {
		return url.URL{}, fmt.Errorf("empty relative URL")
	}
	if !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return url.URL{}, fmt.Errorf("relative URL must start with one slash")
	}
	if strings.Contains(raw, "#") {
		return url.URL{}, fmt.Errorf("relative URL must not contain a fragment")
	}
	if err := validateURLText(raw); err != nil {
		return url.URL{}, err
	}

	parsed, err := url.ParseRequestURI(raw)
	if err != nil {
		return url.URL{}, fmt.Errorf("parse relative URL: %w", err)
	}
	if parsed.IsAbs() || parsed.Host != "" || parsed.User != nil || parsed.Opaque != "" ||
		parsed.Fragment != "" || parsed.RawFragment != "" {
		return url.URL{}, fmt.Errorf("URL must be origin-relative")
	}
	if parsed.Path == "" || !strings.HasPrefix(parsed.Path, "/") || strings.HasPrefix(parsed.Path, "//") {
		return url.URL{}, fmt.Errorf("URL path must start with one slash")
	}
	if err := validateDecodedURLText(parsed.Path); err != nil {
		return url.URL{}, fmt.Errorf("invalid URL path: %w", err)
	}
	for _, segment := range strings.Split(parsed.Path, "/") {
		if segment == "." || segment == ".." {
			return url.URL{}, fmt.Errorf("URL path contains a dot segment")
		}
	}
	decodedQuery, err := url.QueryUnescape(parsed.RawQuery)
	if err != nil {
		return url.URL{}, fmt.Errorf("invalid URL query: %w", err)
	}
	if err := validateDecodedURLText(decodedQuery); err != nil {
		return url.URL{}, fmt.Errorf("invalid URL query: %w", err)
	}
	return *parsed, nil
}

func validateURLText(raw string) error {
	if !utf8.ValidString(raw) {
		return fmt.Errorf("URL is not valid UTF-8")
	}
	for _, value := range []byte(raw) {
		if value <= 0x20 || value == 0x7f {
			return fmt.Errorf("URL contains raw ASCII whitespace or a control character")
		}
		if value == '\\' {
			return fmt.Errorf("URL contains a backslash")
		}
	}
	return nil
}

func validateDecodedURLText(raw string) error {
	if !utf8.ValidString(raw) {
		return fmt.Errorf("decoded URL is not valid UTF-8")
	}
	for _, value := range []byte(raw) {
		if value < 0x20 || value == 0x7f {
			return fmt.Errorf("decoded URL contains an ASCII control character")
		}
		if value == '\\' {
			return fmt.Errorf("decoded URL contains a backslash")
		}
	}
	return nil
}

func cloneTargetURL(source *url.URL) url.URL {
	if source == nil {
		return url.URL{Path: "/"}
	}
	target := *source
	target.Scheme = ""
	target.Host = ""
	target.User = nil
	target.Opaque = ""
	target.ForceQuery = source.ForceQuery
	target.Fragment = ""
	target.RawFragment = ""
	if target.Path == "" {
		target.Path = "/"
	}
	return target
}

func targetRequestURI(request PageRequest) string {
	uri := request.URL.RequestURI()
	if uri == "" {
		return "/"
	}
	return uri
}

func shouldHandleDocument(req *http.Request, excludedPrefixes []string) bool {
	if req == nil || req.URL == nil {
		return false
	}
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		return false
	}
	if !acceptsHTML(req.Header.Values("Accept")) {
		return false
	}
	if isSSRDataPath(req.URL.Path) ||
		pathHasPrefix(req.URL.Path, "/assets") ||
		pathHasPrefix(req.URL.Path, "/debug/pprof") ||
		isStaticAssetLikePath(req.URL.Path) {
		return false
	}
	for _, prefix := range excludedPrefixes {
		if pathHasPrefix(req.URL.Path, prefix) {
			return false
		}
	}
	return true
}

func acceptsHTML(values []string) bool {
	for _, value := range values {
		for _, candidate := range strings.Split(value, ",") {
			mediaType, params, err := mime.ParseMediaType(strings.TrimSpace(candidate))
			if err != nil {
				continue
			}
			quality := 1.0
			if rawQuality, ok := params["q"]; ok {
				parsed, err := strconv.ParseFloat(rawQuality, 64)
				if err != nil {
					continue
				}
				quality = parsed
			}
			if quality <= 0 {
				continue
			}
			switch strings.ToLower(mediaType) {
			case "text/html", "application/xhtml+xml":
				return true
			}
		}
	}
	return false
}

func pathHasPrefix(rawPath, rawPrefix string) bool {
	prefix := strings.TrimSpace(rawPrefix)
	if prefix == "" {
		return false
	}
	prefix = ensureLeadingSlash(prefix)
	if prefix != "/" {
		prefix = strings.TrimRight(prefix, "/")
	}
	return rawPath == prefix || prefix == "/" || strings.HasPrefix(rawPath, prefix+"/")
}
