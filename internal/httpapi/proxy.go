package httpapi

import (
	"net/http"
	"net/http/httputil"
	"net/url"
)

func newDevProxy(target *url.URL) http.Handler {
	return httputil.NewSingleHostReverseProxy(target)
}
