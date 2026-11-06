package proxy

import (
	"fmt"
	"sort"
	"strings"
)

// Stats is a snapshot of the server's counters.
type Stats struct {
	Accepted, Active, Completed, Failed int64
	// FailedBy counts failed connections keyed by result string.
	FailedBy map[string]int64
	// Up and Down are relayed byte totals (client->upstream, upstream->client).
	Up, Down int64
}

// Stats returns a snapshot of the counters.
func (s *Server) Stats() Stats {
	st := Stats{
		Accepted:  s.accepted.Load(),
		Active:    s.active.Load(),
		Completed: s.completed.Load(),
		Failed:    s.failed.Load(),
		Up:        s.up.Load(),
		Down:      s.down.Load(),
		FailedBy:  map[string]int64{},
	}
	s.mu.Lock()
	for k, v := range s.failedBy {
		st.FailedBy[k] = v
	}
	s.mu.Unlock()
	return st
}

// failedByString renders FailedBy as a stable "k=v,k=v" string sorted by key.
func (st Stats) failedByString() string {
	keys := make([]string, 0, len(st.FailedBy))
	for k := range st.FailedBy {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s=%d", k, st.FailedBy[k])
	}
	return strings.Join(parts, ",")
}

// LogStats logs one line with every Stats field.
func (s *Server) LogStats() {
	st := s.Stats()
	s.log.Info("stats",
		"accepted", st.Accepted, "active", st.Active,
		"completed", st.Completed, "failed", st.Failed,
		"failed_by", st.failedByString(),
		"up", st.Up, "down", st.Down)
}
