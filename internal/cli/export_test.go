package cli

import "net/http"

func Pooled(jobs int) http.RoundTripper {
	return pooled(jobs)
}
