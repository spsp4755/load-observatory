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
	if err := sendHeartbeat(ctx, baseURL); err != nil {
		return false, err
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
	heartbeatDone := make(chan struct{})
	go watchCancellation(runCtx, baseURL, assignment.Run.ID, cancel, done)
	go keepHeartbeat(runCtx, baseURL, heartbeatDone)
	publish := func(progress core.RunProgress) {
		sendProgress(ctx, baseURL, assignment.Run.ID, assignment.Shard.ID, progress)
	}
	resultBody, err := json.Marshal(RunTargetWithProgress(runCtx, assignment.Target, assignment.Run.Config, publish))
	close(done)
	close(heartbeatDone)
	if err != nil {
		return false, err
	}
	report, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/agent/runs/"+assignment.Run.ID+"/shards/"+assignment.Shard.ID+"/result", bytes.NewReader(resultBody))
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

// sendProgress publishes the live once-a-second snapshot. Losing one is
// harmless, so failures are ignored rather than interrupting the run.
func sendProgress(ctx context.Context, baseURL, runID, shardID string, progress core.RunProgress) {
	body, err := json.Marshal(progress)
	if err != nil {
		return
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/agent/runs/"+runID+"/shards/"+shardID+"/progress", bytes.NewReader(body))
	if err != nil {
		return
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return
	}
	_ = response.Body.Close()
}

func sendHeartbeat(ctx context.Context, baseURL string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/agent/heartbeat", nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	_ = response.Body.Close()
	return nil
}

func keepHeartbeat(ctx context.Context, baseURL string, done <-chan struct{}) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-ticker.C:
			_ = sendHeartbeat(ctx, baseURL)
		}
	}
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
