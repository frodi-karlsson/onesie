package mock

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/frodi-karlsson/onesie-go"
)

// NewTransport returns a Transport that encodes answers as provider's routes return them.
func NewTransport(provider onesie.Provider) Transport {
	return Transport{provider: provider}
}

// WithEntry returns a copy of ctx whose request Transport answers from entry, as model answered it.
func WithEntry(ctx context.Context, entry Entry, model string) context.Context {
	return context.WithValue(ctx, sentKey{}, sent{entry: entry, model: model})
}

// RoundTrip sends what the server the entry on the request's context describes would send, and
// refuses a request that carries none.
func (t Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		if err := req.Body.Close(); err != nil {
			return nil, err
		}
	}

	carried, found := req.Context().Value(sentKey{}).(sent)
	if !found {
		return nil, errors.New("the mock transport got a request with no mock entry, which is a bug in onesie")
	}

	entry := carried.entry

	switch {
	case entry.fault != nil:
		return nil, entry.fault
	case entry.auth:
		return response(req, entry.status, t.refusal()), nil
	case entry.status != 0:
		return response(req, entry.status, entry.body), nil
	}

	// A response entry holds no answers, which the client refuses as it refuses a live 200 without them.
	body, err := t.provider.ResponseBody(carried.model, &onesie.Result{Model: carried.model, Answers: entry.answers})
	if err != nil {
		return nil, err
	}

	return response(req, http.StatusOK, body), nil
}

func (t Transport) refusal() []byte {
	// Cloudflare names a refused token by its code, under a 400 too, where every other provider
	// refuses a key by its status alone.
	if t.provider.Name() == onesie.Cloudflare().Name() {
		return []byte(`{"success":false,"errors":[{"code":10000,"message":"Authentication error"}],` +
			`"messages":[],"result":null}`)
	}

	return nil
}

func response(req *http.Request, status int, body []byte) *http.Response {
	return &http.Response{
		Status:        strconv.Itoa(status) + " " + http.StatusText(status),
		StatusCode:    status,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        http.Header{"Content-Type": []string{"application/json"}},
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}
}

// Transport is an http.RoundTripper that answers from the mock entry on each request's context, so
// a real client handles every entry as it handles a live response.
type Transport struct {
	provider onesie.Provider
}

type sentKey struct{}

type sent struct {
	entry Entry
	model string
}
