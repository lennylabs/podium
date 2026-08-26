package server

import "net/http"

// contentSecurityPolicy is the policy every response carries.
//
// The directives are stated for the §13.10 UI document, which is the only
// response a browser executes, and they cost a JSON response nothing. Each
// one closes a path that would otherwise stand open behind the client-side
// markdown sanitizer:
//
//   - default-src, script-src, and connect-src hold every fetch the document
//     makes to the registry's own origin, so markup that survived the
//     sanitizer still cannot reach a host the artifact's author picked.
//   - img-src states in the policy what the sanitizer already enforces on the
//     fetching attributes: a same-origin URL, or a data: URL the document
//     itself built. A rendered body therefore cannot turn a page view into a
//     request that hands the reader's address to a foreign host.
//   - style-src carries 'unsafe-inline' because the panel sets a custom
//     property through a style attribute, which a browser attributes to
//     style-src. It does not admit a stylesheet from another origin.
//   - object-src and base-uri close the plugin and the base-URL rewrite, and
//     frame-ancestors refuses framing outright, so the layer panel's
//     destructive controls cannot be driven from a page that overlays them.
//   - form-action keeps a submission on the registry, which is where the
//     browser session cookie is scoped.
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self'; " +
	"connect-src 'self'; " +
	"img-src 'self' data:; " +
	"style-src 'self' 'unsafe-inline'; " +
	"font-src 'self'; " +
	"object-src 'none'; " +
	"base-uri 'self'; " +
	"form-action 'self'; " +
	"frame-ancestors 'none'"

// SecurityHeaders sets the response headers that harden the origin the
// §13.10 web UI and the §6.3.4 browser session share.
//
// The headers go on every response rather than on the UI mount alone,
// because the session cookie is scoped to the origin and not to a path: a
// response from any route on it that a browser can be induced to treat as a
// document is a foothold on the same origin. Setting them here also keeps
// one policy for the process instead of a set that varies by route, which a
// caching proxy in front of the registry could otherwise serve under the
// wrong path.
//
// They are written before the wrapped handler runs, because a header set
// after the handler has called WriteHeader is dropped.
//
// X-Frame-Options duplicates the policy's frame-ancestors directive for a
// browser that honors the header and not the directive. Referrer-Policy is
// no-referrer, because no outbound request the UI makes needs the artifact
// path the reader is on, and an artifact identifier is catalogue content.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}
