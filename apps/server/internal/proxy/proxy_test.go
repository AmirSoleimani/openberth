package proxy

import (
	"testing"

	"github.com/AmirSoleimani/openberth/apps/server/internal/config"
)

// TestComposeFQDN_Nested locks in the default URL shape:
//   FlatURLs=false, Domain="acme.example.com", subdomain="blog"
//   → "blog.acme.example.com"
// This is what every existing self-hosted user relies on, with their
// `*.acme.example.com` wildcard cert covering every deploy.
func TestComposeFQDN_Nested(t *testing.T) {
	cases := []struct {
		domain, sub, want string
	}{
		{"acme.example.com", "blog", "blog.acme.example.com"},
		{"foo.bar.example.com", "api", "api.foo.bar.example.com"},
		{"localhost", "x", "x.localhost"},
		{"single", "y", "y.single"},
	}
	for _, tc := range cases {
		p := &ProxyManager{cfg: &config.Config{Domain: tc.domain, FlatURLs: false}}
		got := p.composeFQDN(tc.sub)
		if got != tc.want {
			t.Errorf("Domain=%q sub=%q: got %q, want %q", tc.domain, tc.sub, got, tc.want)
		}
	}
}

// TestComposeFQDN_Flat exercises the shared-apex shape:
//   FlatURLs=true, Domain="acme.example.com", subdomain="blog"
//   → "blog-acme.example.com"
// One cert at *.example.com covers every deploy in every workspace
// because every URL lands one label below the apex.
func TestComposeFQDN_Flat(t *testing.T) {
	cases := []struct {
		domain, sub, want string
	}{
		{"acme.example.com", "blog", "blog-acme.example.com"},
		{"acme.example.com", "api", "api-acme.example.com"},
		{"foo.bar.example.com", "blog", "blog-foo.bar.example.com"},   // workspace=foo, apex=bar.example.com
		{"a.b.c.d.example.com", "x", "x-a.b.c.d.example.com"},         // workspace=a, apex=b.c.d.example.com
	}
	for _, tc := range cases {
		p := &ProxyManager{cfg: &config.Config{Domain: tc.domain, FlatURLs: true}}
		got := p.composeFQDN(tc.sub)
		if got != tc.want {
			t.Errorf("Domain=%q sub=%q: got %q, want %q", tc.domain, tc.sub, got, tc.want)
		}
	}
}

// TestComposeFQDN_FlatFallsBackOnSingleLabelDomain — when Domain has
// no dot (single-label local-dev install like "localhost"), there's
// no apex to share, so flat mode degrades gracefully to nested.
// Single-label Domains are typically local-dev installs where flat
// mode wouldn't normally be enabled, so this is purely a robustness
// guard.
func TestComposeFQDN_FlatFallsBackOnSingleLabelDomain(t *testing.T) {
	p := &ProxyManager{cfg: &config.Config{Domain: "localhost", FlatURLs: true}}
	got := p.composeFQDN("blog")
	want := "blog.localhost"
	if got != want {
		t.Errorf("got %q, want %q (flat with no apex must fall back)", got, want)
	}
}

// TestDeployURL — the deploy API response includes a `url` field that
// service.go computes BEFORE the container starts (so the API can
// return immediately while the deploy is still "building"). That code
// path used to compose its URL inline and ignored FlatURLs entirely,
// so callers got the nested URL even with --flat-urls. DeployURL is
// the single place that knows how to compose a deploy URL — service
// delegates to it. These tests pin the contract.
func TestDeployURL(t *testing.T) {
	cases := []struct {
		name     string
		domain   string
		insecure bool
		flat     bool
		sub      string
		want     string
	}{
		{"https-nested", "openberth.example.com", false, false, "blog", "https://blog.openberth.example.com"},
		{"https-flat", "openberth.example.com", false, true, "blog", "https://blog-openberth.example.com"},
		{"http-nested", "openberth.example.com", true, false, "blog", "http://blog.openberth.example.com"},
		{"http-flat", "openberth.example.com", true, true, "blog", "http://blog-openberth.example.com"},
		// Single-label fallback still uses nested (the apex-share
		// trick has no apex to share).
		{"flat-localhost-fallback", "localhost", true, true, "x", "http://x.localhost"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &ProxyManager{cfg: &config.Config{
				Domain:   tc.domain,
				Insecure: tc.insecure,
				FlatURLs: tc.flat,
			}}
			got := p.DeployURL(tc.sub)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
