// Package geo provides IP geolocation lookup using the free ip-api.com service.
// No API key is required. Rate limit: 45 requests/minute on the free tier.
// Results are cached in memory to reduce API calls.
package geo

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type GeoResult struct {
	CountryCode string
	Country     string
	Cached      bool
}

type cacheEntry struct {
	result    GeoResult
	fetchedAt time.Time
}

var (
	mu    sync.Mutex
	cache = make(map[string]*cacheEntry)
)

const cacheTTL = 24 * time.Hour

// Lookup returns the country for an IP address.
// Returns empty strings on failure (private IPs, rate limit, etc.).
func Lookup(ip string) GeoResult {
	// Skip private / loopback
	parsed := net.ParseIP(ip)
	if parsed == nil || isPrivate(parsed) {
		return GeoResult{}
	}

	mu.Lock()
	if e, ok := cache[ip]; ok && time.Since(e.fetchedAt) < cacheTTL {
		mu.Unlock()
		r := e.result
		r.Cached = true
		return r
	}
	mu.Unlock()

	result := fetchFromAPI(ip)

	mu.Lock()
	cache[ip] = &cacheEntry{result: result, fetchedAt: time.Now()}
	mu.Unlock()
	return result
}

func fetchFromAPI(ip string) GeoResult {
	url := fmt.Sprintf("http://ip-api.com/json/%s?fields=status,country,countryCode", ip)
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		log.Printf("geo lookup failed for %s: %v", ip, err)
		return GeoResult{}
	}
	defer resp.Body.Close()

	var data struct {
		Status      string `json:"status"`
		Country     string `json:"country"`
		CountryCode string `json:"countryCode"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil || data.Status != "success" {
		return GeoResult{}
	}
	return GeoResult{
		CountryCode: strings.ToUpper(data.CountryCode),
		Country:     data.Country,
	}
}

func isPrivate(ip net.IP) bool {
	privateRanges := []string{
		"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
		"127.0.0.0/8", "::1/128", "fc00::/7",
	}
	for _, cidr := range privateRanges {
		_, network, _ := net.ParseCIDR(cidr)
		if network != nil && network.Contains(ip) {
			return true
		}
	}
	return false
}
