package live

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// RunSRSReconcileLoop polls SRS every reconcileInterval to make sure
// the DB's idea of "which rooms are live" matches reality. Two
// failure modes this catches:
//
//  1. **SRS container restarts** mid-stream. SRS forgets every active
//     publisher; the API never receives `on_unpublish` for any of
//     them. Without reconcile, those rooms stay status=1 forever and
//     appear in Discover with stale viewer counts.
//
//  2. **OBS / RTMP publisher crashes silently** without sending the
//     RTMP unpublish packet. SRS detects this only after `publish_normal_timeout`
//     (7s in srs.conf) and fires on_unpublish — usually OK, but the
//     network can swallow that callback too. Reconcile is the safety
//     net.
//
// We never go the other way (i.e. mark a DB-non-live stream as live
// just because SRS sees it): the SRS hook is the only path that
// SetLive's, and if it fired, the row is already live. Asymmetric
// reconcile keeps the loop conservative.
//
// `srsBase` is the SRS HTTP API URL (e.g. http://srs:1985/api/v1)
// stripped of the trailing /summaries from health.New. Empty disables
// the loop (dev mode).
func RunSRSReconcileLoop(ctx context.Context, repo *Repo, srsAPIBase string, log *slog.Logger) {
	base := strings.TrimSuffix(srsAPIBase, "/")
	// The health-check config passes ".../api/v1/summaries". Strip
	// the trailing /summaries so we can append /streams.
	base = strings.TrimSuffix(base, "/summaries")
	if base == "" {
		log.Info("live reconcile: SRS_API_BASE_URL empty, loop disabled")
		return
	}
	streamsURL := base + "/streams"

	// One sweep on startup so we don't carry zombies through the first hour.
	reconcile(ctx, repo, streamsURL, log)
	t := time.NewTicker(60 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			reconcile(ctx, repo, streamsURL, log)
		}
	}
}

func reconcile(ctx context.Context, repo *Repo, streamsURL string, log *slog.Logger) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(cctx, "GET", streamsURL, nil)
	if err != nil {
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Warn("live reconcile: SRS unreachable", "err", err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		log.Warn("live reconcile: SRS returned non-200", "status", resp.StatusCode)
		return
	}
	var body struct {
		Streams []struct {
			App    string `json:"app"`
			Name   string `json:"name"`
			Active bool   `json:"publish"`
		} `json:"streams"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return
	}
	// Build the set of stream keys SRS currently has a live publisher
	// for. Filter out the transcoder relay variants (`_ld`, `_md`) —
	// those don't represent independent publishers.
	srsLive := make(map[string]struct{}, len(body.Streams))
	for _, s := range body.Streams {
		if s.App != "live" {
			continue
		}
		key := s.Name
		if strings.HasSuffix(key, "_ld") || strings.HasSuffix(key, "_md") {
			continue
		}
		srsLive[key] = struct{}{}
	}

	dbLive, err := repo.ActiveStreamKeys(cctx)
	if err != nil {
		log.Warn("live reconcile: ActiveStreamKeys failed", "err", err.Error())
		return
	}

	// DB says live but SRS doesn't have it → zombie. Force-end + rotate
	// the stream key so a stale URL can't be repushed before owner notices.
	zombies := 0
	for key, id := range dbLive {
		if _, alive := srsLive[key]; alive {
			continue
		}
		if err := repo.ForceEnd(cctx, id); err != nil {
			log.Warn("live reconcile: ForceEnd failed", "roomId", id, "err", err.Error())
			continue
		}
		if _, err := repo.ReleasePublisher(cctx, id); err != nil {
			log.Warn("live reconcile: ReleasePublisher failed", "roomId", id, "err", err.Error())
		}
		zombies++
	}
	if zombies > 0 {
		log.Info("live reconcile: ended zombie rooms", "count", zombies)
	}
}
