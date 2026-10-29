package distribution

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/application"
)

const (
	// fetchTimeout bounds one download, set at the call that owns the budget.
	fetchTimeout = 60 * time.Second
	// maxArchiveBytes caps one downloaded archive: 64 MiB. One extra byte is read to detect an overflow.
	maxArchiveBytes = 64 << 20
	maxRedirects    = 3
	loopbackHost    = "127.0.0.1"
)

// refusedRedirect is returned by the redirect policy; it names the hop that broke the rule.
type refusedRedirect struct{ target string }

func (e refusedRedirect) Error() string {
	return "the source redirected to " + e.target + ", which is not the host the Lock names"
}

// CheckSource accepts https, or http on 127.0.0.1 only, with no credentials in the URL, and refuses anything
// else as ClassForbidden. The Lock schema applies the same rule; this is the second check at the point of use.
func CheckSource(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Host == "" || u.Fragment != "" {
		return nil, application.NewError(application.ClassForbidden, "distribution.source_refused",
			"the artifact source is not a plain https or loopback http URL")
	}
	switch {
	case u.Scheme == "https":
	case u.Scheme == "http" && u.Hostname() == loopbackHost:
	default:
		return nil, application.NewError(application.ClassForbidden, "distribution.source_refused",
			"the artifact source must be https, or http on "+loopbackHost+" only: "+u.Scheme+"://"+u.Host)
	}
	return u, nil
}

// sameOrigin reports whether b keeps a's scheme and host, which is the whole redirect rule.
func sameOrigin(a, b *url.URL) bool { return a.Scheme == b.Scheme && a.Host == b.Host }

// newClient returns a copy of base whose redirects must stay on the origin of the first request.
func newClient(base *http.Client) *http.Client {
	client := &http.Client{}
	if base != nil {
		*client = *base
	}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > maxRedirects {
			return refusedRedirect{target: "more than " + fmt.Sprint(maxRedirects) + " hops"}
		}
		if !sameOrigin(via[0].URL, req.URL) {
			return refusedRedirect{target: req.URL.Scheme + "://" + req.URL.Host}
		}
		return nil
	}
	return client
}

// fetch downloads source with a 60 s deadline and the size cap. A transport failure is ClassUnavailable and
// names the source; there is no retry in this milestone.
func fetch(ctx context.Context, client *http.Client, source string) ([]byte, error) {
	u, err := CheckSource(source)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), http.NoBody)
	if err != nil {
		return nil, application.Unexpected("distribution", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		if refused, ok := errors.AsType[refusedRedirect](err); ok {
			return nil, application.NewError(application.ClassForbidden, "distribution.redirect_refused", refused.Error()).WithCause(err)
		}
		return nil, application.NewError(application.ClassUnavailable, "distribution.source_unreachable",
			"the artifact source "+source+" could not be reached").WithCause(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, application.NewError(application.ClassUnavailable, "distribution.source_unreachable",
			fmt.Sprintf("the artifact source %s answered %d", source, resp.StatusCode))
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxArchiveBytes+1))
	if err != nil {
		return nil, application.NewError(application.ClassUnavailable, "distribution.source_unreachable",
			"reading the artifact from "+source+" failed").WithCause(err)
	}
	if len(data) > maxArchiveBytes {
		return nil, application.NewError(application.ClassCompatibility, "compatibility.artifact_too_large",
			fmt.Sprintf("the artifact at %s exceeds %d bytes", source, maxArchiveBytes))
	}
	return data, nil
}
