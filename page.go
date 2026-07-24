package gossr

import (
	"context"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/daodao97/gossr/renderer"
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
type Redirect = renderer.Redirect

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

	if redirect.Location != strings.TrimSpace(redirect.Location) ||
		strings.ContainsAny(redirect.Location, "\r\n") ||
		!strings.HasPrefix(redirect.Location, "/") ||
		strings.HasPrefix(redirect.Location, "//") {
		return Redirect{}, fmt.Errorf("invalid redirect location %q", redirect.Location)
	}
	parsed, err := url.Parse(redirect.Location)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.User != nil || parsed.Opaque != "" {
		return Redirect{}, fmt.Errorf("invalid redirect location %q", redirect.Location)
	}
	return redirect, nil
}

func newDocumentPageRequest(req *http.Request) (PageRequest, error) {
	if req == nil || req.URL == nil {
		return PageRequest{}, fmt.Errorf("nil document request")
	}
	return PageRequest{
		Source: req,
		URL:    cloneTargetURL(req.URL),
		Kind:   PageRequestDocument,
	}, nil
}

func newNavigationPageRequest(req *http.Request, rawPath string) (PageRequest, error) {
	if req == nil || req.URL == nil {
		return PageRequest{}, fmt.Errorf("nil navigation request")
	}
	target := url.URL{
		Path:     ensureLeadingSlash(rawPath),
		RawQuery: req.URL.RawQuery,
	}
	return PageRequest{
		Source: req,
		URL:    target,
		Kind:   PageRequestNavigation,
	}, nil
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
