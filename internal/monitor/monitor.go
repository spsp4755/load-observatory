package monitor

import (
	"encoding/json"
	"fmt"
	"github.com/spsp4755/load-observatory/internal/core"
	"net/http"
	"net/url"
	"strconv"
)

type Client struct{ url string }

func New(url string) Client { return Client{url: url} }
func (c Client) Sample() core.MonitoringSample {
	if c.url == "" {
		return core.MonitoringSample{Status: "unavailable", Message: "Prometheus URL not configured"}
	}
	gpu, err := c.query("DCGM_FI_DEV_GPU_UTIL")
	if err != nil {
		return core.MonitoringSample{Status: "unavailable", Message: err.Error()}
	}
	cpu, _ := c.query("100 - avg(rate(node_cpu_seconds_total{mode=\"idle\"}[1m])) * 100")
	memory, _ := c.query("node_memory_MemTotal_bytes - node_memory_MemAvailable_bytes")
	return core.MonitoringSample{Status: "collected", GPUUtilization: gpu, CPUUtilization: cpu, MemoryUsed: memory}
}

func (c Client) query(expression string) (float64, error) {
	response, err := http.Get(c.url + "/api/v1/query?query=" + url.QueryEscape(expression))
	if err != nil { return 0, err }; defer response.Body.Close()
	var payload struct {
		Status string `json:"status"`
		Data   struct {
			Result []struct {
				Value []json.RawMessage `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if json.NewDecoder(response.Body).Decode(&payload) != nil || len(payload.Data.Result) == 0 || len(payload.Data.Result[0].Value) < 2 { return 0, fmt.Errorf("metric unavailable") }
	var value string
	_ = json.Unmarshal(payload.Data.Result[0].Value[1], &value)
	return strconv.ParseFloat(value, 64)
}
