package core

import "fmt"

// Saturation states. A high KV cache usage on its own is normal operation, not
// distress, so it never produces Saturated by itself.
const (
	SaturationUnknown       = "unknown"
	SaturationHeadroom      = "headroom"
	SaturationSaturated     = "saturated"
	SaturationConfigLimited = "config_limited"
)

type SaturationVerdict struct {
	State    string `json:"state"`
	Headline string `json:"headline"`
	Detail   string `json:"detail,omitempty"`
	// The three signals the verdict rests on, averaged over the samples.
	AvgWaiting       float64 `json:"avg_waiting"`
	PeakWaiting      float64 `json:"peak_waiting"`
	AvgRunning       float64 `json:"avg_running"`
	PeakRunning      float64 `json:"peak_running"`
	AvgKVCacheUsage  float64 `json:"avg_kv_cache_usage"`
	PeakKVCacheUsage float64 `json:"peak_kv_cache_usage"`
	PreemptionRate   float64 `json:"preemption_rate"`
	Samples          int     `json:"samples"`
}

// AssessSaturation decides whether the model server actually ran out of capacity
// during the run. Saturation requires sustained queueing AND preemption: a full
// KV cache with an empty queue and no preemption is vLLM working as designed.
func AssessSaturation(samples []MonitoringSample, config RunConfig) SaturationVerdict {
	verdict := SaturationVerdict{State: SaturationUnknown, Headline: "서버측 지표가 없어 포화 여부를 판단할 수 없습니다."}
	waiting := newSeries()
	running := newSeries()
	kv := newSeries()
	preemptions := newSeries()
	for _, sample := range samples {
		waiting.observe(sample, MetricRequestsWaiting)
		running.observe(sample, MetricRequestsRunning)
		kv.observe(sample, MetricKVCacheUsage)
		preemptions.observe(sample, MetricPreemptionRate)
	}
	if waiting.count == 0 && kv.count == 0 {
		return verdict
	}
	verdict.Samples = max(waiting.count, kv.count)
	verdict.AvgWaiting, verdict.PeakWaiting = waiting.mean(), waiting.peak
	verdict.AvgRunning, verdict.PeakRunning = running.mean(), running.peak
	verdict.AvgKVCacheUsage, verdict.PeakKVCacheUsage = kv.mean(), kv.peak
	verdict.PreemptionRate = preemptions.mean()

	// Sustained queueing means most sampled seconds had someone waiting, not that
	// a single spike happened.
	sustainedQueue := waiting.count > 0 && waiting.fractionActive() >= 0.25
	preempting := preemptions.peak > 0

	switch {
	case sustainedQueue && preempting:
		verdict.State = SaturationSaturated
		verdict.Headline = fmt.Sprintf("서버가 포화했습니다. 대기 요청이 측정 구간의 %.0f%%에서 발생하고 preemption이 초당 %.2f건 일어났습니다.", waiting.fractionActive()*100, preemptions.mean())
		verdict.Detail = "preemption은 KV 캐시가 부족해 이미 진행한 작업을 버리고 다시 계산한다는 뜻입니다. 이 부하는 운영 한계를 넘었습니다."
	case sustainedQueue && config.Server.MaxNumSeqs > 0 && running.peak >= float64(config.Server.MaxNumSeqs):
		verdict.State = SaturationConfigLimited
		verdict.Headline = fmt.Sprintf("설정 한계입니다. 동시 실행이 max_num_seqs(%d)에 도달한 상태로 요청이 대기했습니다.", config.Server.MaxNumSeqs)
		verdict.Detail = "하드웨어가 아니라 서버 설정이 상한입니다. GPU 증설이 아니라 max_num_seqs 조정이 먼저입니다."
	case sustainedQueue:
		verdict.State = SaturationConfigLimited
		verdict.Headline = fmt.Sprintf("요청이 대기했지만 preemption은 없습니다. 동시 실행은 최대 %.0f건에서 멈췄습니다.", running.peak)
		verdict.Detail = "KV 캐시가 아니라 동시 실행 상한(max_num_seqs)이나 그 앞단이 병목일 가능성이 높습니다. 서버 설정을 확인하세요."
	default:
		verdict.State = SaturationHeadroom
		verdict.Headline = fmt.Sprintf("여유가 있습니다. 대기 요청이 거의 없었고 preemption도 없었습니다 (KV 캐시 평균 %.0f%%).", kv.mean()*100)
		verdict.Detail = "KV 캐시 사용률이 높은 것 자체는 정상입니다. vLLM은 배치를 키우려고 캐시를 의도적으로 채웁니다. 부하를 더 올려 한계를 찾으세요."
	}
	return verdict
}

type RunValidity struct {
	Trustworthy bool     `json:"trustworthy"`
	Reasons     []string `json:"reasons,omitempty"`
}

// AssessRunValidity looks for conditions that make the measurement meaningless
// regardless of how good the latency numbers look. A thermally throttled GPU
// reports a capacity ceiling that is not the model's ceiling.
func AssessRunValidity(samples []MonitoringSample, result RunResult, config RunConfig) RunValidity {
	validity := RunValidity{Trustworthy: true}
	xid := newSeries()
	power := newSeries()
	thermal := newSeries()
	corrupted := newSeries()
	clock := newSeries()
	prefix := newSeries()
	for _, sample := range samples {
		xid.observe(sample, MetricXIDErrors)
		power.observe(sample, MetricPowerViolationRate)
		thermal.observe(sample, MetricThermalViolationRate)
		corrupted.observe(sample, MetricCorruptedRate)
		clock.observe(sample, MetricSMClockMHz)
		prefix.observe(sample, MetricPrefixCacheHitRate)
	}
	if xid.peak > 0 {
		validity.add(fmt.Sprintf("GPU XID 오류가 보고되었습니다 (코드 %.0f). 하드웨어·드라이버 문제이므로 이 결과는 용량 판정에 쓸 수 없습니다.", xid.peak))
	}
	if power.peak > 0 {
		validity.add("전력 제한으로 GPU가 스로틀링되었습니다. 측정된 한계는 모델의 한계가 아니라 전력 한계입니다.")
	}
	if thermal.peak > 0 {
		validity.add("온도 제한으로 GPU가 스로틀링되었습니다. 냉각 상태를 확인한 뒤 다시 측정하세요.")
	}
	if corrupted.peak > 0 {
		validity.add("서버가 손상된(NaN) 출력을 보고했습니다. 응답 내용을 신뢰할 수 없습니다.")
	}
	// A clock that falls materially between the start and the end of the run is
	// the ground truth for throttling even when the violation counters are absent.
	if drop := clock.declinePercent(); drop >= 5 {
		validity.add(fmt.Sprintf("SM 클럭이 실행 중 %.0f%% 떨어졌습니다. 스로틀링이 용량 한계로 오인됩니다.", drop))
	}
	if prefix.count > 0 {
		hit := prefix.mean()
		switch config.CachePolicy {
		case CachePolicyBypass:
			if hit > 0.5 {
				validity.add(fmt.Sprintf("캐시 우회 설정인데 prefix 캐시 히트율이 %.0f%%입니다. 프롬프트가 실제로 우회되지 않아 TTFT가 낙관적입니다.", hit*100))
			}
		case CachePolicyReuse:
			if hit < 0.2 {
				validity.add(fmt.Sprintf("캐시 활용 설정인데 prefix 캐시 히트율이 %.0f%%입니다. 서버에서 prefix 캐시가 꺼져 있을 수 있습니다.", hit*100))
			}
		}
	}
	return validity
}

func (v *RunValidity) add(reason string) {
	v.Trustworthy = false
	v.Reasons = append(v.Reasons, reason)
}

// Latency attribution outcomes.
const (
	AttributionUnknown  = "unknown"
	AttributionServer   = "server_bound"
	AttributionExternal = "client_or_network_bound"
	// AttributionMismatch means the server's own queue and prefill times are
	// larger than the TTFT our client observed, so the scraped metrics describe
	// traffic that is not (only) ours and cannot attribute this run.
	AttributionMismatch = "metrics_not_this_run"
)

type LatencyAttribution struct {
	Available           bool    `json:"available"`
	Verdict             string  `json:"verdict"`
	Headline            string  `json:"headline"`
	ClientTTFTMillis    int64   `json:"client_ttft_millis"`
	ServerQueueMillis   float64 `json:"server_queue_millis"`
	ServerPrefillMillis float64 `json:"server_prefill_millis"`
	AccountedMillis     float64 `json:"accounted_millis"`
	UnaccountedMillis   float64 `json:"unaccounted_millis"`
	UnaccountedPercent  float64 `json:"unaccounted_percent"`
}

// unaccountedLimitPercent is how much of the client-observed TTFT may sit outside
// the server's own queue and prefill accounting before the bottleneck is more
// likely to be the load generator, the load balancer or connection setup than
// the model.
const unaccountedLimitPercent = 30

// mismatchTolerance is how far the server's accounted time may exceed the
// client-observed TTFT before the two are describing different traffic. Some
// slack is needed because the two are sampled differently.
const mismatchTolerance = 1.2

// AttributeTTFT checks whether the TTFT the client measured is explained by the
// server's own queue and prefill time. Without this check a slow load generator
// or a slow proxy is indistinguishable from a slow model, which is the most
// common way a load test blames the wrong component.
func AttributeTTFT(result RunResult, samples []MonitoringSample) LatencyAttribution {
	attribution := LatencyAttribution{Verdict: AttributionUnknown, Headline: "서버측 단계별 지연 지표가 없어 병목을 귀속할 수 없습니다."}
	queue := newSeries()
	prefill := newSeries()
	for _, sample := range samples {
		queue.observe(sample, MetricQueueTimeP95)
		prefill.observe(sample, MetricPrefillTimeP95)
	}
	client := result.TTFT.P95Millis
	if client == 0 {
		client = result.TTFTP95Millis
	}
	if client == 0 || (queue.count == 0 && prefill.count == 0) {
		return attribution
	}
	attribution.Available = true
	attribution.ClientTTFTMillis = client
	attribution.ServerQueueMillis = queue.mean()
	attribution.ServerPrefillMillis = prefill.mean()
	attribution.AccountedMillis = attribution.ServerQueueMillis + attribution.ServerPrefillMillis
	attribution.UnaccountedMillis = float64(client) - attribution.AccountedMillis
	if attribution.UnaccountedMillis < 0 {
		attribution.UnaccountedMillis = 0
	}
	attribution.UnaccountedPercent = attribution.UnaccountedMillis * 100 / float64(client)

	// The server cannot have spent longer on our request than our client waited.
	// When it reports that it has, the metrics cover other traffic on the same
	// server (or another model), so they cannot attribute this run.
	if attribution.AccountedMillis > float64(client)*mismatchTolerance {
		attribution.Verdict = AttributionMismatch
		attribution.Headline = fmt.Sprintf("서버가 보고한 대기 %.0fms + prefill %.0fms가 클라이언트 TTFT P95 %dms보다 큽니다. 이 지표는 이 실행의 요청만을 나타내지 않습니다. 같은 서버에 다른 트래픽이 있거나 다른 모델·인스턴스의 지표를 보고 있을 수 있습니다.", attribution.ServerQueueMillis, attribution.ServerPrefillMillis, client)
		return attribution
	}
	if attribution.UnaccountedPercent > unaccountedLimitPercent {
		attribution.Verdict = AttributionExternal
		attribution.Headline = fmt.Sprintf("클라이언트 TTFT P95 %dms 중 %.0fms(%.0f%%)가 서버의 대기·prefill로 설명되지 않습니다. 병목이 모델 서버가 아니라 부하 발생기·로드밸런서·연결 수립일 수 있습니다.", client, attribution.UnaccountedMillis, attribution.UnaccountedPercent)
		return attribution
	}
	attribution.Verdict = AttributionServer
	attribution.Headline = fmt.Sprintf("클라이언트 TTFT P95 %dms가 서버 대기 %.0fms + prefill %.0fms로 설명됩니다. 병목은 모델 서버입니다.", client, attribution.ServerQueueMillis, attribution.ServerPrefillMillis)
	return attribution
}

// series accumulates one metric across samples, ignoring samples where the
// metric was not collected so an absent metric never counts as zero.
type series struct {
	count    int
	sum      float64
	peak     float64
	first    float64
	last     float64
	nonZero  int
	hasFirst bool
}

func newSeries() *series { return &series{} }

func (s *series) observe(sample MonitoringSample, key string) {
	value, ok := sample.Value(key)
	if !ok {
		return
	}
	s.count++
	s.sum += value
	if !s.hasFirst {
		s.first, s.hasFirst = value, true
	}
	s.last = value
	if value > s.peak {
		s.peak = value
	}
	if value > 0 {
		s.nonZero++
	}
}

func (s *series) mean() float64 {
	if s.count == 0 {
		return 0
	}
	return s.sum / float64(s.count)
}

// fractionActive reports how much of the measured window had a non-zero value,
// which distinguishes a sustained queue from a single spike.
func (s *series) fractionActive() float64 {
	if s.count == 0 {
		return 0
	}
	return float64(s.nonZero) / float64(s.count)
}

// declinePercent reports how far the metric fell from its peak to its final
// value, which is how throttling shows up in a clock reading.
func (s *series) declinePercent() float64 {
	if s.count < 3 || s.peak <= 0 {
		return 0
	}
	return (s.peak - s.last) * 100 / s.peak
}
