// Package health exposes a deep /healthz that probes every critical
// dependency. The old shallow `{"ok": true}` would happily return 200 even
// when the DB pool was exhausted or MinIO was down — useless for ops.
package health

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Pinger is anything that can confirm its dependency is reachable.
// Both *storage.Client and *redis.Client satisfy this with thin wrappers.
type Pinger interface {
	Healthz(ctx context.Context) error
}

type Checker struct {
	pool   *pgxpool.Pool
	store  Pinger
	srsURL string // e.g. http://srs:1985/api/v1/summaries (empty to skip)
}

func New(pool *pgxpool.Pool, store Pinger, srsURL string) *Checker {
	return &Checker{pool: pool, store: store, srsURL: srsURL}
}

// Handler probes everything in parallel with a 2s budget. Returns 200
// only if all probes succeed; 503 otherwise. The JSON body lists per-
// component latency + error for ops dashboards.
func (c *Checker) Handler() gin.HandlerFunc {
	return func(g *gin.Context) {
		ctx, cancel := context.WithTimeout(g.Request.Context(), 2*time.Second)
		defer cancel()

		results := struct {
			OK      bool              `json:"ok"`
			Checked time.Time         `json:"checked"`
			Latency map[string]string `json:"latency"`
			Errors  map[string]string `json:"errors,omitempty"`
		}{
			Checked: time.Now().UTC(),
			Latency: make(map[string]string),
			Errors:  make(map[string]string),
		}

		var mu sync.Mutex
		var wg sync.WaitGroup
		probe := func(name string, fn func(context.Context) error) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				start := time.Now()
				err := fn(ctx)
				mu.Lock()
				defer mu.Unlock()
				results.Latency[name] = time.Since(start).String()
				if err != nil {
					results.Errors[name] = err.Error()
				}
			}()
		}

		probe("postgres", func(ctx context.Context) error {
			return c.pool.Ping(ctx)
		})
		if c.store != nil {
			probe("minio", func(ctx context.Context) error {
				return c.store.Healthz(ctx)
			})
		}
		if c.srsURL != "" {
			probe("srs", func(ctx context.Context) error {
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.srsURL, nil)
				if err != nil {
					return err
				}
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					return err
				}
				defer resp.Body.Close()
				if resp.StatusCode >= 500 {
					return fmt.Errorf("srs returned %d", resp.StatusCode)
				}
				return nil
			})
		}

		wg.Wait()
		results.OK = len(results.Errors) == 0
		status := http.StatusOK
		if !results.OK {
			status = http.StatusServiceUnavailable
		}
		g.JSON(status, results)
	}
}
