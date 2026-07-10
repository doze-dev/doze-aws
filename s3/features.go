package s3

// The functional bucket features: CORS preflight evaluation, bucket-website
// index/error serving, and lifecycle expiration.

import (
	"encoding/xml"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ---- CORS ----

type corsConfig struct {
	Rules []corsRule `xml:"CORSRule"`
}

type corsRule struct {
	AllowedOrigins []string `xml:"AllowedOrigin"`
	AllowedMethods []string `xml:"AllowedMethod"`
	AllowedHeaders []string `xml:"AllowedHeader"`
	ExposeHeaders  []string `xml:"ExposeHeader"`
	MaxAgeSeconds  int      `xml:"MaxAgeSeconds"`
}

// corsRules loads and parses a bucket's CORS config, or nil.
func (s *Server) corsRules(bucket string) []corsRule {
	bk, err := s.store.GetBucket(bucket)
	if err != nil || bk.CORS == "" {
		return nil
	}
	var cfg corsConfig
	if xml.Unmarshal([]byte(bk.CORS), &cfg) != nil {
		return nil
	}
	return cfg.Rules
}

// matchOrigin implements S3's origin matching (exact or single-* wildcard).
func matchOrigin(pattern, origin string) bool {
	if pattern == "*" {
		return true
	}
	if i := strings.Index(pattern, "*"); i >= 0 {
		return strings.HasPrefix(origin, pattern[:i]) && strings.HasSuffix(origin, pattern[i+1:])
	}
	return pattern == origin
}

// handlePreflight answers OPTIONS requests from the bucket's CORS rules.
func (s *Server) handlePreflight(w http.ResponseWriter, r *http.Request, bucket string) {
	origin := r.Header.Get("Origin")
	method := r.Header.Get("Access-Control-Request-Method")
	if origin == "" || method == "" || bucket == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	reqHeaders := splitCSV(r.Header.Get("Access-Control-Request-Headers"))

	for _, rule := range s.corsRules(bucket) {
		if !originAllowed(rule, origin) || !contains(rule.AllowedMethods, method) {
			continue
		}
		if !headersAllowed(rule, reqHeaders) {
			continue
		}
		allowOrigin := origin
		if len(rule.AllowedOrigins) == 1 && rule.AllowedOrigins[0] == "*" {
			allowOrigin = "*"
		}
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", allowOrigin)
		h.Set("Access-Control-Allow-Methods", strings.Join(rule.AllowedMethods, ", "))
		if len(reqHeaders) > 0 {
			h.Set("Access-Control-Allow-Headers", strings.Join(reqHeaders, ", "))
		}
		if len(rule.ExposeHeaders) > 0 {
			h.Set("Access-Control-Expose-Headers", strings.Join(rule.ExposeHeaders, ", "))
		}
		if rule.MaxAgeSeconds > 0 {
			h.Set("Access-Control-Max-Age", strconv.Itoa(rule.MaxAgeSeconds))
		}
		h.Set("Vary", "Origin, Access-Control-Request-Headers, Access-Control-Request-Method")
		w.WriteHeader(200)
		return
	}
	w.WriteHeader(http.StatusForbidden)
}

// applyCORS decorates a non-preflight response when a rule matches.
func (s *Server) applyCORS(w http.ResponseWriter, r *http.Request, bucket, origin string) {
	for _, rule := range s.corsRules(bucket) {
		if !originAllowed(rule, origin) || !contains(rule.AllowedMethods, r.Method) {
			continue
		}
		allowOrigin := origin
		if len(rule.AllowedOrigins) == 1 && rule.AllowedOrigins[0] == "*" {
			allowOrigin = "*"
		}
		w.Header().Set("Access-Control-Allow-Origin", allowOrigin)
		if len(rule.ExposeHeaders) > 0 {
			w.Header().Set("Access-Control-Expose-Headers", strings.Join(rule.ExposeHeaders, ", "))
		}
		w.Header().Set("Vary", "Origin")
		return
	}
}

func originAllowed(rule corsRule, origin string) bool {
	for _, p := range rule.AllowedOrigins {
		if matchOrigin(p, origin) {
			return true
		}
	}
	return false
}

func headersAllowed(rule corsRule, requested []string) bool {
	for _, want := range requested {
		ok := false
		for _, p := range rule.AllowedHeaders {
			if matchOrigin(strings.ToLower(p), strings.ToLower(want)) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for part := range strings.SplitSeq(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// ---- website ----

type websiteConfig struct {
	IndexDocument struct {
		Suffix string `xml:"Suffix"`
	} `xml:"IndexDocument"`
	ErrorDocument struct {
		Key string `xml:"Key"`
	} `xml:"ErrorDocument"`
}

// serveWebsite serves the bucket-website index or error document when the
// bucket has a website configuration. Returns true when it wrote a response.
func (s *Server) serveWebsite(w http.ResponseWriter, r *http.Request, bucket, key string, notFound bool) bool {
	bk, err := s.store.GetBucket(bucket)
	if err != nil || bk.Website == "" {
		return false
	}
	var cfg websiteConfig
	if xml.Unmarshal([]byte(bk.Website), &cfg) != nil {
		return false
	}
	serveKey := ""
	status := 200
	if notFound {
		if cfg.ErrorDocument.Key == "" {
			return false
		}
		serveKey, status = cfg.ErrorDocument.Key, http.StatusNotFound
	} else {
		if cfg.IndexDocument.Suffix == "" {
			return false
		}
		serveKey = key + cfg.IndexDocument.Suffix
	}
	v, gerr := s.store.GetVersion(bucket, serveKey, "")
	if gerr != nil || v.DeleteMarker {
		return false
	}
	f, ferr := s.store.OpenBlob(v.Blob)
	if ferr != nil {
		return false
	}
	defer f.Close()
	w.Header().Set("Content-Type", orDefault(v.ContentType, "text/html"))
	w.Header().Set("Content-Length", fmtInt(v.Size))
	w.WriteHeader(status)
	_, _ = io.Copy(w, f)
	return true
}

// ---- lifecycle ----

type lifecycleConfig struct {
	Rules []lifecycleRule `xml:"Rule"`
}

type lifecycleRule struct {
	ID     string `xml:"ID"`
	Status string `xml:"Status"`
	Prefix string `xml:"Prefix"`
	Filter struct {
		Prefix string `xml:"Prefix"`
	} `xml:"Filter"`
	Expiration struct {
		Days int    `xml:"Days"`
		Date string `xml:"Date"`
	} `xml:"Expiration"`
	NoncurrentVersionExpiration struct {
		NoncurrentDays int `xml:"NoncurrentDays"`
	} `xml:"NoncurrentVersionExpiration"`
	AbortIncompleteMultipartUpload struct {
		DaysAfterInitiation int `xml:"DaysAfterInitiation"`
	} `xml:"AbortIncompleteMultipartUpload"`
}

func (r lifecycleRule) prefix() string {
	if r.Filter.Prefix != "" {
		return r.Filter.Prefix
	}
	return r.Prefix
}

// sweepLifecycle applies expiration rules across every bucket. Storage-class
// transitions are accepted in config but never acted on (there is one storage
// class locally).
func (s *Server) sweepLifecycle() {
	buckets, err := s.store.ListBuckets()
	if err != nil {
		return
	}
	now := s.now()
	for _, bk := range buckets {
		if bk.Lifecycle == "" {
			continue
		}
		var cfg lifecycleConfig
		if xml.Unmarshal([]byte(bk.Lifecycle), &cfg) != nil {
			continue
		}
		for _, rule := range cfg.Rules {
			if rule.Status != "Enabled" {
				continue
			}
			s.applyLifecycleRule(bk.Name, rule, now)
		}
	}
}

func (s *Server) applyLifecycleRule(bucket string, rule lifecycleRule, now time.Time) {
	prefix := rule.prefix()

	// Current-version expiration.
	if days := rule.Expiration.Days; days > 0 {
		cutoff := now.Add(-time.Duration(days) * 24 * time.Hour).Unix()
		res, err := s.store.ListObjects(bucket, prefix, "", "", 10000)
		if err == nil {
			for _, e := range res.Entries {
				if e.LastModified <= cutoff {
					if _, _, err := s.store.DeleteObject(bucket, e.Key, "", false); err == nil {
						s.logf("s3: lifecycle expired %s/%s (rule %s)", bucket, e.Key, rule.ID)
					}
				}
			}
		}
	}

	// Noncurrent-version expiration.
	if days := rule.NoncurrentVersionExpiration.NoncurrentDays; days > 0 {
		cutoff := now.Add(-time.Duration(days) * 24 * time.Hour).Unix()
		res, err := s.store.ListVersions(bucket, prefix, "", "", "", 10000)
		if err == nil {
			for _, v := range res.Versions {
				if !v.IsLatest && v.LastModified <= cutoff {
					if _, _, err := s.store.DeleteObject(bucket, v.Key, v.VersionID, false); err == nil {
						s.logf("s3: lifecycle expired noncurrent %s/%s@%s (rule %s)", bucket, v.Key, v.VersionID, rule.ID)
					}
				}
			}
		}
	}

	// Abandoned multipart uploads.
	if days := rule.AbortIncompleteMultipartUpload.DaysAfterInitiation; days > 0 {
		cutoff := now.Add(-time.Duration(days) * 24 * time.Hour).Unix()
		ups, err := s.store.Uploads(bucket, prefix)
		if err == nil {
			for _, up := range ups {
				if up.Initiated <= cutoff {
					_ = s.store.AbortUpload(bucket, up.ID)
					s.logf("s3: lifecycle aborted stale upload %s/%s (rule %s)", bucket, up.Key, rule.ID)
				}
			}
		}
	}
}
