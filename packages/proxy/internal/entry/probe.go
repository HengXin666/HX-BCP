package entry

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/HengXin666/hx-pxy/internal/store"
)

const (
	exitProbeHost       = "ip-api.com"
	exitProbePath       = "/json/?fields=status,message,countryCode,city,as,hosting,proxy,mobile,query"
	exitProbeBatch      = 30
	exitProbeWorkers    = 4
	exitProbeRetryAfter = 6 * time.Hour
)

// ProfileStore is the persistence exit probing needs.
type ProfileStore interface {
	UpsertNodeExitProfile(context.Context, store.NodeExitProfileRecord) error
	ListNodeExitProfiles(context.Context) ([]store.NodeExitProfileRecord, error)
}

type exitAnswer struct {
	Status      string `json:"status"`
	Message     string `json:"message"`
	CountryCode string `json:"countryCode"`
	City        string `json:"city"`
	AS          string `json:"as"`
	Hosting     bool   `json:"hosting"`
	Proxy       bool   `json:"proxy"`
	Mobile      bool   `json:"mobile"`
	Query       string `json:"query"`
}

// dueForProbe picks the nodes whose exit measurement is missing or old. A
// failed measurement waits exitProbeRetryAfter before the next try.
func dueForProbe(snapshot *Snapshot, profiles []store.NodeExitProfileRecord, now time.Time, limit int) []Candidate {
	byID := make(map[string]store.NodeExitProfileRecord, len(profiles))
	for _, profile := range profiles {
		byID[profile.NodeID] = profile
	}
	due := make([]Candidate, 0, limit)
	for _, candidate := range snapshot.Nodes {
		if len(due) >= limit {
			break
		}
		if !candidate.Alive {
			continue
		}
		profile, measured := byID[candidate.ID]
		switch {
		case !measured:
		case profile.Success && now.Sub(profile.SampledAt) >= ProfileTTL/2:
		case !profile.Success && now.Sub(profile.SampledAt) >= exitProbeRetryAfter:
		default:
			continue
		}
		due = append(due, candidate)
	}
	return due
}

// probeExits measures one batch of nodes and stores the results.
func (r *Router) probeExits(ctx context.Context, profiles ProfileStore, now time.Time) (int, error) {
	snapshot := r.snapshots.load()
	existing, err := profiles.ListNodeExitProfiles(ctx)
	if err != nil {
		return 0, err
	}
	due := dueForProbe(snapshot, existing, now, exitProbeBatch)
	work := make(chan Candidate)
	var group sync.WaitGroup
	for worker := 0; worker < exitProbeWorkers; worker++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for candidate := range work {
				record := r.measureExit(ctx, snapshot, candidate)
				_ = profiles.UpsertNodeExitProfile(ctx, record)
			}
		}()
	}
	for _, candidate := range due {
		work <- candidate
	}
	close(work)
	group.Wait()
	return len(due), nil
}

// measureExit asks the exit lookup service through one node.
func (r *Router) measureExit(ctx context.Context, snapshot *Snapshot, candidate Candidate) store.NodeExitProfileRecord {
	record := store.NodeExitProfileRecord{NodeID: candidate.ID, SampledAt: time.Now().UTC()}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	conn, err := r.dialHub(ctx, snapshot, candidate.HubUser, net.JoinHostPort(exitProbeHost, "80"))
	if err != nil {
		record.Error = truncate(err.Error(), 200)
		return record
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	answer, err := readExitAnswer(conn)
	if err != nil {
		record.Error = truncate(err.Error(), 200)
		return record
	}
	return profileFromAnswer(record, answer)
}

func readExitAnswer(conn net.Conn) (exitAnswer, error) {
	request := "GET " + exitProbePath + " HTTP/1.1\r\nHost: " + exitProbeHost + "\r\nUser-Agent: hx-pxy\r\nConnection: close\r\n\r\n"
	if _, err := io.WriteString(conn, request); err != nil {
		return exitAnswer{}, err
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		return exitAnswer{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return exitAnswer{}, fmt.Errorf("exit lookup answered %s", response.Status)
	}
	var answer exitAnswer
	if err := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&answer); err != nil {
		return exitAnswer{}, err
	}
	if answer.Status != "success" {
		return exitAnswer{}, fmt.Errorf("exit lookup failed: %s", answer.Message)
	}
	return answer, nil
}

// profileFromAnswer turns a lookup answer into a stored profile. An exit is
// residential when the lookup flags it neither as hosting nor as a known proxy.
func profileFromAnswer(record store.NodeExitProfileRecord, answer exitAnswer) store.NodeExitProfileRecord {
	record.Success = true
	record.ExitIP = answer.Query
	record.CountryCode = strings.ToUpper(answer.CountryCode)
	record.City = answer.City
	asNumber, asName, _ := strings.Cut(answer.AS, " ")
	record.ASN, _ = strconv.Atoi(strings.TrimPrefix(strings.ToUpper(asNumber), "AS"))
	record.ASName = asName
	record.Hosting = answer.Hosting
	record.Mobile = answer.Mobile
	record.Residential = !answer.Hosting && !answer.Proxy
	return record
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
