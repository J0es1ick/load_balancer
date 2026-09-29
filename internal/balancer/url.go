package balancer

import "net/url"

func joinURLPath(baseURL, requestURL *url.URL) (path, rawPath string) {
	if baseURL.RawPath == "" && requestURL.RawPath == "" {
		return joinPath(baseURL.Path, requestURL.Path), ""
	}

	basePath := baseURL.EscapedPath()
	requestPath := requestURL.EscapedPath()
	return joinPath(baseURL.Path, requestURL.Path), joinPath(basePath, requestPath)
}
