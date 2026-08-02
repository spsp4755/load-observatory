package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/spsp4755/load-observatory/internal/core"
)

func RunOnce(ctx context.Context, controllerURL string) (bool, error) {
	baseURL := strings.TrimSuffix(controllerURL, "/")
	claim, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/agent/claim", nil)
	if err != nil { return false, err }
	response, err := http.DefaultClient.Do(claim)
	if err != nil { return false, err }
	defer response.Body.Close()
	if response.StatusCode == http.StatusNoContent { return false, nil }
	if response.StatusCode != http.StatusOK { return false, fmt.Errorf("claim run: HTTP %d", response.StatusCode) }
	var assignment core.Assignment
	if err := json.NewDecoder(response.Body).Decode(&assignment); err != nil { return false, err }
	if assignment.Run.ID == "" || assignment.Target.URL == "" { return false, fmt.Errorf("invalid assignment") }

	resultBody, err := json.Marshal(RunTarget(ctx, assignment.Target, assignment.Run.Config))
	if err != nil { return false, err }
	report, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/agent/runs/"+assignment.Run.ID+"/result", bytes.NewReader(resultBody))
	if err != nil { return false, err }
	report.Header.Set("Content-Type", "application/json")
	response, err = http.DefaultClient.Do(report)
	if err != nil { return false, err }
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK { return false, fmt.Errorf("report result: HTTP %d", response.StatusCode) }
	return true, nil
}
