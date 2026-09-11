package catalog

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// UserAgent identifies this fetcher to the sites it reads. A publisher looking at their own
// logs should be able to see who took a copy and where to complain; an anonymous scraper is
// the kind nobody can hold to anything.
const UserAgent = "BosaGezmeBot/1.0 (+https://bosagezme.com/bot)"

// Fetcher reads a brand's own pages, obeying that site's robots.txt and asking no faster
// than once a second per host.
//
// The rule is applied as a rule, not case by case. English Home's file allows the list API
// this product reads and forbids the per-store pages beside it; discovering that by hand
// for twenty brands and remembering it in twenty adapters is how a policy becomes a
// promise nobody keeps. The file decides, every time, before the request is made.
type Fetcher struct {
	client *http.Client
	mu     sync.Mutex
	rules  map[string]*robots
	lastAt map[string]time.Time
	delay  time.Duration
}

func NewFetcher() *Fetcher {
	return &Fetcher{
		client: &http.Client{Timeout: 30 * time.Second},
		rules:  map[string]*robots{},
		lastAt: map[string]time.Time{},
		delay:  time.Second,
	}
}

// ErrDisallowed is returned instead of a body when robots.txt forbids the path. It is an
// error rather than an empty result so that a misconfigured adapter fails loudly during a
// dry run rather than reporting that a brand has no shops.
type ErrDisallowed struct{ URL string }

func (e ErrDisallowed) Error() string { return "robots.txt disallows " + e.URL }

// Get fetches a URL. Most locators are a GET and this is what they use.
func (f *Fetcher) Get(ctx context.Context, rawURL string) ([]byte, error) {
	return f.Send(ctx, http.MethodGet, rawURL, "")
}

// Send is the same request with a method and a form body, for the locators that answer only
// to a POST. WordPress puts every one of its endpoints behind a single address and tells
// them apart by a form field, so for those sites there is nothing to fetch without a body.
// robots.txt is consulted for the path either way: a rule about a path is a rule about a
// path, whichever verb reaches it.
func (f *Fetcher) Send(ctx context.Context, method, rawURL, body string) ([]byte, error) {
	if method == "" {
		method = http.MethodGet
	}
	method = strings.ToUpper(method)
	target, e := url.Parse(rawURL)
	if e != nil {
		return nil, e
	}
	if target.Scheme != "https" && target.Scheme != "http" {
		return nil, fmt.Errorf("refusing to fetch %q", rawURL)
	}
	allowed, e := f.allowed(ctx, target)
	if e != nil {
		return nil, e
	}
	if !allowed {
		return nil, ErrDisallowed{URL: rawURL}
	}
	// A host that says "too many" is telling us our pace is wrong, and the honest answer is
	// to slow down and ask again rather than to abandon the brand. One province of a store
	// list failing is one province missing from the catalogue, and it fails on a schedule
	// nobody watches, so a retry here is the difference between a complete import and a
	// quietly partial one.
	var last error
	for attempt := 0; attempt < backoffAttempts; attempt++ {
		answer, retryAfter, e := f.send(ctx, method, rawURL, body)
		if e == nil {
			return answer, nil
		}
		last = e
		if retryAfter <= 0 {
			return nil, e
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(retryAfter):
		}
	}
	return nil, last
}

// How many times a throttled request is retried before the brand is reported as failed.
// Five doublings from two seconds is about a minute of patience, which is more than any
// of these locators has needed and still finite.
const backoffAttempts = 5

// get performs one request. The second return value is how long to wait before trying
// again, and is zero for every failure that waiting would not fix.
func (f *Fetcher) send(ctx context.Context, method, rawURL, body string) ([]byte, time.Duration, error) {
	target, e := url.Parse(rawURL)
	if e != nil {
		return nil, 0, e
	}
	f.wait(target.Host)
	var payload io.Reader
	if body != "" {
		payload = strings.NewReader(body)
	}
	request, e := http.NewRequestWithContext(ctx, method, rawURL, payload)
	if e != nil {
		return nil, 0, e
	}
	request.Header.Set("User-Agent", UserAgent)
	request.Header.Set("Accept", "application/json, text/html;q=0.9, */*;q=0.5")
	if body != "" {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	response, e := f.client.Do(request)
	if e != nil {
		return nil, 0, e
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return nil, 0, fmt.Errorf("%s: %w", rawURL, errNotFound)
	}
	if response.StatusCode != http.StatusOK {
		return nil, retryDelay(response), fmt.Errorf("%s: %s", rawURL, response.Status)
	}
	// A store list is tens of kilobytes. A cap keeps a misconfigured URL from pulling a
	// whole site into memory.
	answer, e := io.ReadAll(io.LimitReader(response.Body, 16<<20))
	return answer, 0, e
}

// errNotFound lets a caller fanning out over many addresses tell "this one is not there"
// apart from "this locator is broken". They look identical in a status line and are not the
// same fact.
var errNotFound = errors.New("404 not found")

// retryDelay reads how long the server asked us to wait, and otherwise picks a delay for
// the statuses that are about pace or a momentary fault. A 403 or a 404 is an answer, not
// a wait; repeating it only wastes the host's time and ours.
func retryDelay(response *http.Response) time.Duration {
	switch response.StatusCode {
	case http.StatusTooManyRequests, http.StatusServiceUnavailable, http.StatusBadGateway, http.StatusGatewayTimeout:
	default:
		return 0
	}
	// The header is the host's own instruction and outranks our guess, in either direction.
	if header := strings.TrimSpace(response.Header.Get("Retry-After")); header != "" {
		if seconds, e := strconv.Atoi(header); e == nil && seconds >= 0 {
			if wait := time.Duration(seconds) * time.Second; wait <= maxRetryWait {
				return wait
			}
			return maxRetryWait
		}
	}
	return defaultRetryWait
}

const (
	defaultRetryWait = 4 * time.Second
	maxRetryWait     = 60 * time.Second
)

// wait spaces requests to one host. Politeness, and also self-preservation: a burst is what
// gets a fetcher blocked and turns a working adapter into a silent one.
func (f *Fetcher) wait(host string) {
	f.mu.Lock()
	last, seen := f.lastAt[host]
	now := time.Now()
	var sleep time.Duration
	if seen {
		if gap := now.Sub(last); gap < f.delay {
			sleep = f.delay - gap
		}
	}
	f.lastAt[host] = now.Add(sleep)
	f.mu.Unlock()
	if sleep > 0 {
		time.Sleep(sleep)
	}
}

func (f *Fetcher) allowed(ctx context.Context, target *url.URL) (bool, error) {
	f.mu.Lock()
	cached, ok := f.rules[target.Host]
	f.mu.Unlock()
	if !ok {
		fetched, e := f.loadRobots(ctx, target)
		if e != nil {
			return false, e
		}
		f.mu.Lock()
		f.rules[target.Host] = fetched
		f.mu.Unlock()
		cached = fetched
	}
	return cached.allows(target.EscapedPath()), nil
}

func (f *Fetcher) loadRobots(ctx context.Context, target *url.URL) (*robots, error) {
	request, e := http.NewRequestWithContext(ctx, http.MethodGet, target.Scheme+"://"+target.Host+"/robots.txt", nil)
	if e != nil {
		return nil, e
	}
	request.Header.Set("User-Agent", UserAgent)
	response, e := f.client.Do(request)
	if e != nil {
		return nil, e
	}
	defer response.Body.Close()
	// Which statuses mean what is settled by RFC 9309, and it is worth following rather
	// than inventing something stricter. A 4xx -- including the 403 several Turkish sites
	// answer with -- means the file is unavailable, which means no restrictions were
	// stated: the standard says a crawler may then access the site. Treating 403 as a
	// refusal, which this did, is a stricter rule than the publisher wrote, and it cost us
	// two chains that serve their pages to us perfectly happily.
	//
	// A 5xx is different and stays a refusal: the site is failing, and hammering a failing
	// site is exactly what robots.txt exists to prevent.
	if response.StatusCode >= 400 && response.StatusCode < 500 {
		return &robots{}, nil
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("robots.txt for %s: %s", target.Host, response.Status)
	}
	return parseRobots(io.LimitReader(response.Body, 1<<20)), nil
}

type robots struct{ disallow, allow []string }

// parseRobots reads the groups that apply to everyone. A brand that wants to name this
// fetcher specifically will use "*" until it has heard of us, and honouring the wildcard is
// the conservative reading either way.
func parseRobots(body io.Reader) *robots {
	out := &robots{}
	scanner := bufio.NewScanner(body)
	applies := false
	for scanner.Scan() {
		line := scanner.Text()
		if hash := strings.IndexByte(line, '#'); hash >= 0 {
			line = line[:hash]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		field, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		field = strings.ToLower(strings.TrimSpace(field))
		value = strings.TrimSpace(value)
		switch field {
		case "user-agent":
			applies = value == "*" || strings.EqualFold(value, "BosaGezmeBot")
		case "disallow":
			if applies && value != "" {
				out.disallow = append(out.disallow, value)
			}
		case "allow":
			if applies && value != "" {
				out.allow = append(out.allow, value)
			}
		}
	}
	return out
}

func (r *robots) allows(path string) bool {
	if path == "" {
		path = "/"
	}
	// An explicit Allow wins over a Disallow, which is how every major crawler reads these
	// files and how publishers expect them to be read.
	for _, pattern := range r.allow {
		if matchRobots(pattern, path) {
			return true
		}
	}
	for _, pattern := range r.disallow {
		if matchRobots(pattern, path) {
			return false
		}
	}
	return true
}

// matchRobots implements the two wildcards these files use: "*" for any run of characters
// and a trailing "$" for end-of-path. Everything else is a literal prefix.
func matchRobots(pattern, path string) bool {
	anchored := strings.HasSuffix(pattern, "$")
	pattern = strings.TrimSuffix(pattern, "$")
	parts := strings.Split(pattern, "*")
	position := 0
	for i, part := range parts {
		if part == "" {
			continue
		}
		index := strings.Index(path[position:], part)
		if index < 0 {
			return false
		}
		// The first segment is a prefix, not a search: "/list" must not match "/a/list".
		if i == 0 && index != 0 {
			return false
		}
		position += index + len(part)
	}
	if anchored {
		return position == len(path)
	}
	return true
}
