package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/spsp4755/load-observatory/internal/core"
)

func RunOnce(ctx context.Context, controllerURL string) (bool, error) {
	baseURL := strings.TrimSuffix(controllerURL, "/")
	heartbeat, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/agent/heartbeat", nil)
	if err != nil {
		return false, err
	}
	if response, err := http.DefaultClient.Do(heartbeat); err != nil {
		return false, err
	} else {
		_ = response.Body.Close()
	}
	claim, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/agent/claim", nil)
	if err != nil {
		return false, err
	}
	response, err := http.DefaultClient.Do(claim)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNoContent {
		return false, nil
	}
	if response.StatusCode != http.StatusOK {
		return false, fmt.Errorf("claim run: HTTP %d", response.StatusCode)
	}
	var assignment core.Assignment
	if err := json.NewDecoder(response.Body).Decode(&assignment); err != nil {
		return false, err
	}
	if assignment.Run.ID == "" || assignment.Target.URL == "" {
		return false, fmt.Errorf("invalid assignment")
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	go watchCancellation(runCtx, baseURL, assignment.Run.ID, cancel, done)
	resultBody, err := json.Marshal(RunTarget(runCtx, assignment.Target, assignment.Run.Config))
	close(done)
	if err != nil {
		return false, err
	}
	report, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/agent/runs/"+assignment.Run.ID+"/result", bytes.NewReader(resultBody))
	if err != nil {
		return false, err
	}
	report.Header.Set("Content-Type", "application/json")
	response, err = http.DefaultClient.Do(report)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return false, fmt.Errorf("report result: HTTP %d", response.StatusCode)
	}
	return true, nil
}

func watchCancellation(ctx context.Context, baseURL, runID string, cancel context.CancelFunc, done <-chan struct{}) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-ticker.C:
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/runs/"+runID, nil)
			if err != nil {
				continue
			}
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				continue
			}
			var run core.Run
			if response.StatusCode == http.StatusOK {
				_ = json.NewDecoder(response.Body).Decode(&run)
			}
			_ = response.Body.Close()
			if run.Status == "cancelled" {
				cancel()
				return
			}
		}
	}
}
