package entry

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/HengXin666/hx-pxy/internal/store"
	"gopkg.in/yaml.v3"
)

const (
	defaultSlotsPerCountry = 3
	maximumSlotsPerCountry = 20
	maximumCountries       = 12
)

// ClashOptions shape a tenant's Clash profile.
type ClashOptions struct {
	// PanelHost is the host the profile was fetched from, the last fallback.
	PanelHost string
	Host      string
	Port      int
	Pool      string
	Countries []string
	Slots     int
	Protocol  string
}

// RenderClash writes a Clash profile whose proxies are stable slots on the
// entry. Every proxy name and username is a pure function of pool, country and
// index, so refreshing the profile never renames a node a user picked, and the
// exit behind a name only changes when its slot rebinds.
func RenderClash(tenant store.EntryTenantRecord, secret string, snapshot *Snapshot, options ClashOptions) ([]byte, error) {
	if options.Host == "" || options.Port == 0 {
		return nil, fmt.Errorf("%w: entry host and port are required", ErrInvalidTenant)
	}
	pool := strings.ToLower(options.Pool)
	if pool == "" {
		pool = PoolSubscription
	}
	if !containsString(tenant.Pools, pool) {
		return nil, fmt.Errorf("%w: %s", ErrPoolDenied, pool)
	}
	slots := options.Slots
	if slots <= 0 {
		slots = defaultSlotsPerCountry
	}
	slots = min(slots, maximumSlotsPerCountry)
	countries := options.Countries
	if len(countries) == 0 && pool != PoolDirect && pool != PoolResidential {
		countries = topCountries(snapshot, pool, maximumCountries)
	}
	kind := "socks5"
	if strings.EqualFold(options.Protocol, "http") {
		kind = "http"
	}
	proxies := make([]map[string]any, 0, len(countries)*slots+1)
	names := make([]string, 0, cap(proxies))
	add := func(name, username string) {
		proxies = append(proxies, map[string]any{
			"name": name, "type": kind, "server": options.Host, "port": options.Port,
			"username": username, "password": secret,
		})
		names = append(names, name)
	}
	prefix := tenant.Name + "-pool-" + pool
	if pool == PoolDirect {
		add("direct", prefix)
	} else {
		add(strings.ToUpper(pool)+"-rotate", prefix)
	}
	for _, country := range countries {
		country = strings.ToUpper(strings.TrimSpace(country))
		if len(country) != 2 || !isLetters(country) {
			return nil, fmt.Errorf("%w: country %q", ErrInvalidUsername, country)
		}
		for index := 1; index <= slots; index++ {
			label := fmt.Sprintf("%s-%02d", country, index)
			slotName := strings.ToLower(pool + "_" + country + "_" + strconv.Itoa(index))
			add(strings.ToUpper(pool)+"-"+label, prefix+"-cc-"+strings.ToLower(country)+"-session-"+slotName+"-ttl-1440")
		}
	}
	document := map[string]any{
		"mode":    "rule",
		"proxies": proxies,
		"proxy-groups": []map[string]any{
			{"name": "PROXY", "type": "select", "proxies": append([]string{"AUTO"}, names...)},
			{"name": "AUTO", "type": "url-test", "proxies": names, "url": "https://www.gstatic.com/generate_204", "interval": 300},
		},
		"rules": []string{"MATCH,PROXY"},
	}
	return yaml.Marshal(document)
}

// topCountries returns the countries with the most live nodes in a pool.
func topCountries(snapshot *Snapshot, pool string, limit int) []string {
	counts := map[string]int{}
	for _, candidate := range snapshot.Nodes {
		if candidate.Alive && candidate.Country != "" && containsString(candidate.Pools, pool) {
			counts[candidate.Country]++
		}
	}
	countries := make([]string, 0, len(counts))
	for country := range counts {
		countries = append(countries, country)
	}
	sort.Slice(countries, func(left, right int) bool {
		if counts[countries[left]] != counts[countries[right]] {
			return counts[countries[left]] > counts[countries[right]]
		}
		return countries[left] < countries[right]
	})
	if len(countries) > limit {
		countries = countries[:limit]
	}
	return countries
}

func atoiPort(value string) int {
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return 0
	}
	return port
}
