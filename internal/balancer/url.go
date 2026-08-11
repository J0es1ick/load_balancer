package balancer

import "net/url"

// joinURLPath mirrors net/http/httputil's URL joining semantics while
// preserving RawPath. Encoded separators must reach the upstream unchanged:
// decoding %2F before routing can make authorization and routing disagree.
func joinURLPath(baseURL, requestURL *url.URL) (path, rawPath string) {
	if baseURL.RawPath == "" && requestURL.RawPath == "" {
		return joinPath(baseURL.Path, requestURL.Path), ""
	}

	basePath := baseURL.EscapedPath()
	requestPath := requestURL.EscapedPath()
	return joinPath(baseURL.Path, requestURL.Path), joinPath(basePath, requestPath)
}
