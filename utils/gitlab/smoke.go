package gitlab

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Smoke checks that every service runs and Puma answers. Health and readiness
// are asked on the host against its own nginx: an edge in front of the
// instance may hide /-/health, and /-/readiness is limited to the monitoring
// allowlist anyway. The sign-in page is fetched from the host through public
// DNS, not from the workstation, whose outbound HTTPS may be filtered; a
// failure there is a warning, because the upgrade itself already succeeded.
func Smoke(ctx context.Context, h Host, r *Reporter, attempts int, backoff time.Duration) error {
	status, err := h.Run(ctx, "gitlab-ctl status")
	if err != nil && status == "" {
		return abortAfter("gitlab-ctl status: %v", err)
	}
	var down []string
	for _, l := range strings.Split(strings.TrimSpace(status), "\n") {
		if l != "" && !strings.HasPrefix(l, "run:") {
			down = append(down, l)
		}
	}
	if len(down) > 0 {
		return abortAfter("%d services not running: %s", len(down), strings.Join(down, "; "))
	}
	r.Info("all %d services running", len(strings.Split(strings.TrimSpace(status), "\n")))

	f, err := ReadFacts(ctx, h, false)
	if err != nil {
		return abortAfter("%v", err)
	}
	fqdn := f.FQDN()
	if fqdn == "" {
		r.Warn("no external_url in gitlab.rb; HTTP checks skipped")
		return nil
	}

	// Puma answers 502 for a minute or two after a (re)start.
	code := ""
	for i := 1; i <= attempts; i++ {
		code = hostHTTP(ctx, h, fqdn, "https://127.0.0.1/-/health", true)
		if code == "200" {
			break
		}
		if i == attempts {
			return abortAfter("/-/health on the host not 200 after %d attempts (last %s)", attempts, code)
		}
		r.Info("/-/health on host -> HTTP %s, waiting", code)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
	}
	r.Info("/-/health on host -> HTTP 200")

	if code = hostHTTP(ctx, h, fqdn, "https://127.0.0.1/-/readiness?all=1", true); code == "200" {
		r.Info("/-/readiness on host -> HTTP 200")
	} else {
		r.Warn("/-/readiness on host -> HTTP %s; check the monitoring allowlist or run `gitlab-rake gitlab:check SANITIZE=true`", code)
	}

	signin := fmt.Sprintf("https://%s/users/sign_in", fqdn)
	if code = hostHTTP(ctx, h, "", signin, false); code == "200" {
		r.Info("%s from the host via public DNS -> HTTP 200", signin)
	} else {
		r.Warn("%s from the host via public DNS -> HTTP %s; check from a browser", signin, code)
	}
	return nil
}

func hostHTTP(ctx context.Context, h Host, hostHeader, url string, insecure bool) string {
	cmd := "curl -s -o /dev/null --max-time 15 -w '%{http_code}'"
	if insecure {
		cmd += " -k"
	}
	if hostHeader != "" {
		cmd += " -H " + shellQuote("Host: "+hostHeader)
	}
	out, _ := h.Run(ctx, cmd+" "+shellQuote(url))
	if out = strings.TrimSpace(out); out == "" {
		return "000"
	}
	return out
}
